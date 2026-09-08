package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// setupDocProgress stands up a stub backbone answering the progress route
// with p, plus a repo whose config scopes commands to project "proj". No
// store is involved: the derivation is tested in internal/progress, and what
// this file is about is the command's rendering and its --json passthrough.
func setupDocProgress(t *testing.T, p model.ProjectProgress) *string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir repo config dir: %v", err)
	}
	cfg := "current_project = \"proj\"\nproject_key = \"WL\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".worklode", "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
	t.Chdir(repo)

	var asked string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{id}/progress", func(w http.ResponseWriter, r *http.Request) {
		asked = r.PathValue("id")
		writeTestJSON(t, w, p)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	return &asked
}

// oneActiveSpec is a project whose single spec is under way: three sections
// built, one in progress, one bound — which is not owed and so is not counted
// (066 §1.4).
func oneActiveSpec() model.ProjectProgress {
	return model.ProjectProgress{
		Project: "proj",
		Counts:  model.ProgressCounts{Active: 1},
		Bar: []model.ProgressSlice{
			{State: "built", Count: 3}, {State: "in_progress", Count: 1},
		},
		Groups: []model.ProgressGroup{
			{Key: "active", Specs: []model.ProgressSpec{{
				Doc: 66, Ref: "WL-SPEC-66", Title: "The project Progress page",
				Group: "active",
				Next:  model.ProgressAct{Kind: "in_progress", Text: "2 open task(s) in WL-PLAN-9"},
				Sections: []model.ProgressSection{
					{Anchor: "sec-1", State: "built"},
					{Anchor: "sec-2", State: "built"},
					{Anchor: "sec-3", State: "built"},
					{Anchor: "sec-4", State: "in_progress"},
					{Anchor: "sec-5", State: "bound"},
				},
			}}},
			{Key: "planning"}, {Key: "no_record"}, {Key: "built"},
		},
	}
}

// TestDocProgressTable is the human rendering: the group header, the spec's
// row with its state counts and next act, and no empty group heading. The
// project comes from the checkout's config, so nothing has to name it.
func TestDocProgressTable(t *testing.T) {
	asked := setupDocProgress(t, oneActiveSpec())

	out, err := runLode(t, "doc", "progress")
	if err != nil {
		t.Fatalf("doc progress: %v\noutput: %s", err, out)
	}
	if *asked != "proj" {
		t.Errorf("progress read for project %q, want proj", *asked)
	}
	if !strings.Contains(out, "Active · 1") {
		t.Errorf("output = %q, want the Active group header", out)
	}
	if !hasLine(out, "WL-SPEC-66", "The project Progress page",
		"built 3 · in progress 1", "2 open task(s) in WL-PLAN-9") {
		t.Errorf("output = %q, want one row for WL-SPEC-66", out)
	}
	// A group with no spec is not printed at all, heading included.
	if strings.Contains(out, "Needs planning") || strings.Contains(out, "No execution record") {
		t.Errorf("output = %q, want empty groups omitted", out)
	}
	// 032 §4 forbids a completion percentage, so the table shows none.
	if strings.Contains(out, "%") {
		t.Errorf("output = %q, want no percentage anywhere", out)
	}
}

// TestDocProgressJSON is the agent's read: --json echoes the response body
// rather than a re-encoding of the rendered table.
func TestDocProgressJSON(t *testing.T) {
	setupDocProgress(t, oneActiveSpec())

	out, err := runLode(t, "doc", "progress", "--json")
	if err != nil {
		t.Fatalf("doc progress --json: %v\noutput: %s", err, out)
	}
	for _, want := range []string{`"project":"proj"`, `"ref":"WL-SPEC-66"`, `"state":"bound"`} {
		if !strings.Contains(strings.ReplaceAll(out, " ", ""), want) {
			t.Errorf("--json output = %q, want it to carry %s", out, want)
		}
	}
}
