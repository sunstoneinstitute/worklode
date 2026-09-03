package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/corpusindex"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedChunk writes one chunk for subj, text-only when vec is nil (the
// lexical arm only, matching a no-provider instance, 040 §11).
func seedChunk(t *testing.T, st *store.Store, subj store.ChunkSubject, text string, vec []float32) {
	t.Helper()
	if subj.ContentHash == "" {
		subj.ContentHash = "seed"
	}
	var vecs [][]float32
	if vec != nil {
		vecs = [][]float32{vec}
	}
	chunks := []corpusindex.Chunk{{Text: text}}
	if err := st.ReplaceSubjectChunks(context.Background(), subj, chunks, vecs); err != nil {
		t.Fatalf("seed chunk for %s: %v", subj.Kind, err)
	}
}

func TestSearchGuard(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/search?q=x", "", nil)
	if rr.Code < 400 || rr.Code >= 500 {
		t.Fatalf("anonymous search = %d, want a 4xx", rr.Code)
	}
}

// TestSearchDegraded covers 040 §11: a no-provider instance still serves
// real lexical results, and an explicit mode=dense degrades the same way.
func TestSearchDegraded(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "medium", "kind": "feature",
	})
	seedChunk(t, st, store.ChunkSubject{Kind: store.SubjectTask, TaskID: task["id"].(string), Project: "proj"},
		"The quokka is a small marsupial.", nil)

	rr := doReq(t, h, "GET", "/api/v1/search?q=quokka", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rr.Code, rr.Body)
	}
	var resp model.SearchResponse
	decodeInto(t, rr, &resp)
	if resp.Provider != "none" || resp.Mode != "lexical" {
		t.Fatalf("provider/mode: %q/%q", resp.Provider, resp.Mode)
	}
	if len(resp.Hits) == 0 {
		t.Fatal("degraded search returned no hits, want real lexical results")
	}

	// mode=dense on the same degraded instance still degrades to lexical
	// with real hits, not a 4xx and not an empty set.
	rr = doReq(t, h, "GET", "/api/v1/search?q=quokka&mode=dense", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("search mode=dense degraded: %d %s", rr.Code, rr.Body)
	}
	decodeInto(t, rr, &resp)
	if resp.Mode != "lexical" || len(resp.Hits) == 0 {
		t.Fatalf("mode=dense degraded: mode=%q hits=%d", resp.Mode, len(resp.Hits))
	}
}

// TestSearchArmRanks checks the per-arm ranks reach the wire, both as typed
// fields and as raw JSON keys (a dropped json tag would pass the typed
// decode but fail the map decode).
func TestSearchArmRanks(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "medium", "kind": "feature",
	})
	seedChunk(t, st, store.ChunkSubject{Kind: store.SubjectTask, TaskID: task["id"].(string), Project: "proj"},
		"A wombat digs a burrow.", nil)

	rr := doReq(t, h, "GET", "/api/v1/search?q=wombat", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rr.Code, rr.Body)
	}
	raw := decodeMap(t, rr)
	hits, _ := raw["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("hits: %v", raw["hits"])
	}
	h0, _ := hits[0].(map[string]any)
	if _, ok := h0["dense_rank"]; !ok {
		t.Fatalf("hit missing dense_rank key: %v", h0)
	}
	lr, ok := h0["lexical_rank"]
	if !ok {
		t.Fatalf("hit missing lexical_rank key: %v", h0)
	}
	if lr.(float64) < 1 {
		t.Fatalf("lexical_rank: want >= 1, got %v", lr)
	}

	var resp model.SearchResponse
	decodeInto(t, rr, &resp)
	if len(resp.Hits) != 1 || resp.Hits[0].LexicalRank < 1 {
		t.Fatalf("typed hits: %+v", resp.Hits)
	}
}

