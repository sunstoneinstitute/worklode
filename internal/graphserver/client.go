// Package graphserver is a client for the data-platform graph-server
// (data-platform crates/graph-server) — the knowledge-graph system of
// record that spec 009 requires the data-platform to host.
//
// graph-server's surface, unlike a plain SPARQL endpoint, is:
//   - branch-scoped Graph Store Protocol writes:
//     PUT/GET/POST/DELETE /branches/{branch}/graphs?graph=<iri>
//     (PUT answers 201 on create, 204 on replace);
//   - a read-only POST /sparql proxying the Oxigraph materialization.
//
// This client covers PUT/GET/DELETE; POST (merge) is deliberately
// unimplemented. There is no SPARQL Update endpoint: writes replace or merge
// whole named graphs. IRIs are opaque strings here; minting is owned
// elsewhere (internal/kg/iri once the platform-graph-design plan lands).
package graphserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// ErrNotFound is returned when the named graph has no visible quads on the
// requested branch.
var ErrNotFound = errors.New("graph not found")

// ErrSPARQLUnavailable is returned when /sparql is not serving: 503 from
// graph-server when Oxigraph or its materializer is down, or 502/504 from
// the ingress while graph-server itself is coming up. Callers may retry.
var ErrSPARQLUnavailable = errors.New("sparql endpoint unavailable")

// Client talks to one graph-server instance.
type Client struct {
	base string
	http *http.Client
}

// graph-server writes commit through Nessie/Iceberg and can be slow under
// load; the timeout bounds a hung connection without tripping normal writes.
// The authenticated path (New with a non-nil ts) inherits this timeout only
// because oauth2.NewClient copies Timeout from the ctx-provided http.Client
// (verified against golang.org/x/oauth2@v0.36.0); a future bump of that
// dependency could drop it silently.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// New returns a client for the graph-server at base
// (e.g. https://graph.dev.sunstoneinstitute.ai). A non-nil ts attaches a
// Bearer token to every request; nil means unauthenticated (tests, or a
// server running without AUTH_ENFORCE).
func New(base string, ts oauth2.TokenSource) *Client {
	hc := httpClient
	if ts != nil {
		hc = oauth2.NewClient(context.WithValue(context.Background(), oauth2.HTTPClient, httpClient), ts)
	}
	return &Client{base: strings.TrimRight(base, "/"), http: hc}
}

func (c *Client) graphURL(branch, graphIRI string) string {
	return c.base + "/branches/" + url.PathEscape(branch) +
		"/graphs?graph=" + url.QueryEscape(graphIRI)
}

// PutGraph replaces the named graph on branch with the given Turtle.
// The bool reports whether the graph was created (201) as opposed to
// replaced (204); an idempotent re-PUT returns false.
func (c *Client) PutGraph(ctx context.Context, branch, graphIRI string, turtle []byte) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.graphURL(branch, graphIRI), bytes.NewReader(turtle))
	if err != nil {
		return false, fmt.Errorf("build PUT graph request: %w", err)
	}
	req.Header.Set("Content-Type", "text/turtle")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("PUT graph %s on %s: %w", graphIRI, branch, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
		return true, nil
	case http.StatusNoContent:
		return false, nil
	default:
		return false, httpError("PUT graph", resp)
	}
}

// GetGraph returns the named graph on branch as Turtle. ErrNotFound means
// the graph has no visible quads there.
func (c *Client) GetGraph(ctx context.Context, branch, graphIRI string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.graphURL(branch, graphIRI), nil)
	if err != nil {
		return nil, fmt.Errorf("build GET graph request: %w", err)
	}
	req.Header.Set("Accept", "text/turtle")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET graph %s on %s: %w", graphIRI, branch, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return io.ReadAll(resp.Body)
	case http.StatusNotFound:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("graph %s on %s: %w: %s", graphIRI, branch, ErrNotFound, strings.TrimSpace(string(body)))
	default:
		return nil, httpError("GET graph", resp)
	}
}

// DeleteGraph removes the named graph from branch.
func (c *Client) DeleteGraph(ctx context.Context, branch, graphIRI string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.graphURL(branch, graphIRI), nil)
	if err != nil {
		return fmt.Errorf("build DELETE graph request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE graph %s on %s: %w", graphIRI, branch, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("graph %s on %s: %w: %s", graphIRI, branch, ErrNotFound, strings.TrimSpace(string(body)))
	default:
		return httpError("DELETE graph", resp)
	}
}

// Select runs a SPARQL SELECT against /sparql (the Oxigraph read path) and
// returns one map per solution, variable name → bound value. Unbound
// variables are absent from the map, so row[v] yields the empty string;
// use the two-value index form to tell an unbound variable from an empty
// literal. ErrSPARQLUnavailable means Oxigraph or its materializer is not
// serving.
func (c *Client) Select(ctx context.Context, query string) ([]map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/sparql", strings.NewReader(query))
	if err != nil {
		return nil, fmt.Errorf("build select request: %w", err)
	}
	req.Header.Set("Content-Type", "application/sparql-query")
	req.Header.Set("Accept", "application/sparql-results+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK: // fall through to the decode below
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return nil, fmt.Errorf("select: %w", ErrSPARQLUnavailable)
	default:
		return nil, httpError("select", resp)
	}
	var out struct {
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("select: decode results: %w", err)
	}
	rows := make([]map[string]string, 0, len(out.Results.Bindings))
	for _, b := range out.Results.Bindings {
		row := make(map[string]string, len(b))
		for v, cell := range b {
			row[v] = cell.Value
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// httpError folds a non-2xx response into one error carrying status and a
// bounded body excerpt.
func httpError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s: %d %s: %s", op, resp.StatusCode,
		http.StatusText(resp.StatusCode), strings.TrimSpace(string(body)))
}
