package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/skillsync"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedSkillDirect upserts one skill for the internal recommendation() tests,
// bypassing the HTTP layer since this file lives inside package api.
func seedSkillDirect(t *testing.T, st *store.Store, name, description string) *store.Skill {
	t.Helper()
	ctx := context.Background()
	if _, _, err := st.UpsertSkill(ctx, store.SkillUpsert{
		Qualifier:   "acme",
		Name:        name,
		Description: description,
		SourceRepo:  "acme/skills",
		SourcePath:  "skills/" + name,
		GitCommit:   "deadbeef",
		ContentHash: "h-" + name,
		SkillMD:     "# " + name,
		Frontmatter: json.RawMessage(`{}`),
		Archive:     []byte("archive-" + name),
	}); err != nil {
		t.Fatalf("seed skill %s: %v", name, err)
	}
	sk, err := st.GetSkill(ctx, name)
	if err != nil {
		t.Fatalf("get skill %s: %v", name, err)
	}
	return sk
}

// TestRecommendationPins exercises s.recommendation's pin-resolution branch
// directly, covering the not-found and soft-deleted warnings and the rule
// that a pinned name never also appears in matches.
func TestRecommendationPins(t *testing.T) {
	st := store.OpenTestStore(t)

	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	t.Cleanup(fakeSrv.Close)

	s := &server{
		st:         st,
		log:        slog.Default(),
		skillFloor: 0.35,
		embedder:   &embed.OpenAI{URL: fakeSrv.URL, Model: "m"},
	}

	pinned := seedSkillDirect(t, st, "tdd", "Red-green-refactor discipline")
	if err := st.ReplaceSkillEmbeddings(context.Background(), pinned.ID, [][]float32{{1, 0}}); err != nil {
		t.Fatalf("replace embeddings: %v", err)
	}

	deleted := seedSkillDirect(t, st, "legacy", "Retired skill")
	if _, err := st.SoftDeleteSkillsExcept(context.Background(), "acme/skills", []string{"acme:tdd"}); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if sk, err := st.GetSkill(context.Background(), deleted.Name); err != nil || !sk.Deleted {
		t.Fatalf("legacy not soft-deleted: %v %+v", err, sk)
	}

	rec, err := s.recommendation(context.Background(), "write tests first",
		[]string{"tdd", "legacy", "no-such-skill"}, 5)
	if err != nil {
		t.Fatalf("recommendation: %v", err)
	}

	// tdd is pinned with inline content, and does not also show up as a match
	// even though it is the only vector match for this query.
	if len(rec.Pinned) != 2 {
		t.Fatalf("pinned: %+v", rec.Pinned)
	}
	var gotTDD, gotLegacy bool
	for _, p := range rec.Pinned {
		switch p.Name {
		case "acme:tdd":
			gotTDD = true
			if p.Content == "" {
				t.Fatalf("tdd pin missing inline content: %+v", p)
			}
		case "acme:legacy":
			gotLegacy = true
		}
	}
	if !gotTDD || !gotLegacy {
		t.Fatalf("pinned names: %+v", rec.Pinned)
	}
	if len(rec.Matches) != 0 {
		t.Fatalf("pinned name leaked into matches: %+v", rec.Matches)
	}

	// Warnings: one for the soft-deleted pin, one for the unknown name.
	foundDeletedWarning, foundMissingWarning := false, false
	for _, w := range rec.Warnings {
		if w == "pinned skill removed from its source repo: legacy" {
			foundDeletedWarning = true
		}
		if w == "pinned skill not found: no-such-skill" {
			foundMissingWarning = true
		}
	}
	if !foundDeletedWarning {
		t.Fatalf("missing soft-deleted warning: %v", rec.Warnings)
	}
	if !foundMissingWarning {
		t.Fatalf("missing not-found warning: %v", rec.Warnings)
	}
}

