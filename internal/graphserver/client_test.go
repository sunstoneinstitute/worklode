package graphserver_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

// record captures one request.
type record struct {
	method, path, rawQuery, contentType, accept, auth, body string
}

// recordingServer answers every request with status and respBody.
func recordingServer(t *testing.T, status int, respBody string) (*httptest.Server, *record) {
	t.Helper()
	rec := &record{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*rec = record{
			method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"),
			accept:      r.Header.Get("Accept"),
			auth:        r.Header.Get("Authorization"),
			body:        string(b),
		}
		w.WriteHeader(status)
		io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

const graphIRI = "https://worklode.io/ns/graph/project/acme"

func authed(srvURL string) *graphserver.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok"})
	return graphserver.New(srvURL, ts)
}

func TestPutGraphCreated(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusCreated, "")
	created, err := authed(srv.URL).PutGraph(context.Background(), "main", graphIRI, []byte("<urn:a> <urn:b> <urn:c> ."))
	if err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if !created {
		t.Fatal("created = false; want true on 201")
	}
	if rec.method != http.MethodPut || rec.path != "/branches/main/graphs" {
		t.Fatalf("request = %s %s; want PUT /branches/main/graphs", rec.method, rec.path)
	}
	if rec.rawQuery != "graph=https%3A%2F%2Fworklode.io%2Fns%2Fgraph%2Fproject%2Facme" {
		t.Fatalf("query = %q; want the url-encoded graph IRI", rec.rawQuery)
	}
	if rec.contentType != "text/turtle" {
		t.Fatalf("content type = %q; want text/turtle", rec.contentType)
	}
	if rec.body != "<urn:a> <urn:b> <urn:c> ." {
		t.Fatalf("body = %q", rec.body)
	}
	if rec.auth != "Bearer tok" {
		t.Fatalf("auth = %q; want Bearer tok", rec.auth)
	}
}

func TestPutGraphReplaced(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusNoContent, "")
	created, err := authed(srv.URL).PutGraph(context.Background(), "main", graphIRI, []byte("<urn:a> <urn:b> <urn:c> ."))
	if err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if created {
		t.Fatal("created = true; want false on 204 (idempotent re-PUT)")
	}
}

func TestPutGraphError(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusForbidden, "missing readwrite role")
	_, err := authed(srv.URL).PutGraph(context.Background(), "main", graphIRI, nil)
	if err == nil {
		t.Fatal("PutGraph on 403: want an error")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "missing readwrite role") {
		t.Fatalf("error = %v; want status and body", err)
	}
}

func TestGetGraph(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusOK, "<urn:a> <urn:b> <urn:c> .")
	body, err := authed(srv.URL).GetGraph(context.Background(), "main", graphIRI)
	if err != nil {
		t.Fatalf("GetGraph: %v", err)
	}
	if string(body) != "<urn:a> <urn:b> <urn:c> ." {
		t.Fatalf("body = %q", body)
	}
	if rec.method != http.MethodGet || rec.path != "/branches/main/graphs" {
		t.Fatalf("request = %s %s; want GET /branches/main/graphs", rec.method, rec.path)
	}
	if rec.accept != "text/turtle" {
		t.Fatalf("accept = %q; want text/turtle", rec.accept)
	}
}

func TestGetGraphNotFound(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusNotFound, "graph has no visible quads")
	_, err := authed(srv.URL).GetGraph(context.Background(), "main", graphIRI)
	if !errors.Is(err, graphserver.ErrNotFound) {
		t.Fatalf("error = %v; want ErrNotFound", err)
	}
}

func TestDeleteGraph(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusNoContent, "")
	if err := authed(srv.URL).DeleteGraph(context.Background(), "main", graphIRI); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}
	if rec.method != http.MethodDelete || rec.path != "/branches/main/graphs" {
		t.Fatalf("request = %s %s; want DELETE /branches/main/graphs", rec.method, rec.path)
	}
}

func TestDeleteGraphNotFound(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusNotFound, "graph has no visible quads")
	err := authed(srv.URL).DeleteGraph(context.Background(), "main", graphIRI)
	if !errors.Is(err, graphserver.ErrNotFound) {
		t.Fatalf("error = %v; want ErrNotFound", err)
	}
}

func TestSelect(t *testing.T) {
	srv, rec := recordingServer(t, http.StatusOK, `{
		"head": {"vars": ["component"]},
		"results": {"bindings": [
			{"component": {"type": "uri", "value": "https://worklode.io/ns/id/component/comp-b"}}
		]}
	}`)
	rows, err := authed(srv.URL).Select(context.Background(), "SELECT ?component WHERE {}")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/sparql" {
		t.Fatalf("request = %s %s; want POST /sparql", rec.method, rec.path)
	}
	if rec.contentType != "application/sparql-query" {
		t.Fatalf("content type = %q; want application/sparql-query", rec.contentType)
	}
	if rec.accept != "application/sparql-results+json" {
		t.Fatalf("accept = %q", rec.accept)
	}
	if rec.body != "SELECT ?component WHERE {}" {
		t.Fatalf("body = %q; want the raw query", rec.body)
	}
	want := []map[string]string{{"component": "https://worklode.io/ns/id/component/comp-b"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v; want %v", rows, want)
	}
}

