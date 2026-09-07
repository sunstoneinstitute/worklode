package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDocSections covers `lode doc sections [number]` (055 §4): the optional
// argument reaches the server as the number query parameter, and the view
// prints the citable <ref>#<anchor> beside the heading.
func TestDocSections(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"doc":1,"project":"worklode","ref":"WL-SPEC-25","slug":"025-docs",
			"doc_kind":"spec","doc_number":25,"doc_title":"Documents in the backbone",
			"anchor":"sec-8.2","number":"8.2","heading":"8.2 Patching accepted text",
			"depth":3,"position":11,"last_revised_in":2,"published":true}]`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")

	out, err := runLode(t, "doc", "sections", "8.2", "--project=")
	if err != nil {
		t.Fatalf("doc sections: %v\n%s", err, out)
	}
	if gotURL != "/api/v1/docs/sections?number=8.2" {
		t.Errorf("request = %s, want /api/v1/docs/sections?number=8.2", gotURL)
	}
	for _, want := range []string{"WL-SPEC-25#sec-8.2", "Patching accepted text"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// No argument: the whole corpus in scope, no number filter.
	if _, err := runLode(t, "doc", "sections", "--project=wl"); err != nil {
		t.Fatalf("doc sections (no number): %v", err)
	}
	if gotURL != "/api/v1/docs/sections?project=wl" {
		t.Errorf("request = %s, want /api/v1/docs/sections?project=wl", gotURL)
	}
}
