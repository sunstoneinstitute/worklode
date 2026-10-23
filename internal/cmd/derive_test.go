package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestDeriveCommand: each kind posts its selection to the project's derive
// route, --diff and --dry-run reach the server, and a node error makes the
// command fail after printing (WL-REQ-1860).
func TestDeriveCommand(t *testing.T) {
	var posted model.RefDeriveParams
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects/cow/refs/derive" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Fatalf("decode POST body: %v", err)
		}
		node := model.RefDeriveNode{Ref: "WL-1", Rewrites: 1, Unresolved: []string{"WL-9"}, Diff: "WL-1 body line 1\n-/WL-2\n+WL-2\n",
			Added: []model.InlineRef{{Field: "body", ToRef: "WL-2"}}}
		if len(posted.Refs) > 0 && posted.Refs[0] == "BAD" {
			node.Error = "boom"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.RefDeriveReport{Nodes: []model.RefDeriveNode{node}})
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	out, err := runLode(t, "task", "derive", "WL-1", "--project", "cow", "--dry-run")
	if err != nil {
		t.Fatalf("task derive: %v\n%s", err, out)
	}
	if posted.Kind != "task" || posted.Refs[0] != "WL-1" || !posted.DryRun || posted.Diff {
		t.Errorf("posted %+v", posted)
	}
	if !strings.Contains(out, "WL-1  +1 -0 refs, 1 rewrites, unresolved: WL-9") {
		t.Errorf("table = %q", out)
	}

	out, err = runLode(t, "rule", "derive", "--all", "--diff", "--project", "cow")
	if err != nil {
		t.Fatalf("rule derive: %v\n%s", err, out)
	}
	if posted.Kind != "rule" || !posted.All || !posted.Diff {
		t.Errorf("posted %+v", posted)
	}
	if !strings.Contains(out, "+WL-2\n+ WL-1 body -> WL-2\n") {
		t.Errorf("diff = %q", out)
	}

	if _, err = runLode(t, "doc", "derive", "--kind", "plan", "--status", "draft", "--project", "cow"); err != nil {
		t.Fatal(err)
	}
	if posted.Kind != "doc" || posted.DocKind != "plan" || len(posted.Status) != 1 {
		t.Errorf("posted %+v", posted)
	}

	if _, err = runLode(t, "task", "derive", "BAD", "--project", "cow"); err == nil || !strings.Contains(err.Error(), "1 failed") {
		t.Errorf("node error: err = %v", err)
	}
	if _, err = runLode(t, "doc", "derive", "--project", "cow"); err == nil {
		t.Error("selecting nothing succeeded")
	}
}