func TestSelectUnavailable(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusServiceUnavailable, "oxigraph unavailable")
	_, err := authed(srv.URL).Select(context.Background(), "SELECT * WHERE {}")
	if !errors.Is(err, graphserver.ErrSPARQLUnavailable) {
		t.Fatalf("error = %v; want ErrSPARQLUnavailable", err)
	}
}

func TestSelectBadGatewayRetryable(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusBadGateway, "upstream connect error")
	_, err := authed(srv.URL).Select(context.Background(), "SELECT * WHERE {}")
	if !errors.Is(err, graphserver.ErrSPARQLUnavailable) {
		t.Fatalf("error = %v; want ErrSPARQLUnavailable on 502", err)
	}
}

// A rollout answers every method with an ingress status, not just /sparql;
// a caller retrying through one must not have to special-case the verb.
func TestGraphStoreUnavailableIsRetryable(t *testing.T) {
	for _, status := range []int{
		http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout,
	} {
		for _, op := range []struct {
			name string
			call func(*graphserver.Client) error
		}{
			{"PutGraph", func(c *graphserver.Client) error {
				_, err := c.PutGraph(context.Background(), "main", graphIRI, nil)
				return err
			}},
			{"GetGraph", func(c *graphserver.Client) error {
				_, err := c.GetGraph(context.Background(), "main", graphIRI)
				return err
			}},
			{"DeleteGraph", func(c *graphserver.Client) error {
				return c.DeleteGraph(context.Background(), "main", graphIRI)
			}},
		} {
			t.Run(fmt.Sprintf("%s/%d", op.name, status), func(t *testing.T) {
				srv, _ := recordingServer(t, status, "upstream connect error")
				err := op.call(authed(srv.URL))
				if !errors.Is(err, graphserver.ErrUnavailable) {
					t.Fatalf("error = %v; want ErrUnavailable on %d", err, status)
				}
				if !strings.Contains(err.Error(), "upstream connect error") {
					t.Fatalf("error = %v; want the body excerpt kept", err)
				}
			})
		}
	}
}

func TestGraphStoreForbiddenNotRetryable(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusForbidden, "missing readwrite role")
	_, err := authed(srv.URL).PutGraph(context.Background(), "main", graphIRI, nil)
	if errors.Is(err, graphserver.ErrUnavailable) {
		t.Fatalf("error = %v; a 403 must not be retryable", err)
	}
}

// ErrSPARQLUnavailable is the /sparql-specific face of ErrUnavailable, so a
// caller that only asks "retryable?" can test the general one.
func TestSelectUnavailableWrapsErrUnavailable(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusServiceUnavailable, "oxigraph unavailable")
	_, err := authed(srv.URL).Select(context.Background(), "SELECT * WHERE {}")
	if !errors.Is(err, graphserver.ErrUnavailable) {
		t.Fatalf("error = %v; want ErrUnavailable", err)
	}
}

// An oversized result set must fail loudly rather than return a short answer.
func TestSelectResultSetCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"head":{"vars":["s"]},"results":{"bindings":[`)
	row := `{"s":{"type":"uri","value":"https://worklode.io/ns/id/pad/` + strings.Repeat("x", 1024) + `"}}`
	for b.Len() < 33<<20 {
		if b.Len() > len(`{"head":{"vars":["s"]},"results":{"bindings":[`) {
			b.WriteString(",")
		}
		b.WriteString(row)
	}
	b.WriteString(`]}}`)

	srv, _ := recordingServer(t, http.StatusOK, b.String())
	rows, err := authed(srv.URL).Select(context.Background(), "SELECT ?s WHERE {}")
	if err == nil {
		t.Fatalf("Select on an oversized result set: want an error, got %d rows", len(rows))
	}
	if rows != nil {
		t.Fatalf("rows = %d; want nil alongside the error", len(rows))
	}
}

func TestSelectBadRequestNotRetryable(t *testing.T) {
	srv, _ := recordingServer(t, http.StatusBadRequest, "bad query")
	_, err := authed(srv.URL).Select(context.Background(), "SELECT bogus")
	if err == nil {
		t.Fatal("Select on 400: want an error")
	}
	if errors.Is(err, graphserver.ErrSPARQLUnavailable) {
		t.Fatalf("error = %v; a 400 must not be retryable", err)
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "bad query") {
		t.Fatalf("error = %v; want status and body", err)
	}
}