// TestRecommendationNoPins covers the plain vector-match path with no pins at
// all, so a name that is both pinned and a vector match (above) is contrasted
// against the un-pinned case.
func TestRecommendationNoPins(t *testing.T) {
	st := store.OpenTestStore(t)
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	t.Cleanup(fakeSrv.Close)

	s := &server{
		st:         st,
		log:        slog.Default(),
		skillFloor: 0.35,
		embedder:   &embed.OpenAI{URL: fakeSrv.URL, Model: "m"},
	}
	sk := seedSkillDirect(t, st, "tdd", "Red-green-refactor discipline")
	if err := st.ReplaceSkillEmbeddings(context.Background(), sk.ID, [][]float32{{1, 0}}); err != nil {
		t.Fatalf("replace embeddings: %v", err)
	}

	rec, err := s.recommendation(context.Background(), "write tests first", nil, 5)
	if err != nil {
		t.Fatalf("recommendation: %v", err)
	}
	if len(rec.Pinned) != 0 {
		t.Fatalf("pinned: %+v", rec.Pinned)
	}
	if len(rec.Matches) != 1 || rec.Matches[0].Name != "acme:tdd" {
		t.Fatalf("matches: %+v", rec.Matches)
	}
}

// TestNewServerSkillsConfig covers NewServer's skills-config boot validation
// one field at a time: the reviewer found this path had zero coverage, so
// deleting the 0.35 default, the range check, the EmbeddingModel
// requirement, or the appAuth requirement all left the api suite green.
func TestNewServerSkillsConfig(t *testing.T) {
	st := store.OpenTestStore(t)

	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"empty config boots with skills off", Config{}, false},

		{"score floor non-numeric", Config{SkillScoreFloor: "abc"}, true},
		{"score floor below zero", Config{SkillScoreFloor: "-0.1"}, true},
		{"score floor above one", Config{SkillScoreFloor: "1.5"}, true},
		{"score floor NaN", Config{SkillScoreFloor: "NaN"}, true},
		{"score floor leading space", Config{SkillScoreFloor: " 0.5"}, true},
		{"score floor zero", Config{SkillScoreFloor: "0"}, false},
		{"score floor one", Config{SkillScoreFloor: "1"}, false},
		{"score floor mid-range", Config{SkillScoreFloor: "0.5"}, false},

		{"embedding url without model", Config{EmbeddingURL: "https://example.com/embed"}, true},
		{"embedding url with model", Config{EmbeddingURL: "https://example.com/embed", EmbeddingModel: "m"}, false},

		{"skill sources malformed", Config{SkillSources: "not-a-source"}, true},
		{"skill sources without github app", Config{SkillSources: "acme/skills@main:skills/*"}, true},
		// "skill sources with github app configured" lives in its own test,
		// TestNewServerSkillsSourcesWithGitHubApp below: that config makes
		// NewServer fire a boot-time skill sync (see runSkillSync), which
		// needs githubAPIBase redirected at a local server so the test suite
		// never reaches out to the real GitHub API.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := NewServer(st, tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewServer(%+v) err = %v, wantErr %v", tc.cfg, err, tc.wantErr)
			}
		})
	}
}

