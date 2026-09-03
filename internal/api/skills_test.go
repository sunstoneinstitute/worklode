package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedSkill upserts one skill with a deterministic hash ("h-<name>") and a
// non-empty archive, so archive-download and recommend tests have something
// to read.
func seedSkill(t *testing.T, st *store.Store, name, description string) {
	t.Helper()
	_, _, err := st.UpsertSkill(context.Background(), store.SkillUpsert{
		Qualifier:   "acme",
		Name:        name,
		Description: description,
		SourceRepo:  "acme/skills",
		SourcePath:  "skills/" + name,
		GitCommit:   "deadbeef",
		ContentHash: "h-" + name,
		SkillMD:     "# " + name + "\n\n" + description,
		Frontmatter: json.RawMessage(`{}`),
		Archive:     []byte("gzip-archive-" + name),
	})
	if err != nil {
		t.Fatalf("seed skill %s: %v", name, err)
	}
}

func TestSkillsEndpoints(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")
	seedSkill(t, st, "debugging", "Systematic debugging loop")

	// List.
	rr := doReq(t, h, "GET", "/api/v1/skills", token, nil)
	if rr.Code != 200 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
	body := decodeMap(t, rr)
	skills, _ := body["skills"].([]any)
	if len(skills) != 2 {
		t.Fatalf("list count: %v", body["skills"])
	}

	// Get.
	rr = doReq(t, h, "GET", "/api/v1/skills/tdd", token, nil)
	if rr.Code != 200 {
		t.Fatalf("get: %d", rr.Code)
	}
	got := decodeMap(t, rr)
	if got["name"] != "acme:tdd" || got["hash"] != "h-tdd" {
		t.Fatalf("get body: %v", got)
	}

	// Get missing: 404.
	rr = doReq(t, h, "GET", "/api/v1/skills/nope", token, nil)
	if rr.Code != 404 {
		t.Fatalf("get missing: %d", rr.Code)
	}

	// Archive round-trips bytes with the right content type.
	rr = doReq(t, h, "GET", "/api/v1/skills/tdd/archive/h-tdd", token, nil)
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("archive: %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if rr.Body.String() != "gzip-archive-tdd" {
		t.Fatalf("archive body: %q", rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/api/v1/skills/tdd/archive/wrong", token, nil)
	if rr.Code != 404 {
		t.Fatalf("archive miss: %d", rr.Code)
	}

	// Recommend without a provider: pins-only degradation, provider "none".
	rr = doReq(t, h, "POST", "/api/v1/skills/recommend", token,
		map[string]any{"text": "write tests first"})
	if rr.Code != 200 {
		t.Fatalf("recommend: %d %s", rr.Code, rr.Body)
	}
	body = decodeMap(t, rr)
	if body["provider"] != "none" {
		t.Fatalf("provider: %v", body["provider"])
	}
	if m, _ := body["matches"].([]any); len(m) != 0 {
		t.Fatalf("matches without a provider: %v", body["matches"])
	}
	// On the raw body, not the decoded map: a nil slice marshals to null, and
	// a client iterating matches would break on it. len() cannot tell the two
	// apart.
	if !strings.Contains(rr.Body.String(), `"matches":[]`) {
		t.Fatalf("matches must serialize as [], not null: %s", rr.Body.String())
	}

	// Recommend requires exactly one of task_id/text.
	rr = doReq(t, h, "POST", "/api/v1/skills/recommend", token, map[string]any{})
	if rr.Code != 422 {
		t.Fatalf("recommend neither: %d", rr.Code)
	}
	rr = doReq(t, h, "POST", "/api/v1/skills/recommend", token,
		map[string]any{"task_id": "WL-1", "text": "x"})
	if rr.Code != 422 {
		t.Fatalf("recommend both: %d", rr.Code)
	}

	// Sync without configuration: 422. token is alice's, an admin token.
	rr = doReq(t, h, "POST", "/api/v1/skills/sync", token, nil)
	if rr.Code != 422 {
		t.Fatalf("sync unconfigured: %d", rr.Code)
	}

	// Sync as non-admin: 403.
	userToken := seedActor(t, st, "bob", "human", "Bob", false)
	rr = doReq(t, h, "POST", "/api/v1/skills/sync", userToken, nil)
	if rr.Code != 403 {
		t.Fatalf("sync non-admin: %d", rr.Code)
	}
}

// TestRecommendWithProvider exercises the embedding-provider path: a fixed
// vector from a fake embeddings endpoint, a skill embedded with the same
// vector, and both the text and task_id request shapes.
func TestRecommendWithProvider(t *testing.T) {
	t.Parallel()
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":` + store.VecJSONForTests(1, 0) + `}]}`))
	}))
	defer fakeSrv.Close()

	st, h, token := newTestServerWithConfig(t, api.Config{EmbeddingURL: fakeSrv.URL, EmbeddingModel: "m"})
	createProject(t, st, "proj")
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	sk, err := st.GetSkill(context.Background(), "tdd")
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}
	if err := st.SeedSkillChunksForTests(context.Background(), sk.ID, [][]float32{store.VecForTests(1, 0)}); err != nil {
		t.Fatalf("replace embeddings: %v", err)
	}

	// text path: the fake vector is an exact match for the stored embedding.
	rr := doReq(t, h, "POST", "/api/v1/skills/recommend", token,
		map[string]any{"text": "write tests first"})
	if rr.Code != 200 {
		t.Fatalf("recommend: %d %s", rr.Code, rr.Body)
	}
	body := decodeMap(t, rr)
	if body["provider"] != "openai-compatible" {
		t.Fatalf("provider: %v", body["provider"])
	}
	matches, _ := body["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("matches: %v", body["matches"])
	}
	m0, _ := matches[0].(map[string]any)
	if m0["name"] != "acme:tdd" {
		t.Fatalf("match name: %v", m0["name"])
	}
	if score, ok := m0["score"].(float64); !ok || score < 0.99 {
		t.Fatalf("match score: %v", m0["score"])
	}

	// task_id path: the task pins "tdd", so its content must come back in
	// "pinned" — and, since it is now also the embedding match, it must be
	// excluded from "matches" so the same skill is not surfaced twice.
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "high", "kind": "feature",
		"skills": []string{"tdd"},
	})
	rr = doReq(t, h, "POST", "/api/v1/skills/recommend", token,
		map[string]any{"task_id": task["id"]})
	if rr.Code != 200 {
		t.Fatalf("recommend by task: %d %s", rr.Code, rr.Body)
	}
	body = decodeMap(t, rr)
	pinned, _ := body["pinned"].([]any)
	if len(pinned) != 1 {
		t.Fatalf("pinned by task: %v", body["pinned"])
	}
	p0, _ := pinned[0].(map[string]any)
	if p0["name"] != "acme:tdd" {
		t.Fatalf("pinned name: %v", p0["name"])
	}
	if p0["content"] == "" || p0["content"] == nil {
		t.Fatalf("pinned content missing: %v", p0["content"])
	}
	matches, _ = body["matches"].([]any)
	if len(matches) != 0 {
		t.Fatalf("matches by task: want none (tdd is pinned, so excluded from matches), got %v", body["matches"])
	}

	// task_id for a missing task still maps to 404 through mapStoreErr.
	rr = doReq(t, h, "POST", "/api/v1/skills/recommend", token, map[string]any{"task_id": "WL-999"})
	if rr.Code != 404 {
		t.Fatalf("recommend missing task: %d", rr.Code)
	}

	// Provider errors degrade to pins-only with a warning rather than fail
	// the request: recommendations must never block work.
	t.Run("provider 500 degrades to pins-only", func(t *testing.T) {
		errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer errSrv.Close()
		_, h, token := newTestServerWithConfig(t, api.Config{EmbeddingURL: errSrv.URL, EmbeddingModel: "m"})
		assertDegradedRecommend(t, h, token)
	})

	t.Run("provider timeout degrades to pins-only", func(t *testing.T) {
		slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(3 * time.Second) // longer than the server's recommendTimeout (2s)
			w.WriteHeader(http.StatusOK)
		}))
		defer slowSrv.Close()
		_, h, token := newTestServerWithConfig(t, api.Config{EmbeddingURL: slowSrv.URL, EmbeddingModel: "m"})
		assertDegradedRecommend(t, h, token)
	})
}

// assertDegradedRecommend POSTs a recommend request and asserts the
// pins-only degradation shape: 200, provider still reported as configured,
// no matches, and a warning explaining why.
func assertDegradedRecommend(t *testing.T, h http.Handler, token string) {
	t.Helper()
	rr := doReq(t, h, "POST", "/api/v1/skills/recommend", token, map[string]any{"text": "write tests first"})
	if rr.Code != 200 {
		t.Fatalf("recommend: %d %s", rr.Code, rr.Body)
	}
	body := decodeMap(t, rr)
	if body["provider"] != "openai-compatible" {
		t.Fatalf("provider: %v", body["provider"])
	}
	if m, _ := body["matches"].([]any); len(m) != 0 {
		t.Fatalf("matches on provider failure: %v", body["matches"])
	}
	if w, _ := body["warnings"].([]any); len(w) == 0 {
		t.Fatalf("expected a degradation warning, got none")
	}
}
