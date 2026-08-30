package api_test

// eventpayload_test.go pins WL-281: every task-scoped event payload names the
// task it describes under the JSON key "task" (025 §15.2), so GET
// /api/v1/events can attribute an event without a second lookup. One test per
// event type, each reading the payload back out of the store (storeEventsOfType,
// events_test.go) and decoding it as JSON, rather than trusting the wire
// response — the wire response never carries the raw event payload back out.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// decodePayload decodes one event's JSON payload into a map for field-by-field
// assertions.
func decodePayload(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("decode payload %s: %v", payload, err)
	}
	return m
}

// TestEventPayloadTaskCreated covers POST /api/v1/tasks: the minted id is not
// known when the payload is marshalled, so "task" is merged in afterward by
// store.AttributeEventToTask inside the same transaction (tasks.go). The
// pre-existing request fields must survive that merge.
func TestEventPayloadTaskCreated(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "First task", "priority": "high", "kind": "feature",
	})
	id := created["id"].(string)

	events := storeEventsOfType(t, st, "task.created", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != id {
		t.Fatalf(`payload["task"] = %v, want %q`, payload["task"], id)
	}
	if payload["project"] != "proj" || payload["title"] != "First task" {
		t.Fatalf("payload missing pre-existing fields: %v", payload)
	}
}

// TestEventPayloadTaskCreatedFromWeb is TestEventPayloadTaskCreated's
// counterpart for the "web" source (webform.go's recordFormTask), which also
// merges "task" in via AttributeEventToTask after the id is minted.
func TestEventPayloadTaskCreatedFromWeb(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	rr := doForm(t, h, "/projects/proj/tasks", url.Values{
		"title": {"Wire the intake form"}, "priority": {"high"}, "kind": {"feature"},
	}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}

	events := storeEventsOfType(t, st, "task.created", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != "WL-1" {
		t.Fatalf(`payload["task"] = %v, want "WL-1"`, payload["task"])
	}
	if payload["title"] != "Wire the intake form" {
		t.Fatalf("payload missing pre-existing fields: %v", payload)
	}
}

// TestEventPayloadTaskUpdated covers PATCH /api/v1/tasks/{id}: recordTaskEvent
// merges "task" into the EditTaskInput payload alongside the fields the patch
// set.
func TestEventPayloadTaskUpdated(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Orig", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"title": "New title"})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body %s", rr.Code, rr.Body.String())
	}

	events := storeEventsOfType(t, st, "task.updated", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != "WL-1" {
		t.Fatalf(`payload["task"] = %v, want "WL-1"`, payload["task"])
	}
	if payload["title"] != "New title" {
		t.Fatalf("payload missing the patched field: %v", payload)
	}
}

