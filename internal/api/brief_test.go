package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
)

func TestTaskBrief(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Fix the: Thing!!", "priority": "high", "kind": "bug",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Blocker", "priority": "high", "kind": "feature",
	})

	// Claim WL-1, then make WL-2 an open blocker of it.
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-2/edges", token, map[string]any{"to": "WL-1", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)

	task, ok := got["task"].(map[string]any)
	if !ok || task["id"] != "WL-1" {
		t.Fatalf("task = %v, want id WL-1", got["task"])
	}
	if got["branch"] != "lode/WL-1-fix-the-thing" {
		t.Fatalf("branch = %v, want lode/WL-1-fix-the-thing", got["branch"])
	}
	if _, ok := got["body"]; !ok {
		t.Fatalf("body key missing: %v", got)
	}

	blockers, ok := got["open_blockers"].([]any)
	if !ok || len(blockers) != 1 {
		t.Fatalf("open_blockers = %v, want one entry", got["open_blockers"])
	}
	blk := blockers[0].(map[string]any)
	if blk["id"] != "WL-2" || blk["state"] != "ready" || blk["title"] != "Blocker" {
		t.Fatalf("open blocker = %v, want id WL-2 state ready title Blocker", blk)
	}

	lease, ok := got["lease"].(map[string]any)
	if !ok || lease["worktree"] != "host:/wt-1" {
		t.Fatalf("lease = %v, want worktree host:/wt-1", got["lease"])
	}

	// Reserved fields are present and null in v1.
	for _, k := range []string{"governing_design", "affected_components", "definition_of_done"} {
		v, present := got[k]
		if !present || v != nil {
			t.Fatalf("%s = %v (present=%v), want JSON null", k, v, present)
		}
	}
}

func TestTaskBriefNoLease(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Unclaimed", "priority": "low", "kind": "chore",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-1/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["lease"] != nil {
		t.Fatalf("lease = %v, want null", got["lease"])
	}
	if blockers, ok := got["open_blockers"].([]any); !ok || len(blockers) != 0 {
		t.Fatalf("open_blockers = %v, want empty array", got["open_blockers"])
	}
}

func TestTaskBriefParent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Delivery lifecycle")
	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": epic,
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+child["id"].(string)+"/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	parent, ok := got["parent"].(map[string]any)
	if !ok {
		t.Fatalf("parent = %v, want an object", got["parent"])
	}
	if parent["id"] != epic || parent["title"] != "Delivery lifecycle" || parent["state"] != "ready" {
		t.Fatalf("parent = %v, want id %s title Delivery lifecycle state ready", parent, epic)
	}
}

func TestTaskBriefNoParent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Root", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-1/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if v, present := got["parent"]; !present || v != nil {
		t.Fatalf("parent = %v (present=%v), want JSON null", v, present)
	}
}

func TestTaskBriefNotFound(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-99/brief", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("brief unknown task status = %d, want 404", rr.Code)
	}
}

func TestRebindWorktree(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Work", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	// Holder rebinds: 200 with the updated lease.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/lease/worktree", token, map[string]any{"worktree": "host:/wt-moved"})
	if rr.Code != http.StatusOK {
		t.Fatalf("rebind status = %d, body %s", rr.Code, rr.Body.String())
	}
	lease := decodeMap(t, rr)
	if lease["worktree"] != "host:/wt-moved" || lease["task_id"] != "WL-1" {
		t.Fatalf("rebound lease = %v, want worktree host:/wt-moved task_id WL-1", lease)
	}

	// Empty worktree: 400.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/lease/worktree", token, map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty worktree status = %d, want 400", rr.Code)
	}

	// Non-holder: 404 (probe-resistant).
	bobToken := secondActor(t, st, "bob")
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/lease/worktree", bobToken, map[string]any{"worktree": "host:/wt-bob"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("non-holder rebind status = %d, want 404", rr.Code)
	}
}

// --- brief skills section ---------------------------------------------

func TestTaskBriefSkillsPinned(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "high", "kind": "feature",
		"skills": []string{"tdd"},
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+task["id"].(string)+"/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	skills, ok := got["skills"].(map[string]any)
	if !ok {
		t.Fatalf("skills section missing: %v", got)
	}
	if skills["provider"] != "none" {
		t.Fatalf("provider = %v, want none", skills["provider"])
	}
	pinned, _ := skills["pinned"].([]any)
	if len(pinned) != 1 {
		t.Fatalf("pinned = %v, want one entry", skills["pinned"])
	}
	p0, _ := pinned[0].(map[string]any)
	if p0["name"] != "tdd" || p0["content"] == "" || p0["content"] == nil {
		t.Fatalf("pinned[0] = %v, want tdd with content", p0)
	}
	matches, _ := skills["matches"].([]any)
	if len(matches) != 0 {
		t.Fatalf("matches = %v, want none (no embedder configured)", skills["matches"])
	}
}

