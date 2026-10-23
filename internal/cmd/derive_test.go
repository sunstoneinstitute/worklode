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

// TestDeriveCommandPages: --all pages through the selection one request per
// batch and renders the merged report once; a failed node does not stop the
// later pages (WL-REQ-1860).
func TestDeriveCommandPages(t *testing.T) {
	pages := map[string]model.RefDeriveReport{
		"":     {Nodes: []model.RefDeriveNode{{Ref: "WL-1", Rewrites: 1}}, Next: "WL-1"},
		"WL-1": {Nodes: []model.RefDeriveNode{{Ref: "WL-2", Error: "boom"}}, Next: "WL-2"},
		"WL-2": {Nodes: []model.RefDeriveNode{{Ref: "WL-3", Rewrites: 2}}},
	}
	var posted []model.RefDeriveParams
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in model.RefDeriveParams
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatalf("decode POST body: %v", err)
		}
		posted = append(posted, in)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pages[in.After])
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	out, err := runLode(t, "task", "derive", "--all", "--project", "cow")
	if err == nil || !strings.Contains(err.Error(), "1 failed: WL-2") {
		t.Errorf("err = %v", err)
	}
	if len(posted) != 3 || posted[0].Limit <= 0 || !posted[2].All || posted[2].After != "WL-2" {
		t.Errorf("posted %+v", posted)
	}
	for _, ref := range []string{"WL-1", "WL-2", "WL-3"} {
		if !strings.Contains(out, ref) {
			t.Errorf("output lacks %s: %q", ref, out)
		}
	}

	posted = nil
	out, _ = runLode(t, "task", "derive", "--all", "--project", "cow", "--json")
	var rep model.RefDeriveReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil || len(rep.Nodes) != 3 || rep.Next != "" {
		t.Errorf("json = %q, %v", out, err)
	}
}
