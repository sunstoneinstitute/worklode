package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedSkillDirect upserts one skill for the internal recommendation() tests,
// bypassing the HTTP layer since this file lives inside package api.
func seedSkillDirect(t *testing.T, st *store.Store, name, description string) *store.Skill {
	t.Helper()
	ctx := context.Background()
	if _, _, err := st.UpsertSkill(ctx, store.SkillUpsert{
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
// directly: task_id can't carry pins until Task 8 adds tasks.skills, so this
// is the only path that exercises it before then.
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
	if _, err := st.SoftDeleteSkillsExcept(context.Background(), "acme/skills", []string{"tdd"}); err != nil {
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
		case "tdd":
			gotTDD = true
			if p.Content == "" {
				t.Fatalf("tdd pin missing inline content: %+v", p)
			}
		case "legacy":
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
	if len(rec.Matches) != 1 || rec.Matches[0].Name != "tdd" {
		t.Fatalf("matches: %+v", rec.Matches)
	}
}