// TestTaskBriefSkillsEmptyWhenNoPinsNoEmbedder guards the wire contract Task
// 13 renders against: the skills section is always present, with empty
// arrays (never null) even when there is nothing to show.
func TestTaskBriefSkillsEmptyWhenNoPinsNoEmbedder(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "No pins", "priority": "low", "kind": "chore",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+task["id"].(string)+"/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	skills, ok := got["skills"].(map[string]any)
	if !ok {
		t.Fatalf("skills section missing entirely: %v", got)
	}
	if skills["provider"] != "none" {
		t.Fatalf("provider = %v, want none", skills["provider"])
	}
	if pinned, ok := skills["pinned"].([]any); !ok || len(pinned) != 0 {
		t.Fatalf("pinned = %v, want empty array not null", skills["pinned"])
	}
	if matches, ok := skills["matches"].([]any); !ok || len(matches) != 0 {
		t.Fatalf("matches = %v, want empty array not null", skills["matches"])
	}
	if warnings, ok := skills["warnings"].([]any); !ok || len(warnings) != 0 {
		t.Fatalf("warnings = %v, want empty array not null", skills["warnings"])
	}
}

// TestTaskBriefDeletedPinStillResolves guards that a brief never breaks
// because a pinned skill was withdrawn upstream: content still comes back,
// alongside a warning.
func TestTaskBriefDeletedPinStillResolves(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")
	if _, err := st.SoftDeleteSkillsExcept(context.Background(), "acme/skills", nil); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "high", "kind": "feature",
		"skills": []string{"tdd"},
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+task["id"].(string)+"/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	skills := got["skills"].(map[string]any)
	pinned, _ := skills["pinned"].([]any)
	if len(pinned) != 1 {
		t.Fatalf("pinned = %v, want tdd resolved despite deletion", skills["pinned"])
	}
	p0 := pinned[0].(map[string]any)
	if p0["content"] == "" || p0["content"] == nil {
		t.Fatalf("pinned[0] content missing: %v", p0)
	}
	warnings, _ := skills["warnings"].([]any)
	found := false
	for _, w := range warnings {
		if w == "pinned skill removed from its source repo: tdd" {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want a removed-from-source-repo warning", warnings)
	}
}

// TestTaskBriefPinnedExcludedFromMatches guards that the skillMatches
// refactor kept the exclusion behavior: a pinned skill that would also match
// by embedding must appear only under pinned, never duplicated into matches.
func TestTaskBriefPinnedExcludedFromMatches(t *testing.T) {
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer fakeSrv.Close()

	st, h, token := newTestServerWithConfig(t, api.Config{EmbeddingURL: fakeSrv.URL, EmbeddingModel: "m"})
	createProject(t, st, "proj")
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	sk, err := st.GetSkill(context.Background(), "tdd")
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}
	if err := st.ReplaceSkillEmbeddings(context.Background(), sk.ID, [][]float32{{1, 0}}); err != nil {
		t.Fatalf("replace embeddings: %v", err)
	}

	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "high", "kind": "feature",
		"skills": []string{"tdd"},
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+task["id"].(string)+"/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	skills := got["skills"].(map[string]any)
	if skills["provider"] != "openai-compatible" {
		t.Fatalf("provider = %v, want openai-compatible", skills["provider"])
	}
	pinned, _ := skills["pinned"].([]any)
	if len(pinned) != 1 {
		t.Fatalf("pinned = %v, want one entry", skills["pinned"])
	}
	matches, _ := skills["matches"].([]any)
	if len(matches) != 0 {
		t.Fatalf("matches = %v, want none: tdd is pinned so must be excluded from matches", skills["matches"])
	}
}

