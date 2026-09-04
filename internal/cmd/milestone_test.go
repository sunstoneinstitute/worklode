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

// TestMilestoneAttachPatchesDeliverable covers `lode milestone attach
// <milestone> <deliverable>`: it PATCHes the deliverable's milestone field
// and prints the updated row.
func TestMilestoneAttachPatchesDeliverable(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"id":"COW-DEL-3","project":"cow","name":"Datapackage",`+
			`"milestone":"COW-MILE-2","created_by":"ada","created_at":"2026-09-03T10:00:00Z",`+
			`"updated_at":"2026-09-03T10:00:00Z"}`)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	cmd := newMilestoneAttachCmd()
	cmd.SetArgs([]string{"COW-MILE-2", "COW-DEL-3"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("milestone attach: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/v1/deliverables/COW-DEL-3" {
		t.Errorf("request = %s %s, want PATCH /api/v1/deliverables/COW-DEL-3", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"milestone":"COW-MILE-2"`) {
		t.Errorf("request body = %q, want the milestone id", gotBody)
	}
	for _, want := range []string{"COW-DEL-3", "COW-MILE-2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

// TestMilestoneDetachClearsMilestone covers `lode milestone detach
// <deliverable>`: it PATCHes the milestone field to "".
func TestMilestoneDetachClearsMilestone(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"id":"COW-DEL-3","project":"cow","name":"Datapackage",`+
			`"created_by":"ada","created_at":"2026-09-03T10:00:00Z","updated_at":"2026-09-03T10:00:00Z"}`)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	cmd := newMilestoneDetachCmd()
	cmd.SetArgs([]string{"COW-DEL-3"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("milestone detach: %v", err)
	}
	if !strings.Contains(gotBody, `"milestone":""`) {
		t.Errorf("request body = %q, want an empty milestone", gotBody)
	}
	if !strings.Contains(out.String(), "COW-DEL-3") {
		t.Errorf("output missing COW-DEL-3:\n%s", out.String())
	}
}

// TestMilestoneAttachRejectsTaskID: a task id passed where a deliverable id
// was wanted is a common mistake (029 §2 keeps the two attach paths
// separate) — it must be caught client-side with a message pointing at the
// task path, never sent to the server.
func TestMilestoneAttachRejectsTaskID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	cmd := newMilestoneAttachCmd()
	cmd.SetArgs([]string{"COW-MILE-2", "COW-12"})
	cmd.SetOut(io.Discard)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("attach with a task id: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "lode task edit --milestone") {
		t.Errorf("error = %q, want it to point at `lode task edit --milestone`", err.Error())
	}
}