// TestEventPayloadTaskSkillsSet covers PUT /api/v1/tasks/{id}/skills.
func TestEventPayloadTaskSkillsSet(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "PUT", "/api/v1/tasks/WL-1/skills", token,
		map[string]any{"skills": []string{"tdd", "debugging"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("set skills status = %d, body %s", rr.Code, rr.Body.String())
	}

	events := storeEventsOfType(t, st, "task.skills_set", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != "WL-1" {
		t.Fatalf(`payload["task"] = %v, want "WL-1"`, payload["task"])
	}
	skills, ok := payload["skills"].([]any)
	if !ok || len(skills) != 2 || skills[0] != "tdd" || skills[1] != "debugging" {
		t.Fatalf("payload skills = %v, want [tdd debugging]", payload["skills"])
	}
}

// TestEventPayloadTaskDecomposed covers POST /api/v1/tasks/{id}/decompose:
// "task" names the parent, not any of the minted children.
func TestEventPayloadTaskDecomposed(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Parent", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/decompose", token,
		map[string]any{"into": []string{"Child A", "Child B"}})
	if rr.Code != http.StatusCreated {
		t.Fatalf("decompose status = %d, body %s", rr.Code, rr.Body.String())
	}

	events := storeEventsOfType(t, st, "task.decomposed", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != "WL-1" {
		t.Fatalf(`payload["task"] = %v, want "WL-1" (the parent)`, payload["task"])
	}
	into, ok := payload["into"].([]any)
	if !ok || len(into) != 2 || into[0] != "Child A" || into[1] != "Child B" {
		t.Fatalf("payload into = %v, want [Child A Child B]", payload["into"])
	}
}

// TestEventPayloadIssuePromoted covers POST /api/v1/inbox/promote: like
// task.created, the minted id is attributed after the fact
// (store.AttributeEventToTask in admin.go).
func TestEventPayloadIssuePromoted(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "bug",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("promote status = %d, body %s", rr.Code, rr.Body.String())
	}
	id := decodeMap(t, rr)["id"].(string)

	events := storeEventsOfType(t, st, "issue.promoted", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != id {
		t.Fatalf(`payload["task"] = %v, want %q`, payload["task"], id)
	}
	if payload["repo"] != "acme/widgets" {
		t.Fatalf("payload missing pre-existing fields: %v", payload)
	}
}

// TestEventPayloadIssueLinked covers POST /api/v1/inbox/link: "task" is
// merged in via recordTaskEvent alongside the pre-existing "task_id" field
// LinkInput already carries (admin.go).
func TestEventPayloadIssueLinked(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")
	taskID := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "already tracked", "priority": "low", "kind": "bug",
	})["id"].(string)

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/link", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "task_id": taskID,
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("link status = %d, body %s", rr.Code, rr.Body.String())
	}

	events := storeEventsOfType(t, st, "issue.linked", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != taskID {
		t.Fatalf(`payload["task"] = %v, want %q`, payload["task"], taskID)
	}
	if payload["task_id"] != taskID {
		t.Fatalf(`payload["task_id"] = %v, want %q (the pre-existing field)`, payload["task_id"], taskID)
	}
}

// --- regression coverage: emitters that already named their task -----------
//
// These four built their payload literally with "task": id from the start,
// ahead of WL-281. They are pinned here so a future refactor of
// recordEvent/recordTaskEvent cannot silently drop the key.

// TestEventPayloadTaskDeleted covers DELETE /api/v1/tasks/{id}.
func TestEventPayloadTaskDeleted(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Doomed", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "DELETE", "/api/v1/tasks/WL-1", token, map[string]any{"justification": "cleanup"})
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body %s", rr.Code, rr.Body.String())
	}

	events := storeEventsOfType(t, st, "task.deleted", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != "WL-1" {
		t.Fatalf(`payload["task"] = %v, want "WL-1"`, payload["task"])
	}
}

// TestEventPayloadTaskDone covers POST /api/v1/tasks/{id}/done.
func TestEventPayloadTaskDone(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Ship it", "priority": "high", "kind": "chore",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/done", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("done status = %d, body %s", rr.Code, rr.Body.String())
	}

	events := storeEventsOfType(t, st, "task.done", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != "WL-1" {
		t.Fatalf(`payload["task"] = %v, want "WL-1"`, payload["task"])
	}
}

// TestEventPayloadTaskAbandoned covers POST /api/v1/tasks/{id}/abandon.
func TestEventPayloadTaskAbandoned(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Not happening", "priority": "low", "kind": "chore",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/abandon", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("abandon status = %d, body %s", rr.Code, rr.Body.String())
	}

	events := storeEventsOfType(t, st, "task.abandoned", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != "WL-1" {
		t.Fatalf(`payload["task"] = %v, want "WL-1"`, payload["task"])
	}
}

// TestEventPayloadTaskAssigned covers POST /api/v1/tasks/{id}/assign.
func TestEventPayloadTaskAssigned(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Needs an owner", "priority": "medium", "kind": "feature",
	})
	secondActor(t, st, "bob")
	store.SeedCrewForTests(t, st, "proj", "bob")

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, map[string]any{"assignee": "bob"})
	if rr.Code != http.StatusOK {
		t.Fatalf("assign status = %d, body %s", rr.Code, rr.Body.String())
	}

	events := storeEventsOfType(t, st, "task.assigned", 1)
	payload := decodePayload(t, events[len(events)-1].Payload)
	if payload["task"] != "WL-1" {
		t.Fatalf(`payload["task"] = %v, want "WL-1"`, payload["task"])
	}
	if payload["assignee"] != "bob" {
		t.Fatalf("payload missing pre-existing fields: %v", payload)
	}
}
