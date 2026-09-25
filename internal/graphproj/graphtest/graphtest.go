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
// both set; otherwise the test skips. CI sets TEST_SPARQL_URL on every
// runner (hel01's always-on Oxigraph, or ubuntu-latest's ephemeral one;
// docs/self-hosted-runner.md), so there a down endpoint is a broken run. A
// local run without Oxigraph skips.
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
// cleanup, registered before the PUT is sent so a PUT the server applied
// but the client timed out on is still dropped.
//
// The endpoint is shared by concurrent builds, so graphIRI must be
// run-unique and a test's assertions must read only its own graphs or
// subjects.
//
// ponytail: no stale-graph sweeper. A crashed run's graphs stay until the
// CI Oxigraph restarts (tmpfs); isolation keeps them harmless. Add a sweep
// if leftovers ever grow the store past its tmpfs size.
func PutGraph(t *testing.T, base, graphIRI string, turtle []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, storeURL(base, graphIRI), bytes.NewReader(turtle))
	if err != nil {
		t.Fatalf("build PUT for %s: %v", graphIRI, err)
	}
	req.Header.Set("Content-Type", "text/turtle")
	DropOnCleanup(t, base, graphIRI)
	// 201 on create, 204 on replace — any 2xx is success.
	do(t, req, "PUT "+graphIRI)
}

// DropOnCleanup deletes graphIRI from the endpoint when the test ends, for a
// graph written by something other than PutGraph. A failed delete is an
// error, never fatal, so the remaining cleanups still run.
func DropOnCleanup(t *testing.T, base, graphIRI string) {
	t.Helper()
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
		t.Errorf("build DELETE for %s: %v", graphIRI, err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Errorf("DELETE %s: %v", graphIRI, err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	// 404: the graph was never written, or a repeated PUT's cleanup ran first.
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
