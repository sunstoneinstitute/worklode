package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestClauseEditRequiresFile covers the one required flag: no --file, no
// network call, straight refusal.
func TestClauseEditRequiresFile(t *testing.T) {
	out, err := runLode(t, "clause", "edit", "WL-CL-1")
	if err == nil {
		t.Fatalf("lode clause edit WL-CL-1 (no --file) succeeded\noutput: %s", out)
	}
	want := `no file: pass a path, or "-" to read the body from stdin`
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

// TestClauseEditOnAcceptedSaysWhereItLanded: the write went to the arranging
// document's candidate revision, so the clause read back still carries the
// old text. The render alone reads as a no-op; the command has to name the
// document and the command that lands it.
func TestClauseEditOnAcceptedSaysWhereItLanded(t *testing.T) {
	accepted := model.Clause{
		Ref: "WL-CL-1", Heading: "Sub", Body: "\nOld text.\n\n",
		Status: "accepted", Version: 1,
		ArrangedIn: []model.ClauseArrangement{{DocRef: "WL-SPEC-12", Anchor: "sec-1.1", Depth: 3, ClauseVersion: 1}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/clauses/WL-CL-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(accepted)
	})
	mux.HandleFunc("PUT /api/v1/clauses/WL-CL-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(accepted)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("New text.\n"), 0o600); err != nil {
		t.Fatalf("write fixture body: %v", err)
	}

	out, err := runLode(t, "clause", "edit", "WL-CL-1", "--file", file)
	if err != nil {
		t.Fatalf("lode clause edit: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "candidate revision of WL-SPEC-12") ||
		!strings.Contains(out, "lode doc revise WL-SPEC-12 --accept") {
		t.Fatalf("output = %q; want it to name the document and how the change lands", out)
	}

	out, err = runLode(t, "clause", "edit", "WL-CL-1", "--file", file, "--json")
	if err != nil {
		t.Fatalf("lode clause edit --json: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "candidate revision") {
		t.Fatalf("--json output carries the notice: %q", out)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("--json output is not valid JSON: %q", out)
	}
}

// TestClauseEditDraftSaysNothingExtra: on a draft document the write lands at
// once, so the notice above must not appear.
func TestClauseEditDraftSaysNothingExtra(t *testing.T) {
	mux := http.NewServeMux()
	reply := model.Clause{
		Ref: "WL-CL-1", Heading: "Sub", Body: "\nNew text.\n\n", Status: "draft", Version: 1,
		ArrangedIn: []model.ClauseArrangement{{DocRef: "WL-SPEC-12", Anchor: "sec-1.1", Depth: 3, ClauseVersion: 1}},
	}
	mux.HandleFunc("GET /api/v1/clauses/WL-CL-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply)
	})
	mux.HandleFunc("PUT /api/v1/clauses/WL-CL-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("New text.\n"), 0o600); err != nil {
		t.Fatalf("write fixture body: %v", err)
	}
	out, err := runLode(t, "clause", "edit", "WL-CL-1", "--file", file)
	if err != nil {
		t.Fatalf("lode clause edit: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "candidate revision") {
		t.Fatalf("draft edit printed the revision notice: %q", out)
	}
}

// TestClauseEditReadsStdin: --file - reads the body from stdin, the way
// every sibling body-taking command does.
func TestClauseEditReadsStdin(t *testing.T) {
	var putBody model.EditClauseInput
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/clauses/WL-CL-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Clause{Ref: "WL-CL-1", Heading: "Sub", Status: "draft", Version: 1})
	})
	mux.HandleFunc("PUT /api/v1/clauses/WL-CL-1", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
			t.Fatalf("decode PUT body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Clause{Ref: "WL-CL-1", Heading: putBody.Heading, Body: putBody.Body, Status: "draft", Version: 1})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	resetFlags(t, rootCmd)
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader("Piped body.\n"))
	defer rootCmd.SetIn(nil)
	rootCmd.SetArgs([]string{"clause", "edit", "WL-CL-1", "--file", "-"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("lode clause edit --file -: %v\noutput: %s", err, buf.String())
	}
	if putBody.Body != "Piped body.\n" {
		t.Fatalf("PUT body = %q; want the piped text", putBody.Body)
	}
}

// TestClauseEditResolvesHeadingLocally covers the decided fix: when
// --heading is omitted, the current heading is fetched and used, and that
// resolution never leaks into a later invocation. Two calls in the same
// process, against two different current headings, must each pick up their
// own server response rather than the first call's.
func TestClauseEditResolvesHeadingLocally(t *testing.T) {
	heading := "Heading A"
	var putBody model.EditClauseInput
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/clauses/WL-CL-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Clause{Ref: "WL-CL-1", Heading: heading, Status: "draft", Version: 1})
	})
	mux.HandleFunc("PUT /api/v1/clauses/WL-CL-1", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
			t.Fatalf("decode PUT body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Clause{Ref: "WL-CL-1", Heading: putBody.Heading, Body: putBody.Body, Status: "draft", Version: 2})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("New body text.\n"), 0o600); err != nil {
		t.Fatalf("write fixture body: %v", err)
	}

	out, err := runLode(t, "clause", "edit", "WL-CL-1", "--file", file)
	if err != nil {
		t.Fatalf("lode clause edit WL-CL-1 --file %s: %v\noutput: %s", file, err, out)
	}
	if putBody.Heading != "Heading A" {
		t.Fatalf("PUT heading = %q; want %q", putBody.Heading, "Heading A")
	}
	if !strings.Contains(out, "Heading A") {
		t.Fatalf("output = %q; want it to render the resolved heading", out)
	}

	heading = "Heading B"
	out, err = runLode(t, "clause", "edit", "WL-CL-1", "--file", file)
	if err != nil {
		t.Fatalf("lode clause edit WL-CL-1 --file %s (2nd run): %v\noutput: %s", file, err, out)
	}
	if putBody.Heading != "Heading B" {
		t.Fatalf("2nd run PUT heading = %q; want %q (not the 1st run's leaked value)", putBody.Heading, "Heading B")
	}
}

// TestClauseVersionsCommand covers `lode clause versions <ref>` rendering
// the version-history table.
func TestClauseVersionsCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clauses/WL-CL-1/versions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"version":2,"heading":"Sub","created_at":"2026-09-22T10:00:00Z"},{"version":1,"heading":"Sub","created_at":"2026-09-21T10:00:00Z"}]`)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	out, err := runLode(t, "clause", "versions", "WL-CL-1")
	if err != nil {
		t.Fatalf("lode clause versions WL-CL-1: %v\noutput: %s", err, out)
	}
	if !strings.HasPrefix(out, "VERSION") || !strings.Contains(out, "2") || !strings.Contains(out, "1") {
		t.Fatalf("output = %q; want a version table with both versions", out)
	}
}