// TestSearchFiltering covers kind and project narrowing.
func TestSearchFiltering(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")

	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "medium", "kind": "feature",
	})
	seedChunk(t, st, store.ChunkSubject{Kind: store.SubjectTask, TaskID: task["id"].(string), Project: "proj"},
		"A gizmo whirs in the workshop.", nil)

	doc := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 901, Slug: "901-search-fixture", Body: docSpecBody,
	})
	seedChunk(t, st, store.ChunkSubject{Kind: store.SubjectDoc, DocID: doc.ID, Project: "proj"},
		"A gizmo whirs in the workshop.", nil)

	// kind=task narrows to the task hit only.
	rr := doReq(t, h, "GET", "/api/v1/search?q=gizmo&kind=task", token, nil)
	var resp model.SearchResponse
	decodeInto(t, rr, &resp)
	if len(resp.Hits) != 1 || resp.Hits[0].Kind != store.SubjectTask {
		t.Fatalf("kind=task hits: %+v", resp.Hits)
	}

	// kind=doc&kind=task returns both.
	rr = doReq(t, h, "GET", "/api/v1/search?q=gizmo&kind=doc&kind=task", token, nil)
	decodeInto(t, rr, &resp)
	if len(resp.Hits) != 2 {
		t.Fatalf("kind=doc,task hits: %+v", resp.Hits)
	}

	// project=proj matches; project=other does not.
	rr = doReq(t, h, "GET", "/api/v1/search?q=gizmo&project=proj", token, nil)
	decodeInto(t, rr, &resp)
	if len(resp.Hits) != 2 {
		t.Fatalf("project=proj hits: %+v", resp.Hits)
	}
	rr = doReq(t, h, "GET", "/api/v1/search?q=gizmo&project=other", token, nil)
	decodeInto(t, rr, &resp)
	if len(resp.Hits) != 0 {
		t.Fatalf("project=other hits: %+v", resp.Hits)
	}
}

func TestSearchBadInput(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)

	if rr := doReq(t, h, "GET", "/api/v1/search", token, nil); rr.Code != http.StatusBadRequest {
		t.Fatalf("missing q: %d", rr.Code)
	}
	if rr := doReq(t, h, "GET", "/api/v1/search?q=x&mode=sideways", token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad mode: %d", rr.Code)
	}
	if rr := doReq(t, h, "GET", "/api/v1/search?q=x&kind=banana", token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad kind: %d", rr.Code)
	}
	if rr := doReq(t, h, "GET", "/api/v1/search?q=x&limit=abc", token, nil); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: %d", rr.Code)
	}
}

func TestSearchProviderConfigured(t *testing.T) {
	t.Parallel()
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":` + store.VecJSONForTests(1, 0) + `}]}`))
	}))
	defer fakeSrv.Close()

	_, h, token := newTestServerWithConfig(t, api.Config{EmbeddingURL: fakeSrv.URL, EmbeddingModel: "m"})

	rr := doReq(t, h, "GET", "/api/v1/search?q=quokka", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rr.Code, rr.Body)
	}
	var resp model.SearchResponse
	decodeInto(t, rr, &resp)
	if resp.Provider != "openai-compatible" || resp.Mode != "hybrid" {
		t.Fatalf("provider/mode: %q/%q", resp.Provider, resp.Mode)
	}
}

// TestSearchProviderFailing checks that a failing embedding provider
// degrades to lexical rather than 5xx-ing the whole search (040 §11).
func TestSearchProviderFailing(t *testing.T) {
	t.Parallel()
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fakeSrv.Close()

	_, h, token := newTestServerWithConfig(t, api.Config{EmbeddingURL: fakeSrv.URL, EmbeddingModel: "m"})

	rr := doReq(t, h, "GET", "/api/v1/search?q=quokka", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rr.Code, rr.Body)
	}
	var resp model.SearchResponse
	decodeInto(t, rr, &resp)
	if resp.Mode != "lexical" || resp.Provider != "openai-compatible" {
		t.Fatalf("provider/mode: %q/%q", resp.Provider, resp.Mode)
	}
}
