package api_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func flowByName(t *testing.T, flows []model.ApprovalFlow, name string) model.ApprovalFlow {
	t.Helper()
	for _, f := range flows {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no flow named %q in %d flows", name, len(flows))
	return model.ApprovalFlow{}
}

// writeFlow drops one flow file into a fresh dir and returns the dir.
func writeFlow(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadApprovalFlowsShipsTheStoryDefault(t *testing.T) {
	flows, err := api.LoadApprovalFlows("")
	if err != nil {
		t.Fatal(err)
	}
	story := flowByName(t, flows, "story")
	if story.Rev != "1" || story.Match["kind"] != "sunstone-story" {
		t.Errorf("story = rev %q match %v", story.Rev, story.Match)
	}
	if len(story.Requirements) != 6 {
		t.Fatalf("story has %d lanes, want 6", len(story.Requirements))
	}
	want := map[string][2]string{
		"analysis/peer":             {"analysis-reviewers", "Reproducible analysis"},
		"methodology/science-lead":  {"science-leads", "Methodology"},
		"methodology/domain-expert": {"domain-experts", "Methodology"},
		"report/buddy":              {"report-buddies", "Scientific report"},
		"report/expert":             {"domain-experts", "Scientific report"},
		"report/journalist":         {"journalists", "Scientific report"},
	}
	for _, r := range story.Requirements {
		w, ok := want[r.Lane]
		if !ok {
			t.Errorf("unexpected lane %q", r.Lane)
			continue
		}
		if r.Role != w[0] || r.Target != w[1] {
			t.Errorf("lane %s = role %q target %q, want %q / %q", r.Lane, r.Role, r.Target, w[0], w[1])
		}
		if r.EntityKind != "deliverable" {
			t.Errorf("lane %s entity_kind = %q, want deliverable", r.Lane, r.EntityKind)
		}
		delete(want, r.Lane)
	}
	if len(want) != 0 {
		t.Errorf("lanes missing from the shipped flow: %v", want)
	}
}

func TestLoadApprovalFlowsDirOverridesByName(t *testing.T) {
	dir := writeFlow(t, "story.json", `{
		"name": "story", "rev": "9",
		"requirements": [{"lane": "only", "entity_kind": "document", "role": "editors"}]
	}`)
	flows, err := api.LoadApprovalFlows(dir)
	if err != nil {
		t.Fatal(err)
	}
	story := flowByName(t, flows, "story")
	if story.Rev != "9" || len(story.Requirements) != 1 {
		t.Errorf("story = rev %q with %d lanes, want the dir flow", story.Rev, len(story.Requirements))
	}
	// Replaced, not appended.
	n := 0
	for _, f := range flows {
		if f.Name == "story" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d flows named story, want 1", n)
	}
}

func TestLoadApprovalFlowsRefusesInvalidFiles(t *testing.T) {
	cases := map[string]string{
		"unknown entity kind": `{"name": "x", "rev": "1", "requirements":
			[{"lane": "a", "entity_kind": "milestone", "role": "r"}]}`,
		"pr requirement": `{"name": "x", "rev": "1", "requirements":
			[{"lane": "a", "entity_kind": "pr", "role": "r"}]}`,
		"duplicate lane": `{"name": "x", "rev": "1", "requirements":
			[{"lane": "a", "entity_kind": "task", "role": "r"},
			 {"lane": "a", "entity_kind": "task", "role": "r2"}]}`,
		"empty role": `{"name": "x", "rev": "1", "requirements":
			[{"lane": "a", "entity_kind": "task", "role": ""}]}`,
		"empty name": `{"name": "", "rev": "1", "requirements": []}`,
		"empty rev":  `{"name": "x", "rev": "", "requirements": []}`,
		"not json":   `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeFlow(t, "bad.json", body)
			_, err := api.LoadApprovalFlows(dir)
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), "bad.json") {
				t.Errorf("error does not name the file: %v", err)
			}
		})
	}
}

func TestLoadApprovalFlowsRefusesAnUnreadableFile(t *testing.T) {
	dir := writeFlow(t, "locked.json", `{"name": "x", "rev": "1", "requirements": []}`)
	if err := os.Chmod(filepath.Join(dir, "locked.json"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := api.LoadApprovalFlows(dir); err == nil {
		t.Fatal("want an error for an unreadable flow file, got none")
	} else if !strings.Contains(err.Error(), "locked.json") {
		t.Errorf("error does not name the file: %v", err)
	}
}

func TestLoadApprovalFlowsIgnoresNonJSONAndMissingDir(t *testing.T) {
	dir := writeFlow(t, "notes.txt", "not a flow")
	if _, err := api.LoadApprovalFlows(dir); err != nil {
		t.Errorf("a non-.json file must be ignored: %v", err)
	}
	if _, err := api.LoadApprovalFlows(filepath.Join(dir, "absent")); err == nil {
		t.Error("a configured but missing dir must fail the boot")
	}
}

// TestNewServerLoadsApprovalFlows pins the boot wiring: the flow set is read
// at startup, a bad configured directory fails the boot, and the system actor
// the rules credit their rows to exists before the first request.
func TestNewServerLoadsApprovalFlows(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)

	bad := writeFlow(t, "broken.json", `{"name": "x", "rev": "1", "requirements":
		[{"lane": "a", "entity_kind": "pr", "role": "r"}]}`)
	if _, _, err := api.NewServer(st, api.Config{ApprovalFlowsDir: bad}); err == nil {
		t.Fatal("NewServer booted with an invalid approval flow")
	} else if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("boot error does not name the file: %v", err)
	}

	if _, _, err := api.NewServer(st, api.Config{}); err != nil {
		t.Fatalf("NewServer with the shipped defaults: %v", err)
	}
	actor, err := st.GetActor(t.Context(), "worklode")
	if err != nil {
		t.Fatalf("GetActor(worklode): %v", err)
	}
	if actor == nil {
		t.Fatal("NewServer did not ensure the worklode service actor")
	}
}
