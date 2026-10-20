package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestApprovalDecide covers `lode approval decide` (WL-REQ-1608): a verdict
// resolves the row and renders it, and the store's own-work refusal reaches
// the caller as an error.
func TestApprovalDecide(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)

	var id int64
	if _, _, err := st.RecordEvent(context.Background(), "cli", "seed-decide", "test.seed", []byte(`{}`),
		func(tx *sql.Tx, _ int64) error {
			if _, err := store.InsertAwaitingApproval(tx, st.Now(), "deliverable", "DEL-1", "r1", "", nil, nil, nil); err != nil {
				return err
			}
			a, err := store.ApprovalByKey(tx, "deliverable", "DEL-1", "r1", "")
			if a != nil {
				id = a.ID
			}
			return err
		}); err != nil {
		t.Fatalf("seed approval: %v", err)
	}
	out, err := runLode(t, "approval", "decide", strconv.FormatInt(id, 10),
		"--verdict", "request_changes", "--note", "needs tests")
	if err != nil {
		t.Fatalf("approval decide: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "changes_requested") || !strings.Contains(out, "alice") {
		t.Errorf("approval decide output = %q, want changes_requested by alice", out)
	}

	task := addTask(t, "--project", "proj", "--title", "My own work")
	out, err = runLode(t, "approval", "add", "task", task.ID, "--revision", "abc", "--json")
	if err != nil {
		t.Fatalf("approval add: %v\noutput: %s", err, out)
	}
	var a model.Approval
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if _, err := runLode(t, "approval", "decide", strconv.FormatInt(a.ID, 10), "--verdict", "approve"); err == nil ||
		!strings.Contains(err.Error(), "you authored this change") {
		t.Errorf("deciding own work: err = %v, want the authorship refusal", err)
	}
}

// TestApprovalAddRequirement covers `lode approval add` end to end (WL-SPEC-75
// §13.6): the ad-hoc requirement round-trips to the server and renders, a
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