// TestNewServerInvalidatesEmbeddingsWithoutSkillSources covers the boot path
// for a provider swap on an instance with no skill sources. The check used to
// run only from SyncAll, which such an instance never calls — so dropping the
// sources, or swapping LODE_EMBEDDING_MODEL after doing so, left vectors from
// the old space in place forever. At a different dimension they make every
// query error, and nothing would have cleared them.
func TestNewServerInvalidatesEmbeddingsWithoutSkillSources(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	sk := seedSkillDirect(t, st, "tdd", "Red-green-refactor discipline")
	if err := st.ReplaceSkillEmbeddings(ctx, sk.ID, [][]float32{{1, 0}}); err != nil {
		t.Fatalf("replace embeddings: %v", err)
	}
	if err := st.SetEmbeddingProviderID(ctx, "openai:old-model@example.com/v1/embeddings"); err != nil {
		t.Fatalf("set provider id: %v", err)
	}

	cfg := Config{EmbeddingURL: "https://example.com/v1/embeddings", EmbeddingModel: "new-model"}
	if _, _, err := NewServer(st, cfg); err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	got, err := st.RecommendSkills(ctx, []float32{1, 0}, 5, 0.5)
	if err != nil || len(got) != 0 {
		t.Fatalf("vectors from the previous provider survived boot: %+v err=%v", got, err)
	}
	want := (&embed.OpenAI{URL: cfg.EmbeddingURL, Model: cfg.EmbeddingModel}).ID()
	if id, err := st.EmbeddingProviderID(ctx); err != nil || id != want {
		t.Fatalf("provider id = %q err=%v, want %q", id, err, want)
	}
}

// TestNewServerSkillsSourcesWithGitHubApp covers the "skill sources with
// github app configured" boot case that TestNewServerSkillsConfig can't:
// that config makes NewServer's boot-time skill sync (see runSkillSync)
// fire a real GitHub App auth flow in a background goroutine. Left pointed
// at the real API, that goroutine would reach out to api.github.com — slow,
// flaky, and network-dependent for a unit test — and it can still be
// running when t.Cleanup drops the test database out from under it.
// Redirecting githubAPIBase at a local server keeps the whole flow local and
// fast; the hit counter proves the boot sync actually ran through the
// redirect rather than skipping the network call entirely.
func TestNewServerSkillsSourcesWithGitHubApp(t *testing.T) {
	st := store.OpenTestStore(t)

	var hits int32
	hit := make(chan struct{})
	fakeGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			close(hit)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(fakeGH.Close)

	orig := githubAPIBase
	githubAPIBase = fakeGH.URL
	t.Cleanup(func() { githubAPIBase = orig })

	_, _, err := NewServer(st, Config{
		SkillSources:        "acme/skills@main:skills/*",
		GitHubAppID:         "12345",
		GitHubAppPrivateKey: appTestKeyPEM(t),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	select {
	case <-hit:
	case <-time.After(5 * time.Second):
		t.Fatal("boot sync never reached the fake GitHub server: redirect did not take effect")
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("fake GitHub server hit counter is zero: boot sync did not run through the redirect")
	}
}

// TestDefaultSkillScoreFloor pins the 0.35 default end to end through
// NewServer and the HTTP handler: a stored skill embedding and a query
// vector at a known cosine similarity, just under and just over the
// threshold. (1,0) and (cosθ,sinθ) are both unit vectors, so their cosine
// similarity is exactly cosθ, independent of pgvector's own normalization.
func TestDefaultSkillScoreFloor(t *testing.T) {
	cosVector := func(cos float64) []float32 {
		return []float32{float32(cos), float32(math.Sqrt(1 - cos*cos))}
	}

	if n := recommendMatchCountAtCosine(t, cosVector(0.30)); n != 0 {
		t.Fatalf("cosine 0.30 vs default floor 0.35: got %d matches, want 0", n)
	}
	if n := recommendMatchCountAtCosine(t, cosVector(0.40)); n != 1 {
		t.Fatalf("cosine 0.40 vs default floor 0.35: got %d matches, want 1", n)
	}
}

// recommendMatchCountAtCosine seeds a skill embedded at (1,0), boots a real
// server with no SkillScoreFloor override (so NewServer's 0.35 default
// applies), points its embedder at a fake endpoint that always returns
// query, and returns how many matches /api/v1/skills/recommend reports.
func recommendMatchCountAtCosine(t *testing.T, query []float32) int {
	t.Helper()
	st := store.OpenTestStore(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	vecJSON, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("marshal query vector: %v", err)
	}
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":` + string(vecJSON) + `}]}`))
	}))
	t.Cleanup(fakeSrv.Close)

	cfg := Config{EmbeddingURL: fakeSrv.URL, EmbeddingModel: "m"}
	h, _, err := NewServer(st, cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	// After NewServer: its boot-time provider check clears vectors it cannot
	// attribute to the configured provider, and a store seeded out of band is
	// exactly that case.
	sk := seedSkillDirect(t, st, "tdd", "Red-green-refactor discipline")
	if err := st.ReplaceSkillEmbeddings(ctx, sk.ID, [][]float32{{1, 0}}); err != nil {
		t.Fatalf("replace embeddings: %v", err)
	}

	reqBody, err := json.Marshal(map[string]any{"text": "the fake endpoint ignores this"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/recommend", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("recommend: %d %s", rr.Code, rr.Body)
	}
	var resp model.SkillRecommendation
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return len(resp.Matches)
}

// TestRecommendationPinsSurviveProviderFailure: the degrade-to-pins-only
// guarantee must hold together with pins specifically, not just in
// isolation. Both a hard failure (500) and a timeout past recommendTimeout
// must still return the pin's inline content, an empty match list, and a
// warning.
func TestRecommendationPinsSurviveProviderFailure(t *testing.T) {
	st := store.OpenTestStore(t)
	pinned := seedSkillDirect(t, st, "tdd", "Red-green-refactor discipline")

	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"500", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}},
		{"timeout", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(recommendTimeout + time.Second)
			w.WriteHeader(http.StatusOK)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeSrv := httptest.NewServer(tc.handler)
			t.Cleanup(fakeSrv.Close)
			s := &server{
				st: st, log: slog.Default(), skillFloor: 0.35,
				embedder: &embed.OpenAI{URL: fakeSrv.URL, Model: "m"},
			}

			rec, err := s.recommendation(context.Background(), "write tests first", []string{pinned.Name}, 5)
			if err != nil {
				t.Fatalf("recommendation: %v", err)
			}
			if rec.Provider != "openai-compatible" {
				t.Fatalf("provider: %v", rec.Provider)
			}
			if len(rec.Matches) != 0 {
				t.Fatalf("matches on provider failure: %+v", rec.Matches)
			}
			if len(rec.Warnings) == 0 {
				t.Fatalf("expected a degradation warning, got none")
			}
			if len(rec.Pinned) != 1 || rec.Pinned[0].Name != "acme:tdd" || rec.Pinned[0].Content == "" {
				t.Fatalf("pin did not survive provider failure: %+v", rec.Pinned)
			}
		})
	}
}

