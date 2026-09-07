package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestApprovalHasNoDecisionCommand is the CLI half of 029 §7.3: approving is
// a web UI act because an OIDC session's group claims are fresh and a 30-day
// CLI token's are not. `lode approval` therefore offers add, request and list
// and nothing else, and the absence is asserted so a later "for convenience"
// addition trips a test rather than a spec review.
func TestApprovalHasNoDecisionCommand(t *testing.T) {
	want := map[string]bool{"add": true, "request": true, "list": true}
	for _, sub := range newApprovalCmd().Commands() {
		if !want[sub.Name()] {
			t.Errorf("lode approval %s exists; 029 §7.3 keeps every decision on the web session",
				sub.Name())
		}
		delete(want, sub.Name())
	}
	for name := range want {
		t.Errorf("lode approval %s is missing", name)
	}
}

// TestApprovalAddRequirement covers `lode approval add` end to end (029
// §7.2): the ad-hoc requirement round-trips to the server and renders, a
// document target is named by any reference rather than by its "doc:<id>"
// entity id, and naming both a role and an actor is refused.
func TestApprovalAddRequirement(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	task := addTask(t, "--project", "proj", "--title", "Needs a second pair of eyes")
	out, err := runLode(t, "approval", "add", "task", task.ID,
		"--role", "science-leads", "--lane", "science", "--revision", "abc123")
	if err != nil {
		t.Fatalf("approval add: %v\noutput: %s", err, out)
	}
	for _, want := range []string{task.ID, "science-leads", "awaiting", "abc123"} {
		if !strings.Contains(out, want) {
			t.Errorf("approval add output = %q, want it to name %q", out, want)
		}
	}

	// A document is named by reference; the CLI resolves it to the
	// "doc:<id>" entity id the approvals table stores.
	specFile := writeDocFile(t, docTestBody)
	if _, err := runLode(t, "doc", "add", "--project", "proj", "--kind", "spec",
		"--slug", "adhoc-spec", "--file", specFile); err != nil {
		t.Fatalf("doc add: %v", err)
	}
	out, err = runLode(t, "approval", "add", "doc", "adhoc-spec", "--actor", "alice", "--json")
	if err != nil {
		t.Fatalf("approval add doc: %v\noutput: %s", err, out)
	}
	var a model.Approval
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if a.EntityKind != "doc" || !strings.HasPrefix(a.EntityID, "doc:") {
		t.Errorf("approval = %+v, want a doc: entity id", a)
	}
	if a.SubjectRevision == "" {
		t.Error("a document requirement must bind to the document's current version")
	}

	// Both a role and an actor is a refusal, not a silent preference.
	if _, err := runLode(t, "approval", "add", "task", task.ID,
		"--role", "science-leads", "--actor", "alice"); err == nil {
		t.Error("approval add --role --actor: want an error, got nil")
	}
}
