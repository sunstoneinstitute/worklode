package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestChunks(t *testing.T) {
	if got := Chunks("", 10, 2); got != nil {
		t.Fatalf("empty: %v", got)
	}
	if got := Chunks("short", 10, 2); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short: %v", got)
	}
	got := Chunks(strings.Repeat("a", 25), 10, 2)
	// step = 8: [0:10] [8:18] [16:25]
	if len(got) != 3 || len(got[0]) != 10 || len(got[2]) != 9 {
		t.Fatalf("overlap: %d chunks, lens %d/%d", len(got), len(got[0]), len(got[len(got)-1]))
	}
}

// TestChunksDegenerate covers the size/overlap normalizations that keep the
// chunking loop's step >= 1, so it cannot spin forever.
func TestChunksDegenerate(t *testing.T) {
	s := strings.Repeat("b", 20)

	got := Chunks(s, 0, 0)
	if len(got) != 1 || got[0] != s {
		t.Fatalf("size<=0 should default to ChunkRunes (no splitting for a 20-rune input): %v", got)
	}

	got = Chunks(s, 5, 5)
	// overlap >= size normalizes to overlap=0, so step=5: [0:5][5:10][10:15][15:20]
	if len(got) != 4 {
		t.Fatalf("overlap>=size should normalize overlap to 0: got %d chunks: %v", len(got), got)
	}
	for i, c := range got {
		if len(c) != 5 {
			t.Fatalf("chunk %d: want len 5, got %d (%q)", i, len(c), c)
		}
	}
}

func TestOpenAIEmbed(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		gotModel = req.Model
		// Return out of order to prove index-based reassembly.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":1,"embedding":[0.5,0.5]},{"index":0,"embedding":[1,0]}]}`))
	}))
	defer srv.Close()

	p := &OpenAI{URL: srv.URL, Model: "test-model", Key: "sk-x"}
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 1 || vecs[1][0] != 0.5 {
		t.Fatalf("vecs: %v", vecs)
	}
	if gotAuth != "Bearer sk-x" || gotModel != "test-model" {
		t.Fatalf("auth=%q model=%q", gotAuth, gotModel)
	}
}

// TestOpenAIEmbedNoKey asserts no Authorization header is sent when Key is
// empty, rather than e.g. "Bearer ".
func TestOpenAIEmbedNoKey(t *testing.T) {
	var gotAuth string
	gotAuthSet := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAuthSet = true
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer srv.Close()

	p := &OpenAI{URL: srv.URL, Model: "m"}
	if _, err := p.Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if !gotAuthSet {
		t.Fatal("handler never ran")
	}
	if gotAuth != "" {
		t.Fatalf("want no Authorization header, got %q", gotAuth)
	}
}

// TestOpenAIEmbedEmptyTexts asserts an empty input short-circuits before any
// HTTP call, avoiding a request providers would reject with input:null.
func TestOpenAIEmbedEmptyTexts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for empty input")
	}))
	defer srv.Close()

	p := &OpenAI{URL: srv.URL, Model: "m"}
	vecs, err := p.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if vecs != nil {
		t.Fatalf("want nil vecs, got %v", vecs)
	}
}

// TestOpenAIEmbedMalformedJSON covers a response body that isn't valid JSON.
func TestOpenAIEmbedMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	p := &OpenAI{URL: srv.URL, Model: "m"}
	if _, err := p.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want error for malformed JSON body")
	}
}

func TestOpenAIEmbedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	p := &OpenAI{URL: srv.URL, Model: "m"}
	if _, err := p.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want error")
	}
}

// TestOpenAIEmbedBadIndices ensures duplicated/out-of-range indices are
// rejected rather than silently mismatched to the wrong input texts.
func TestOpenAIEmbedBadIndices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Both entries claim index 0 for two inputs: count matches but
		// indices don't cover [0,1).
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0.5,0.5]}]}`))
	}))
	defer srv.Close()

	p := &OpenAI{URL: srv.URL, Model: "m"}
	if _, err := p.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("want error for duplicated indices")
	}
}

// TestOpenAIEmbedEmptyVector ensures a zero-length embedding is rejected
// with a clear error naming the offending index, rather than propagating
// to the store's ReplaceSkillEmbeddings guard.
func TestOpenAIEmbedEmptyVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[]}]}`))
	}))
	defer srv.Close()

	p := &OpenAI{URL: srv.URL, Model: "m"}
	if _, err := p.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("want error for empty embedding vector")
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("hello", 0); got != "" {
		t.Fatalf("n=0: got %q", got)
	}
	if got := Truncate("hello", -1); got != "" {
		t.Fatalf("n<0: got %q", got)
	}
	if got := Truncate("hello", 100); got != "hello" {
		t.Fatalf("n>len(s): got %q", got)
	}
	// Multi-byte runes: truncate on rune boundaries, not bytes.
	s := "héllo wörld"
	if got := Truncate(s, 3); got != "hél" {
		t.Fatalf("multi-byte: got %q", got)
	}
}

func TestEmbedMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2]}]}`))
	}))
	defer srv.Close()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	p := &OpenAI{URL: srv.URL, Model: "test-model", Metrics: m}

	if _, err := p.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	// Second call against a closed server → error.
	srv.Close()
	if _, err := p.Embed(context.Background(), []string{"hello"}); err == nil {
		t.Fatal("embed against closed server: want error")
	}
	// Empty input makes no HTTP call and records nothing.
	if _, err := p.Embed(context.Background(), nil); err != nil {
		t.Fatalf("embed empty: %v", err)
	}

	if got := testutil.ToFloat64(m.requests.WithLabelValues("ok")); got != 1 {
		t.Fatalf("requests{ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("error")); got != 1 {
		t.Fatalf("requests{error} = %v, want 1", got)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var count uint64
	for _, mf := range mfs {
		if mf.GetName() == "worklode_embed_request_duration_seconds" {
			count = mf.GetMetric()[0].GetHistogram().GetSampleCount()
		}
	}
	if count != 2 {
		t.Fatalf("duration observations = %d, want 2 (the failed call must be timed, the empty one must not)", count)
	}
}

func TestOpenAIID(t *testing.T) {
	a := &OpenAI{URL: "https://api.openai.com/v1/embeddings", Model: "text-embedding-3-small", Key: "sk-secret"}
	b := &OpenAI{URL: "https://api.openai.com/v1/embeddings", Model: "text-embedding-3-large", Key: "sk-secret"}
	c := &OpenAI{URL: "https://api.example.com/v1/embeddings", Model: "text-embedding-3-small", Key: "sk-secret"}
	// Same host, different path: a path-routed gateway (LiteLLM, vLLM,
	// text-embeddings-inference behind a prefix) can serve different
	// backends from one host, so the path must be part of the identity.
	d := &OpenAI{URL: "https://gw.internal/tenant-a/v1/embeddings", Model: "m"}
	e := &OpenAI{URL: "https://gw.internal/tenant-b/v1/embeddings", Model: "m"}

	if a.ID() == "" {
		t.Fatal("ID should not be empty")
	}
	if a.ID() == b.ID() {
		t.Fatalf("different models should produce different IDs: %q == %q", a.ID(), b.ID())
	}
	if a.ID() == c.ID() {
		t.Fatalf("different hosts should produce different IDs: %q == %q", a.ID(), c.ID())
	}
	if d.ID() == e.ID() {
		t.Fatalf("different paths on the same host should produce different IDs: %q == %q", d.ID(), e.ID())
	}
	if a.ID() != a.ID() {
		t.Fatal("ID should be stable across calls")
	}
	if strings.Contains(a.ID(), "sk-secret") {
		t.Fatalf("ID must not leak the API key: %q", a.ID())
	}

	// Userinfo (credentials embedded in the URL) must never reach the ID,
	// including when they carry an actual secret-shaped value.
	userinfo := &OpenAI{URL: "https://user:hunter2@api.example.com/v1/embeddings", Model: "m"}
	if strings.Contains(userinfo.ID(), "hunter2") || strings.Contains(userinfo.ID(), "user:") {
		t.Fatalf("ID must not leak URL userinfo: %q", userinfo.ID())
	}
	if userinfo.ID() != "openai:m@api.example.com/v1/embeddings" {
		t.Fatalf("unexpected ID for userinfo URL: %q", userinfo.ID())
	}

	// Degenerate URLs must never panic, regardless of what ID they produce.
	for _, u := range []string{
		"",
		"://bad",
		"not a url at all",
		"api.example.com/v1/embeddings", // scheme-less
		"https://api.example.com:8443/v1/embeddings",
		"https://user:pass@api.example.com:8443/v1/embeddings?api-key=SECRET#frag",
	} {
		p := &OpenAI{URL: u, Model: "m"}
		got := p.ID()
		if got == "" {
			t.Fatalf("ID(%q) should not be empty", u)
		}
		if strings.Contains(got, "SECRET") || strings.Contains(got, "pass") {
			t.Fatalf("ID(%q) leaked credentials: %q", u, got)
		}
	}
}