// TestSkillMatchesLimitClamped: an ask above the maximum clamps to the
// maximum. Falling back to the default instead made --limit 50 return fewer
// matches than --limit 20, which is the one answer that cannot be right.
func TestSkillMatchesLimitClamped(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx := context.Background()
	for i := 0; i < maxSkillLimit+5; i++ {
		sk := seedSkillDirect(t, st, fmt.Sprintf("skill-%02d", i), "matches everything")
		if err := st.ReplaceSkillEmbeddings(ctx, sk.ID, [][]float32{{1, 0}}); err != nil {
			t.Fatalf("replace embeddings: %v", err)
		}
	}
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	t.Cleanup(fakeSrv.Close)
	s := &server{
		st: st, log: slog.Default(), skillFloor: 0.35,
		embedder: &embed.OpenAI{URL: fakeSrv.URL, Model: "m"},
	}

	matches, _ := s.skillMatches(ctx, "anything", nil, maxSkillLimit+30)
	if len(matches) != maxSkillLimit {
		t.Fatalf("over-large limit returned %d matches, want the cap of %d", len(matches), maxSkillLimit)
	}
	if matches, _ := s.skillMatches(ctx, "anything", nil, 0); len(matches) != defaultSkillLimit {
		t.Fatalf("unset limit returned %d matches, want the default of %d", len(matches), defaultSkillLimit)
	}
}

