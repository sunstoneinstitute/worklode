// Package graphtest loads triples into a SPARQL 1.1 endpoint (Oxigraph) and
// queries them back, for the knowledge-graph projection tests.
//
// It is test-only and deliberately not a production client: production graph
// writes go through internal/graphserver, whose branch-scoped Graph Store
// Protocol surface this does not model. Oxigraph stands in here purely as a
// conformant store to validate ns/ and the projection against. Like
// store.OpenTestStore, it is a non-test file importing testing so tests in
// other packages can use it.
package graphtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// DefaultEndpoint is the compose/CI Oxigraph address used when
// TEST_SPARQL_URL is unset.
const DefaultEndpoint = "http://localhost:7878"

var client = &http.Client{Timeout: 15 * time.Second}

// Endpoint returns the base URL of the test SPARQL endpoint, from
// TEST_SPARQL_URL or DefaultEndpoint, after probing it with an ASK {}.
//
// An unreachable endpoint is fatal only when CI *and* TEST_SPARQL_URL are
// both set; otherwise the test skips. Both conditions are needed because the
// self-hosted runner sets CI but is deliberately Docker-less
// (docs/self-hosted-runner.md), so it has no Oxigraph to reach and never
// sets TEST_SPARQL_URL: an explicitly configured endpoint that is down is a
// broken run, an unconfigured one is a runner that was never meant to have
// it.
func Endpoint(t *testing.T) string {
	t.Helper()
	base, explicit := os.LookupEnv("TEST_SPARQL_URL")
	if base == "" {
		base, explicit = DefaultEndpoint, false
	}
	base = strings.TrimSuffix(base, "/")

	if err := probe(base); err != nil {
		if explicit && os.Getenv("CI") != "" {
			t.Fatalf("SPARQL endpoint unreachable at %s: %v", base, err)
		}
		t.Skipf("SPARQL endpoint unreachable at %s: %v", base, err)
	}
	return base
}

func probe(base string) error {
	resp, err := query(base, "ASK {}")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ASK {}: HTTP %d", resp.StatusCode)
	}
	return nil
}

// PutGraph replaces the named graph graphIRI with turtle (GSP PUT, so a
// re-load replaces rather than merges). The graph is dropped on test
// cleanup, which keeps a shared endpoint free of leftovers between runs.
func PutGraph(t *testing.T, base, graphIRI string, turtle []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, storeURL(base, graphIRI), bytes.NewReader(turtle))
	if err != nil {
		t.Fatalf("build PUT for %s: %v", graphIRI, err)
	}
	req.Header.Set("Content-Type", "text/turtle")
	// 201 on create, 204 on replace — any 2xx is success.
	do(t, req, "PUT "+graphIRI)
	t.Cleanup(func() { dropGraph(t, base, graphIRI) })
}

// Select runs a SPARQL SELECT and flattens the results to one map per
// solution, variable name to lexical value. Unbound variables are absent
// from their solution's map.
func Select(t *testing.T, base, q string) []map[string]string {
	t.Helper()
	resp, err := query(base, q)
	if err != nil {
		t.Fatalf("SPARQL query: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read SPARQL results: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		t.Fatalf("SPARQL query: HTTP %d: %s\nquery:\n%s", resp.StatusCode, body, q)
	}

	var parsed struct {
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode SPARQL results: %v\nbody: %s", err, body)
	}

	rows := make([]map[string]string, 0, len(parsed.Results.Bindings))
	for _, b := range parsed.Results.Bindings {
		row := make(map[string]string, len(b))
		for name, term := range b {
			row[name] = term.Value
		}
		rows = append(rows, row)
	}
	return rows
}

func dropGraph(t *testing.T, base, graphIRI string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, storeURL(base, graphIRI), nil)
	if err != nil {
		t.Fatalf("build DELETE for %s: %v", graphIRI, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", graphIRI, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	// 404 means a repeated PUT already registered a cleanup that ran first.
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE %s: HTTP %d", graphIRI, resp.StatusCode)
	}
}

func query(base, q string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, base+"/query", strings.NewReader(q))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/sparql-query")
	req.Header.Set("Accept", "application/sparql-results+json")
	return client.Do(req)
}

func storeURL(base, graphIRI string) string {
	return base + "/store?graph=" + url.QueryEscape(graphIRI)
}

func do(t *testing.T, req *http.Request, what string) {
	t.Helper()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t.Fatalf("%s: HTTP %d: %s", what, resp.StatusCode, body)
	}
}