// TestTaskBriefSkillsFalseSkipsTheWork: ?skills=false is for callers that
// read only the task row or the lease (lode status, the pre-renew fetch in
// lode resume). It must skip the work, not just trim the output — no pin
// resolution, no inlined bodies, and no embedding round trip.
func TestTaskBriefSkillsFalseSkipsTheWork(t *testing.T) {
	var embedCalls atomic.Int32
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embedCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer fakeSrv.Close()

	st, h, token := newTestServerWithConfig(t, api.Config{EmbeddingURL: fakeSrv.URL, EmbeddingModel: "m"})
	createProject(t, st, "proj")
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "high", "kind": "feature",
		"skills": []string{"tdd"},
	})
	id := task["id"].(string)

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+id+"/brief?skills=false", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief?skills=false: %d %s", rr.Code, rr.Body.String())
	}
	skills := decodeMap(t, rr)["skills"].(map[string]any)
	if pinned, _ := skills["pinned"].([]any); len(pinned) != 0 {
		t.Fatalf("pinned = %v, want none", skills["pinned"])
	}
	if n := embedCalls.Load(); n != 0 {
		t.Fatalf("embedding provider called %d times for a skills=false brief", n)
	}

	// The default is unchanged: pins resolve and the provider is consulted.
	rr = doReq(t, h, "GET", "/api/v1/tasks/"+id+"/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief: %d %s", rr.Code, rr.Body.String())
	}
	skills = decodeMap(t, rr)["skills"].(map[string]any)
	if pinned, _ := skills["pinned"].([]any); len(pinned) != 1 {
		t.Fatalf("pinned = %v, want one entry by default", skills["pinned"])
	}
	if n := embedCalls.Load(); n != 1 {
		t.Fatalf("embedding provider called %d times for a default brief, want 1", n)
	}
}

// TestTaskBriefMatchQueryFailureDegrades is the brief-path half of the
// degradation contract. A corpus left at two vector dimensions makes every
// cosine query error; the brief is the gate on starting work, so it must
// still serve pins with a warning rather than 500.
func TestTaskBriefMatchQueryFailureDegrades(t *testing.T) {
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer fakeSrv.Close()

	st, h, token := newTestServerWithConfig(t, api.Config{EmbeddingURL: fakeSrv.URL, EmbeddingModel: "m"})
	createProject(t, st, "proj")
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")
	seedSkill(t, st, "two-dim", "Vectors from the old model")
	seedSkill(t, st, "three-dim", "Vectors from the new model")
	ctx := context.Background()
	for name, vec := range map[string][]float32{"two-dim": {1, 0}, "three-dim": {1, 0, 0}} {
		sk, err := st.GetSkill(ctx, name)
		if err != nil {
			t.Fatalf("get skill %s: %v", name, err)
		}
		if err := st.ReplaceSkillEmbeddings(ctx, sk.ID, [][]float32{vec}); err != nil {
			t.Fatalf("replace embeddings %s: %v", name, err)
		}
	}

	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "high", "kind": "feature",
		"skills": []string{"tdd"},
	})
	rr := doReq(t, h, "GET", "/api/v1/tasks/"+task["id"].(string)+"/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, want 200 even when the vector query fails, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	skills := got["skills"].(map[string]any)
	if pinned, _ := skills["pinned"].([]any); len(pinned) != 1 {
		t.Fatalf("pinned = %v, want the pin to survive", skills["pinned"])
	}
	if matches, _ := skills["matches"].([]any); len(matches) != 0 {
		t.Fatalf("matches = %v, want none", skills["matches"])
	}
	if warnings, _ := skills["warnings"].([]any); len(warnings) == 0 {
		t.Fatalf("expected a degradation warning, got none")
	}
}

// TestTaskBriefProviderFailureDegrades guards the 2s degrade-to-pins-only
// behavior on the brief path: a provider failure must never turn a brief
// fetch into a 5xx.
func TestTaskBriefProviderFailureDegrades(t *testing.T) {
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errSrv.Close()

	st, h, token := newTestServerWithConfig(t, api.Config{EmbeddingURL: errSrv.URL, EmbeddingModel: "m"})
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+task["id"].(string)+"/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, want 200 even on provider failure, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	skills := got["skills"].(map[string]any)
	if skills["provider"] != "openai-compatible" {
		t.Fatalf("provider = %v, want openai-compatible (still configured)", skills["provider"])
	}
	matches, _ := skills["matches"].([]any)
	if len(matches) != 0 {
		t.Fatalf("matches = %v, want none on provider failure", skills["matches"])
	}
	warnings, _ := skills["warnings"].([]any)
	if len(warnings) == 0 {
		t.Fatalf("expected a degradation warning, got none")
	}
}