// seedMixedDimensionCorpus leaves the store in the one state that makes
// every cosine query fail: two skills whose vectors have different
// dimensions. ReplaceSkillEmbeddings validates within a call, not across
// them, so a provider swap that outran an invalidation produces exactly this.
func seedMixedDimensionCorpus(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	two := seedSkillDirect(t, st, "two-dim", "Vectors from the old model")
	three := seedSkillDirect(t, st, "three-dim", "Vectors from the new model")
	if err := st.ReplaceSkillEmbeddings(ctx, two.ID, [][]float32{{1, 0}}); err != nil {
		t.Fatalf("replace 2-dim embeddings: %v", err)
	}
	if err := st.ReplaceSkillEmbeddings(ctx, three.ID, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatalf("replace 3-dim embeddings: %v", err)
	}
	if _, err := st.RecommendSkills(ctx, []float32{1, 0}, 5, 0.35); err == nil {
		t.Fatal("setup: want the corpus to make vector queries fail")
	}
}

// TestRecommendationPinsSurviveMatchQueryFailure is the store-side half of
// the degradation contract: a mixed-dimension corpus makes RecommendSkills
// error, and that must degrade to pins-only exactly like a provider failure
// rather than 500. The brief path shares skillMatches, so a 500 here would
// mean nobody could get a brief either.
func TestRecommendationPinsSurviveMatchQueryFailure(t *testing.T) {
	st := store.OpenTestStore(t)
	pinned := seedSkillDirect(t, st, "tdd", "Red-green-refactor discipline")
	seedMixedDimensionCorpus(t, st)

	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	t.Cleanup(fakeSrv.Close)
	s := &server{
		st: st, log: slog.Default(), skillFloor: 0.35,
		embedder: &embed.OpenAI{URL: fakeSrv.URL, Model: "m"},
	}

	rec, err := s.recommendation(context.Background(), "write tests first", []string{pinned.Name}, 5)
	if err != nil {
		t.Fatalf("recommendation: %v", err)
	}
	if len(rec.Matches) != 0 {
		t.Fatalf("matches from a failing query: %+v", rec.Matches)
	}
	if len(rec.Pinned) != 1 || rec.Pinned[0].Name != "acme:tdd" || rec.Pinned[0].Content == "" {
		t.Fatalf("pin did not survive the query failure: %+v", rec.Pinned)
	}
	found := false
	for _, w := range rec.Warnings {
		if strings.Contains(w, "skill matching unavailable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a matching-unavailable warning, got %v", rec.Warnings)
	}
}

