package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMilestoneAddPostsToProject covers the whole `lode milestone add` path:
// the title is the positional argument, --position rides along, the request
// goes to the scoped project's milestones endpoint, and the confirmation
// names the created milestone.
func TestMilestoneAddPostsToProject(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":"COW-MILE-2","project":"cow","title":"Publication",`+
			`"position":2,"created_by":"ada","created_at":"2026-09-03T10:00:00Z",`+
			`"updated_at":"2026-09-03T10:00:00Z"}`)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	cmd := newMilestoneAddCmd()
	cmd.SetArgs([]string{"--project", "cow", "Publication", "--position", "2"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("milestone add: %v", err)
	}
	if gotPath != "/api/v1/projects/cow/milestones" {
		t.Errorf("path = %q, want /api/v1/projects/cow/milestones", gotPath)
	}
	if !strings.Contains(gotBody, `"title":"Publication"`) || !strings.Contains(gotBody, `"position":2`) {
		t.Errorf("request body = %q, want the title and position", gotBody)
	}
	for _, want := range []string{"COW-MILE-2", "Publication"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

// TestMilestoneAddJSON: --json prints the server's own body, unreformatted.
// Driven through rootCmd because --json is a root persistent flag.
func TestMilestoneAddJSON(t *testing.T) {
	const body = `{"id":"COW-MILE-1","project":"cow","title":"Internal review","position":1}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, body)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	out, err := runLode(t, "milestone", "add", "--project", "cow", "Internal review", "--json")
	if err != nil {
		t.Fatalf("milestone add --json: %v", err)
	}
	if !strings.Contains(out, body) {
		t.Errorf("output = %q, want the raw server body", out)
	}
}
