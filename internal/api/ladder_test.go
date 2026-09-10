package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ladderFixture creates a project, one spec document, and a freestanding task
// — the ladder's non-escalating rungs (025 §15.5) do not need a plan or a
// lease, unlike `lode task escalate`.
func ladderFixture(t *testing.T, h http.Handler, token string) (model.Doc, map[string]any) {
	t.Helper()
	doc := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Slug: "ladder-spec", Body: escalateSpecBody,
	})
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Ladder task", "kind": "feature", "priority": "medium",
	})
	return doc, task
}

// TestTaskGapRecordsEvent: POST /api/v1/tasks/{id}/gap lands task.gap_found
// with the same payload shape as escalate's minus "to" (025 §15.5).
func TestTaskGapRecordsEvent(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	doc, task := ladderFixture(t, h, token)
	id, _ := task["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/gap", token,
		model.GapTaskInput{Doc: doc.Slug, Anchor: "sec-1", Reason: "the spec skips the empty case"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var res model.LadderEventResult
	decodeInto(t, rr, &res)
	if !res.Recorded {
		t.Fatalf("result = %+v, want Recorded", res)
	}

	events := storeEventsOfType(t, st, "task.gap_found", 1)
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["task"] != id || payload["doc"] != doc.Slug || payload["anchor"] != "sec-1" ||
		payload["reason"] != "the spec skips the empty case" {
		t.Errorf("payload = %+v", payload)
	}
	if _, ok := payload["to"]; ok {
		t.Errorf("payload = %+v, must not carry \"to\" — a gap has no escalation target", payload)
	}
}

// TestTaskGapRefusesBlankReason: the reason is what the fixer reads, so a
// blank one is 422, not a logged gap.
func TestTaskGapRefusesBlankReason(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	doc, task := ladderFixture(t, h, token)
	id, _ := task["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/gap", token,
		model.GapTaskInput{Doc: doc.Slug, Reason: "  "})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
}

// TestTaskGapRefusesUnknownDoc: an unresolvable doc ref is a 404, not a
// server fault or a silently orphaned event.
func TestTaskGapRefusesUnknownDoc(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	_, task := ladderFixture(t, h, token)
	id, _ := task["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/gap", token,
		model.GapTaskInput{Doc: "no-such-doc", Reason: "why"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
}

// TestTaskFixStartedRecordsEvent: POST /api/v1/tasks/{id}/fix with
// phase=started lands fix.started with the tier and target document.
func TestTaskFixStartedRecordsEvent(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	doc, task := ladderFixture(t, h, token)
	id, _ := task["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/fix", token,
		model.FixTaskInput{Phase: "started", Tier: "spec", Doc: doc.Slug})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var res model.LadderEventResult
	decodeInto(t, rr, &res)
	if !res.Recorded {
		t.Fatalf("result = %+v, want Recorded", res)
	}

	events := storeEventsOfType(t, st, "fix.started", 1)
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["task"] != id || payload["tier"] != "spec" || payload["doc"] != doc.Slug {
		t.Errorf("payload = %+v", payload)
	}
}

// TestTaskFixStartedReplayDoesNotDuplicate: the same attempt ordinal (the
// default, 1, when the caller names none) makes a retried "started" call a
// no-op at the log — the external id discipline this route promises.
func TestTaskFixStartedReplayDoesNotDuplicate(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	doc, task := ladderFixture(t, h, token)
	id, _ := task["id"].(string)

	in := model.FixTaskInput{Phase: "started", Tier: "spec", Doc: doc.Slug}
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/fix", token, in)
	if rr.Code != http.StatusOK {
		t.Fatalf("first call: status = %d, body %s", rr.Code, rr.Body.String())
	}
	var first model.LadderEventResult
	decodeInto(t, rr, &first)
	if !first.Recorded {
		t.Fatalf("first call: result = %+v, want Recorded", first)
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/fix", token, in)
	if rr.Code != http.StatusOK {
		t.Fatalf("replay: status = %d, body %s", rr.Code, rr.Body.String())
	}
	var second model.LadderEventResult
	decodeInto(t, rr, &second)
	if second.Recorded {
		t.Fatalf("replay: result = %+v, want !Recorded", second)
	}

	events := storeEventsOfType(t, st, "fix.started", 1)
	if len(events) != 1 {
		t.Fatalf("fix.started events = %d, want exactly 1", len(events))
	}
}

// TestTaskFixFinishedRecordsEvent: phase=finished lands fix.finished with
// the outcome.
func TestTaskFixFinishedRecordsEvent(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	_, task := ladderFixture(t, h, token)
	id, _ := task["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/fix", token,
		model.FixTaskInput{Phase: "finished", Outcome: "resolved"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var res model.LadderEventResult
	decodeInto(t, rr, &res)
	if !res.Recorded {
		t.Fatalf("result = %+v, want Recorded", res)
	}

	events := storeEventsOfType(t, st, "fix.finished", 1)
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["task"] != id || payload["outcome"] != "resolved" {
		t.Errorf("payload = %+v", payload)
	}
}

// TestTaskFixRefusesBadRequest covers the closed label sets the funnel
// depends on: an unknown phase, a "started" missing its tier or document,
// and a "finished" missing (or misspelling) its outcome are all 422.
func TestTaskFixRefusesBadRequest(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	doc, task := ladderFixture(t, h, token)
	id, _ := task["id"].(string)

	for _, tc := range []struct {
		name string
		in   model.FixTaskInput
	}{
		{"unknown phase", model.FixTaskInput{Phase: "paused"}},
		{"started missing tier", model.FixTaskInput{Phase: "started", Doc: doc.Slug}},
		{"started bad tier", model.FixTaskInput{Phase: "started", Tier: "vibes", Doc: doc.Slug}},
		{"started missing doc", model.FixTaskInput{Phase: "started", Tier: "spec"}},
		{"finished missing outcome", model.FixTaskInput{Phase: "finished"}},
		{"finished bad outcome", model.FixTaskInput{Phase: "finished", Outcome: "vibes"}},
	} {
		rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/fix", token, tc.in)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, body %s", tc.name, rr.Code, rr.Body.String())
		}
	}
}