// tarballOf builds a GitHub-shaped tarball: entries under root/, gzipped —
// mirrors skillsync's own test helper, which lives in a different package
// and is not importable from here.
func tarballOf(t *testing.T, root string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for p, c := range files {
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + p, Mode: 0o644, Size: int64(len(c)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(c)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// TestSyncSkillsPartialFailure: one source fetch fails, one succeeds, and the
// real work done by the successful one must survive in the response rather
// than being thrown away behind a bare 502.
func TestSyncSkillsPartialFailure(t *testing.T) {
	st := store.OpenTestStore(t)
	goodTarball := tarballOf(t, "acme-good-sha1", map[string]string{
		"skills/tdd/SKILL.md": "---\nname: tdd\ndescription: Red-green-refactor discipline\n---\n\nBody.",
	})

	fetch := func(ctx context.Context, repo, ref string) ([]byte, error) {
		switch repo {
		case "acme/good":
			return goodTarball, nil
		case "acme/bad":
			return nil, fmt.Errorf("fetch %s: simulated failure", repo)
		default:
			return nil, fmt.Errorf("unexpected repo %s", repo)
		}
	}

	s := &server{
		st:  st,
		log: slog.Default(),
		skillSources: []skillsync.Source{
			{Repo: "acme/good", Ref: "main", Glob: "skills/*"},
			{Repo: "acme/bad", Ref: "main", Glob: "skills/*"},
		},
		skillSyncer: &skillsync.Syncer{Store: st, Fetch: fetch, Log: slog.Default()},
	}
	s.initMetrics(prometheus.NewRegistry())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/sync", nil)
	rr := httptest.NewRecorder()
	s.syncSkills(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", rr.Code, rr.Body)
	}
	var resp model.SkillSyncReport
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Synced != 1 || resp.Changed != 1 {
		t.Fatalf("counts did not survive the partial failure: %+v", resp)
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "acme/bad") {
		t.Fatalf("errors: %+v", resp.Errors)
	}

	// The admin sync path (this handler) must record metrics itself, not
	// just the coalesced background run it may trigger.
	if got := testutil.ToFloat64(s.syncRuns.WithLabelValues("error")); got != 1 {
		t.Fatalf("syncRuns{error} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.syncItems.WithLabelValues("synced")); got != 1 {
		t.Fatalf("syncItems{synced} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.syncItems.WithLabelValues("changed")); got != 1 {
		t.Fatalf("syncItems{changed} = %v, want 1", got)
	}

	sk, err := st.GetSkill(context.Background(), "tdd")
	if err != nil || sk.Deleted {
		t.Fatalf("good source's skill was not persisted: %v %+v", err, sk)
	}
}

// TestSyncOnceLogsFailureAtError: the background sync path used to log a
// failure at Warn while the HTTP path logged Error. A background failure is
// precisely the one nobody is watching a response for, so it gets the higher
// level, not the lower one.
func TestSyncOnceLogsFailureAtError(t *testing.T) {
	st := store.OpenTestStore(t)
	var logbuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logbuf, nil))
	s := &server{
		st: st, log: log,
		skillSources: []skillsync.Source{{Repo: "acme/p", Ref: "main", Glob: "skills/*"}},
		skillSyncer: &skillsync.Syncer{Store: st, Log: log,
			Fetch: func(ctx context.Context, repo, ref string) ([]byte, error) {
				return nil, fmt.Errorf("simulated failure")
			}},
	}
	s.syncOnce(context.Background(), "test")
	if got := logbuf.String(); !strings.Contains(got, "level=ERROR") || !strings.Contains(got, "skill sync failed") {
		t.Fatalf("want the background failure at ERROR, got: %s", got)
	}
}

// TestSyncSkillsCoalescesPendingPush: a webhook push arriving during an
// operator's `lode skills sync` finds the mutex held, so runSkillSync only
// records it in skillSyncPending. The admin handler holds that mutex without
// runSkillSync's drain loop, so it has to consume the flag itself — otherwise
// the push is dropped, and on a quiet repo the next trigger may be a restart.
func TestSyncSkillsCoalescesPendingPush(t *testing.T) {
	st := store.OpenTestStore(t)
	tb := tarballOf(t, "acme-p-aaa111", map[string]string{
		"skills/tdd/SKILL.md": "---\nname: tdd\ndescription: Red-green-refactor discipline\n---\n\nBody.",
	})
	started := make(chan struct{})
	release := make(chan struct{})
	var fetches atomic.Int32
	fetch := func(ctx context.Context, repo, ref string) ([]byte, error) {
		if fetches.Add(1) == 1 {
			close(started)
			<-release
		}
		return tb, nil
	}
	s := &server{
		st: st, log: slog.Default(), bgCtx: context.Background(),
		skillSources: []skillsync.Source{{Repo: "acme/p", Ref: "main", Glob: "skills/*"}},
		skillSyncer:  &skillsync.Syncer{Store: st, Fetch: fetch, Log: slog.Default()},
	}

	code := make(chan int, 1)
	go func() {
		rr := httptest.NewRecorder()
		s.syncSkills(rr, httptest.NewRequest(http.MethodPost, "/api/v1/skills/sync", nil))
		code <- rr.Code
	}()

	<-started // the admin sync now holds skillSyncMu
	s.runSkillSync(context.Background(), "webhook push")
	if !s.skillSyncPending.Load() {
		t.Fatal("a push during an admin sync should have set skillSyncPending")
	}
	close(release)

	if got := <-code; got != http.StatusOK {
		t.Fatalf("admin sync: %d", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for fetches.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the push was dropped: no sync ran after the admin sync finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSyncSkillsTotalFailure covers the zero-Summary branch: when
// nothing at all synced, the response is a generic 502 (the detail is
// logged server-side, not leaked — see mapStoreErr's "logged, not leaked"
// convention).
func TestSyncSkillsTotalFailure(t *testing.T) {
	st := store.OpenTestStore(t)
	fetch := func(ctx context.Context, repo, ref string) ([]byte, error) {
		return nil, fmt.Errorf("fetch %s: simulated failure with a secret token abc123", repo)
	}
	s := &server{
		st:           st,
		log:          slog.Default(),
		skillSources: []skillsync.Source{{Repo: "acme/bad", Ref: "main", Glob: "skills/*"}},
		skillSyncer:  &skillsync.Syncer{Store: st, Fetch: fetch, Log: slog.Default()},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/sync", nil)
	rr := httptest.NewRecorder()
	s.syncSkills(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("sync: %d %s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), "secret token") {
		t.Fatalf("generic 502 leaked the underlying error: %s", rr.Body)
	}
}

// TestSyncSkillsConflictWhenSyncRunning covers the TryLock swap: a sync
// already holding skillSyncMu (webhook push or boot, in practice) must make
// the admin endpoint fail fast with 409, not block for up to
// skillSyncTimeout and then surface a 502 that looks like a GitHub problem.
func TestSyncSkillsConflictWhenSyncRunning(t *testing.T) {
	st := store.OpenTestStore(t)
	s := &server{
		st:           st,
		log:          slog.Default(),
		skillSources: []skillsync.Source{{Repo: "acme/skills", Ref: "main", Glob: "skills/*"}},
		skillSyncer: &skillsync.Syncer{Store: st, Fetch: func(ctx context.Context, repo, ref string) ([]byte, error) {
			return nil, fmt.Errorf("must not be called: sync should short-circuit on the held lock")
		}, Log: slog.Default()},
	}

	s.skillSyncMu.Lock()
	defer s.skillSyncMu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/sync", nil)
	rr := httptest.NewRecorder()
	s.syncSkills(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("sync while another is running: %d %s, want 409", rr.Code, rr.Body)
	}
}

// TestRunSkillSyncCoalescesConcurrentCalls covers the same TryLock
// coalescing from runSkillSync's side (the webhook/boot trigger path): a
// second call while one is in flight must return immediately instead of
// queueing behind it, and must never call Fetch.
func TestRunSkillSyncCoalescesConcurrentCalls(t *testing.T) {
	st := store.OpenTestStore(t)
	s := &server{
		st:           st,
		log:          slog.Default(),
		skillSources: []skillsync.Source{{Repo: "acme/skills", Ref: "main", Glob: "skills/*"}},
		skillSyncer: &skillsync.Syncer{Store: st, Fetch: func(ctx context.Context, repo, ref string) ([]byte, error) {
			return nil, fmt.Errorf("must not be called: coalescing should skip this run entirely")
		}, Log: slog.Default()},
	}

	// Simulate a sync already in flight.
	s.skillSyncMu.Lock()
	defer s.skillSyncMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.runSkillSync(context.Background(), "test")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runSkillSync blocked instead of coalescing via TryLock")
	}
}

// TestRunSkillSyncNoopWithoutSyncer covers the nil-skillSyncer guard: a
// server built without skill sources must not panic or block when the
// webhook/boot trigger calls runSkillSync.
func TestRunSkillSyncNoopWithoutSyncer(t *testing.T) {
	s := &server{log: slog.Default()}

	done := make(chan struct{})
	go func() {
		s.runSkillSync(context.Background(), "test")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runSkillSync did not return promptly with a nil skillSyncer")
	}
}

// TestRunSkillSyncReRunsTriggerThatArrivesMidFlight covers the coalescing
// path a TryLock-drop would silently lose: a trigger arriving while a sync
// is already in flight must not be discarded. It should mark
// skillSyncPending and get a second pass once the in-flight run finishes —
// otherwise new content pushed mid-sync is never re-checked until the next
// trigger (which may not come, on a quiet repo) or a restart.
func TestRunSkillSyncReRunsTriggerThatArrivesMidFlight(t *testing.T) {
	st := store.OpenTestStore(t)

	var fetchCalls int32
	fetchStarted := make(chan struct{})
	release := make(chan struct{})
	fetch := func(ctx context.Context, repo, ref string) ([]byte, error) {
		n := atomic.AddInt32(&fetchCalls, 1)
		if n == 1 {
			close(fetchStarted)
			<-release // hold the first run "in flight" until the test releases it
		}
		return nil, fmt.Errorf("simulated failure %d", n)
	}
	s := &server{
		st:           st,
		log:          slog.Default(),
		skillSources: []skillsync.Source{{Repo: "acme/skills", Ref: "main", Glob: "skills/*"}},
		skillSyncer:  &skillsync.Syncer{Store: st, Fetch: fetch, Log: slog.Default()},
	}

	syncADone := make(chan struct{})
	go func() {
		s.runSkillSync(context.Background(), "A")
		close(syncADone)
	}()
	select {
	case <-fetchStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("sync A never started")
	}

	// A second trigger while A is in flight must coalesce (TryLock fails and
	// returns immediately), not block waiting for A to finish.
	triggerBDone := make(chan struct{})
	go func() {
		s.runSkillSync(context.Background(), "B")
		close(triggerBDone)
	}()
	select {
	case <-triggerBDone:
	case <-time.After(2 * time.Second):
		t.Fatal("trigger B blocked instead of coalescing via TryLock")
	}

	close(release) // let A's Fetch return, so the coalesced re-run can proceed

	select {
	case <-syncADone:
	case <-time.After(5 * time.Second):
		t.Fatal("runSkillSync did not complete its coalesced re-run")
	}

	if got := atomic.LoadInt32(&fetchCalls); got != 2 {
		t.Fatalf("Fetch calls = %d, want 2 (B's trigger must cause a re-run, not be dropped)", got)
	}
}

// TestRunSkillSyncAbortsWhenBackgroundContextCancelled covers Config.
// BackgroundCtx: the ctx passed to runSkillSync (cfg.BackgroundCtx in
// production, wired to cmd/serve.go's shutdown signal) must reach
// skillSyncer.SyncAll, so cancelling it — as happens on SIGTERM — aborts an
// in-flight background sync instead of leaving it to run for up to
// skillSyncTimeout past shutdown.
func TestRunSkillSyncAbortsWhenBackgroundContextCancelled(t *testing.T) {
	st := store.OpenTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())

	fetchStarted := make(chan struct{})
	fetch := func(fctx context.Context, repo, ref string) ([]byte, error) {
		close(fetchStarted)
		<-fctx.Done() // blocks until the sync's own context is cancelled
		return nil, fctx.Err()
	}
	s := &server{
		st:           st,
		log:          slog.Default(),
		skillSources: []skillsync.Source{{Repo: "acme/skills", Ref: "main", Glob: "skills/*"}},
		skillSyncer:  &skillsync.Syncer{Store: st, Fetch: fetch, Log: slog.Default()},
	}

	done := make(chan struct{})
	go func() {
		s.runSkillSync(ctx, "test")
		close(done)
	}()

	select {
	case <-fetchStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("sync never started")
	}

	cancel() // simulate shutdown: the background context is cancelled

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runSkillSync did not abort when its background context was cancelled")
	}
}
