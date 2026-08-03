package graphserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

const graphIRI = "https://worklode.io/ns/graph/workstream/acme"

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
	if rec.rawQuery != "graph=https%3A%2F%2Fworklode.io%2Fns%2Fgraph%2Fworkstream%2Facme" {
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
