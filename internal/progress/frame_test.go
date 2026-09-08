package progress

import (
	"encoding/json"
	"testing"
)

// payload marshals the map an event's call site passes, so a row below reads
// as the payload the backbone actually records.
func payload(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	if fields == nil {
		return nil
	}
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// TestResolve is one row per event type the backbone records today, found by
// grepping the recordEvent/RecordEvent call sites in internal/api,
// internal/store and internal/hooks. The payload column is the map that call
// site passes; the want column is what a Progress frame gets from it.
func TestResolve(t *testing.T) {
	t.Parallel()

	task := func(id string) map[string]any { return map[string]any{"task": id} }
	doc := func(id int64) map[string]any {
		return map[string]any{"doc": id, "actor": "stig", "request": nil}
	}

	cases := []struct {
		typ     string
		fields  map[string]any
		want    Touch
		whereIt string
	}{
		// --- task family -------------------------------------------------
		{"task.created", map[string]any{"project": "WL", "title": "t", "task": "WL-1"},
			Touch{Task: "WL-1", Kind: "task"}, "api/tasks.go, webform.go (task merged by AttributeEventToTask)"},
		{"task.created", map[string]any{"doc": "wlid:doc/spec-worklode-066", "kind": "design", "task": "WL-2"},
			Touch{Task: "WL-2", DocIRI: "wlid:doc/spec-worklode-066", Kind: "task"},
			"api/progress.go mintPlanningTask; doc is an IRI, not an id"},
		{"task.updated", map[string]any{"rule": "plan_accepted", "task": "WL-3", "absorbed_event": 9},
			Touch{Task: "WL-3", Kind: "task"}, "api/docwatch.go suppression note"},
		{"task.done", task("WL-4"), Touch{Task: "WL-4", Kind: "task"}, "api/lifecycle.go finishTask"},
		{"task.deployed_dev", task("WL-5"), Touch{Task: "WL-5", Kind: "task"}, "api/lifecycle.go setStateEventType"},
		{"task.deployed_prod", task("WL-6"), Touch{Task: "WL-6", Kind: "task"}, "api/lifecycle.go setStateEventType"},
		{"task.released", task("WL-7"), Touch{Task: "WL-7", Kind: "task"}, "api/lifecycle.go setStateEventType"},
		{"task.abandoned", task("WL-8"), Touch{Task: "WL-8", Kind: "task"}, "api/lifecycle.go abandonTask"},
		{"task.reopened", task("WL-9"), Touch{Task: "WL-9", Kind: "task"}, "api/lifecycle.go reopenTask"},
		{"task.assigned", map[string]any{"task": "WL-10", "assignee": "stig"},
			Touch{Task: "WL-10", Kind: "task"}, "api/assign.go"},
		{"task.unassigned", task("WL-11"), Touch{Task: "WL-11", Kind: "task"}, "api/assign.go"},
		{"task.started", map[string]any{"task": "WL-12", "actor": "stig"},
			Touch{Task: "WL-12", Kind: "task"}, "api/assign.go"},
		{"task.stopped", map[string]any{"task": "WL-13", "actor": "stig"},
			Touch{Task: "WL-13", Kind: "task"}, "api/assign.go"},
		{"task.deleted", map[string]any{"task": "WL-14", "actor": "stig", "justification": "dupe"},
			Touch{Task: "WL-14", Kind: "task"}, "api/softdelete.go"},
		{"task.undeleted", map[string]any{"task": "WL-15", "actor": "stig"},
			Touch{Task: "WL-15", Kind: "task"}, "api/deleted.go"},
		{"task.transition", map[string]any{"task": "WL-16", "from": "draft", "to": "ready"},
			Touch{Task: "WL-16", Kind: "task"}, "api/progress.go rally confirm, via recordTaskEvent"},
		{"task.decomposed", map[string]any{"task": "WL-17", "children": []string{"WL-18"}},
			Touch{Task: "WL-17", Kind: "task"}, "api/hierarchy.go, via recordTaskEvent"},
		{"task.checklist_set", map[string]any{"task": "WL-19"},
			Touch{Task: "WL-19", Kind: "task"}, "api/checklist.go, via recordTaskEvent"},
		{"task.skills_set", map[string]any{"task": "WL-20"},
			Touch{Task: "WL-20", Kind: "task"}, "api/tasks.go, via recordTaskEvent"},
		{"task.instructed", map[string]any{"task": "WL-21", "actor": "stig"},
			Touch{Task: "WL-21", Kind: "task"}, "store/instructions.go"},
		{"task.decided", map[string]any{"task": "WL-22", "actor": "stig", "key": "accept"},
			Touch{Task: "WL-22", Kind: "task"}, "store/decisions.go"},
		// The two edge events name their endpoints, never a "task": the kind
		// alone is what the page gets, which still refreshes the summary.
		{"task.edge_added", map[string]any{"from": "WL-23", "to": "WL-24", "type": "blocks"},
			Touch{Kind: "task"}, "api/tasks.go addEdge"},
		{"task.edge_removed", map[string]any{"from": "WL-23", "to": "WL-24", "type": "blocks"},
			Touch{Kind: "task"}, "api/tasks.go removeEdge"},

		// --- lease, session, decision, issue -----------------------------
		{"lease.claimed", map[string]any{"task": "WL-25", "actor": "stig", "worktree": "/w"},
			Touch{Task: "WL-25", Kind: "task"}, "store/leases.go"},
		{"lease.renewed", map[string]any{"task": "WL-26", "actor": "stig"},
			Touch{Task: "WL-26", Kind: "task"}, "store/leases.go"},
		{"lease.released", map[string]any{"task": "WL-27", "actor": "stig"},
			Touch{Task: "WL-27", Kind: "task"}, "store/leases.go"},
		{"lease.rebound", map[string]any{"task": "WL-28", "actor": "stig", "worktree": "/w"},
			Touch{Task: "WL-28", Kind: "task"}, "store/leases.go"},
		{"lease.expired", map[string]any{"task": "WL-29", "lease": 7},
			Touch{Task: "WL-29", Kind: "task"}, "store/leases.go sweep"},
		{"agent_session.started", map[string]any{"task": "WL-30", "actor": "stig", "agent": "claude", "session": "s1"},
			Touch{Task: "WL-30", Kind: "task"}, "store/agent_sessions.go"},
		{"agent_session.ended", map[string]any{"task": "WL-31", "actor": "stig", "agent": "claude", "session": "s1"},
			Touch{Task: "WL-31", Kind: "task"}, "store/agent_sessions.go"},
		{"decision.posed", map[string]any{"task": "WL-32", "actor": "stig", "key": "k"},
			Touch{Task: "WL-32", Kind: "task"}, "store/decisions.go"},
		{"decision.edited", map[string]any{"task": "WL-33", "actor": "stig", "key": "k"},
			Touch{Task: "WL-33", Kind: "task"}, "store/decisions.go"},
		{"issue.promoted", map[string]any{"issue": 4, "task": "WL-34"},
			Touch{Task: "WL-34", Kind: "task"}, "api/admin.go (task merged by AttributeEventToTask)"},
		{"issue.linked", map[string]any{"task": "WL-35", "task_id": "WL-35"},
			Touch{Task: "WL-35", Kind: "task"}, "api/admin.go, via recordTaskEvent"},
		{"issue.dismissed", map[string]any{"issue": 4},
			Touch{Kind: "task"}, "api/admin.go: names an inbox issue, no task"},

		// --- documents ----------------------------------------------------
		// doc.created goes in with id 0 — the row does not exist until apply
		// runs — and store.MergeEventPayload writes the real id back inside
		// the same transaction, so what the log holds is this (WL-768).
		{"doc.created", doc(10), Touch{Doc: 10, Kind: "doc"},
			"api/docs.go createDoc (doc merged by MergeEventPayload)"},
		{"doc.updated", doc(11), Touch{Doc: 11, Kind: "doc"}, "api/docs.go"},
		{"doc.patched", doc(12), Touch{Doc: 12, Kind: "doc"}, "api/docs.go, watcher.TypeDocPatched"},
		{"doc.revised", doc(13), Touch{Doc: 13, Kind: "doc"}, "api/docs.go"},
		{"doc.note_added", doc(14), Touch{Doc: 14, Kind: "doc"}, "api/docs.go"},
		{"doc.edges_rebuilt", doc(15), Touch{Doc: 15, Kind: "doc"}, "api/docs.go"},
		{"doc.owner_changed", doc(16), Touch{Doc: 16, Kind: "doc"}, "api/docs.go"},
		{"doc.revision_updated", doc(17), Touch{Doc: 17, Kind: "doc"}, "api/docs.go"},
		{"doc.revision_discarded", doc(18), Touch{Doc: 18, Kind: "doc"}, "api/docs.go"},
		{"doc.revision_accepted", doc(19), Touch{Doc: 19, Kind: "doc"}, "api/docs.go"},
		{"doc.approval_requested", doc(20), Touch{Doc: 20, Kind: "doc"}, "api/approvals.go"},
		{"doc.reviewers_changed", doc(21), Touch{Doc: 21, Kind: "doc"}, "api/approvals.go"},
		{"doc.deleted", doc(22), Touch{Doc: 22, Kind: "doc"}, "api/softdelete.go"},
		// The two typed events name their subject by IRI and nothing else
		// (025 §15.3), so the touch carries the IRI for the reader to resolve.
		{"wl:DocumentSubmitted", map[string]any{"wl:subject": "wlid:doc/spec-worklode-066"},
			Touch{DocIRI: "wlid:doc/spec-worklode-066", Kind: "doc"},
			"eventbus.Emit from api/docs.go submitDoc"},
		{"wl:DocumentAccepted", map[string]any{"wl:subject": "wlid:doc/spec-worklode-066"},
			Touch{DocIRI: "wlid:doc/spec-worklode-066", Kind: "doc"},
			"eventbus.Emit from api/docs.go acceptRevision"},

		// --- rally ---------------------------------------------------------
		{"rally.assembled", map[string]any{"doc": "wlid:doc/spec-worklode-066", "task": "WL-40"},
			Touch{Task: "WL-40", DocIRI: "wlid:doc/spec-worklode-066", Kind: "rally"},
			"api/progress.go (task merged by AttributeEventToTask)"},

		// --- webhook deliveries --------------------------------------------
		// The payload is GitHub's own body; "task" is merged in by the
		// applier once it has resolved the correlation.
		{"pull_request.opened", map[string]any{"pull_request": map[string]any{"number": 5}, "task": "WL-41"},
			Touch{Task: "WL-41", Kind: "pr"}, "hooks/github.go applyPullRequest"},
		{"pull_request.closed", map[string]any{"pull_request": map[string]any{"number": 5}, "task": "WL-42"},
			Touch{Task: "WL-42", Kind: "pr"}, "hooks/github.go applyPullRequest"},
		{"merge_group.checks_requested", map[string]any{"merge_group": map[string]any{"head_ref": "refs/heads/gh-readonly-queue/main/pr-7-abc"}, "task": "WL-44"},
			Touch{Task: "WL-44", Kind: "pr"}, "hooks/github.go applyMergeGroup"},
		{"workflow_run.completed", map[string]any{"workflow_run": map[string]any{"head_sha": "abc"}, "task": "WL-43"},
			Touch{Task: "WL-43", Kind: "ci"}, "hooks/github.go applyWorkflowRun"},
		// The three deploy events transition a set of tasks, so their payload
		// names none; the kind is the whole signal.
		{"push", map[string]any{"ref": "refs/heads/main"},
			Touch{Kind: "deploy"}, "hooks/push.go applyPush"},
		{"deployment_status.created", map[string]any{"deployment": map[string]any{"environment": "prod"}},
			Touch{Kind: "deploy"}, "hooks/deployment.go applyDeploymentStatus"},
		{"flux.Kustomization.ReconciliationSucceeded", map[string]any{"involvedObject": map[string]any{"kind": "Kustomization"}},
			Touch{Kind: "deploy"}, "hooks/flux.go"},

		// --- everything else resolves to nothing ---------------------------
		{"test.event", map[string]any{"a": 1}, Touch{}, "not a type the page shows"},
		{"crew.member_added", map[string]any{"project": "WL", "actor": "stig"}, Touch{}, "api/crew.go"},
		{"crew.member_removed", map[string]any{"project": "WL", "actor": "stig"}, Touch{}, "api/crew.go"},
		{"project.focus_set", map[string]any{"project": "WL", "focus": "f"}, Touch{}, "store/projects.go"},
		{"milestone.created", map[string]any{"project": "WL", "id": "M-1"}, Touch{}, "api/milestones.go: id is the milestone"},
		{"approval.required", map[string]any{"entity_kind": "doc", "entity_id": "1"}, Touch{}, "api/approvals.go"},
		{"approval.decided", map[string]any{"approval_id": 1, "decision": "approve"}, Touch{}, "api/webform.go"},
		{"approval_flow.applied", map[string]any{"project": "WL", "flow": "f"}, Touch{}, "api/approvalflows.go"},
		{"merge.local", map[string]any{"repo": "r", "sha": "s", "tasks": []string{"WL-1"}}, Touch{}, "api/merges.go: a set, not one task"},
		{"inbox.imported", map[string]any{"repo": "r"}, Touch{}, "api/inbox_import.go"},
		{"secrets_materialized", map[string]any{"project": "WL"}, Touch{}, "api/secrets.go"},
		{"deliverable.created", map[string]any{"project": "WL"}, Touch{}, "api/deliverables.go"},
		{"deliverable.updated", map[string]any{"project": "WL"}, Touch{}, "api/deliverables.go"},
		{"runtime.crash_loop", map[string]any{"cluster": "c"}, Touch{}, "api/runtime.go"},
		{"catalog.published", map[string]any{"name": "n"}, Touch{}, "hooks/catalog.go"},
		{"issues.opened", map[string]any{"issue": map[string]any{"number": 1}}, Touch{}, "hooks/github.go applyIssue"},
		{"pull_request_review.submitted", map[string]any{"review": map[string]any{"state": "approved"}}, Touch{},
			"hooks/github.go applyReview: resolves no task"},
		{"release.published", map[string]any{"release": map[string]any{"tag_name": "v1"}}, Touch{}, "hooks/github.go applyRelease"},
		{"registry_package.published", map[string]any{"registry_package": map[string]any{}}, Touch{}, "hooks/github.go"},
	}

	for _, c := range cases {
		t.Run(c.typ+"/"+c.whereIt, func(t *testing.T) {
			t.Parallel()
			if got := Resolve(c.typ, payload(t, c.fields)); got != c.want {
				t.Errorf("Resolve(%q, %s) = %+v, want %+v", c.typ, payload(t, c.fields), got, c.want)
			}
		})
	}
}

// TestResolveMalformedPayload pins the two degenerate payloads the log can
// hold: an event recorded with no payload at all, and one whose payload is
// not a JSON object. Both keep the kind and name no id.
func TestResolveMalformedPayload(t *testing.T) {
	t.Parallel()
	for _, p := range [][]byte{nil, []byte("null"), []byte(`"a string"`), []byte(`[1,2]`), []byte(`{`)} {
		if got := Resolve("task.done", p); got != (Touch{Kind: "task"}) {
			t.Errorf("Resolve(task.done, %q) = %+v, want kind task only", p, got)
		}
		if got := Resolve("test.event", p); got != (Touch{}) {
			t.Errorf("Resolve(test.event, %q) = %+v, want zero", p, got)
		}
	}
}
