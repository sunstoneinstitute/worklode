package store

import (
	"context"
	"errors"
	"testing"
)

func TestSkillEmbeddingsRecommend(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"tdd", "debugging"} {
		if _, _, err := s.UpsertSkill(ctx, testSkillUpsert(name, "h-"+name)); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	dbg, _ := s.GetSkill(ctx, "debugging")

	// Orthogonal-ish unit vectors: query matches tdd chunk 1 best.
	if err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{1, 0, 0}, {0.9, 0.1, 0}}); err != nil {
		t.Fatalf("embed tdd: %v", err)
	}
	if err := s.ReplaceSkillEmbeddings(ctx, dbg.ID, [][]float32{{0, 1, 0}}); err != nil {
		t.Fatalf("embed dbg: %v", err)
	}

	got, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5)
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	if len(got) != 1 || got[0].Name != "p:tdd" || got[0].Score < 0.99 {
		t.Fatalf("recommend: %+v", got)
	}

	// Floor at 0 returns both, best-first, max over chunks.
	got, err = s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0)
	if err != nil || len(got) != 2 || got[0].Name != "p:tdd" {
		t.Fatalf("recommend all: %+v err=%v", got, err)
	}

	// Replace wipes old chunks.
	if err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{0, 0, 1}}); err != nil {
		t.Fatalf("re-embed: %v", err)
	}
	got, _ = s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5)
	if len(got) != 0 {
		t.Fatalf("after replace: %+v", got)
	}

	// Soft-deleted skills are never recommended.
	if err := s.ReplaceSkillEmbeddings(ctx, dbg.ID, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatalf("re-embed dbg: %v", err)
	}
	if _, err := s.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ = s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0.5)
	if len(got) != 0 {
		t.Fatalf("deleted still recommended: %+v", got)
	}
}

// TestReplaceSkillEmbeddingsMixedDimensions guards the invariant documented
// in 0007_skills.up.sql: the embedding column has no dimension typmod, so
// mismatched vector lengths would insert happily and then break every
// cosine query over the whole table. Reject before writing anything.
func TestReplaceSkillEmbeddingsMixedDimensions(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")

	err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{1, 0, 0}, {1, 0}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("mismatched dims: want ErrInvalidInput, got %v", err)
	}

	// Nothing should have been written.
	got, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("expected no embeddings written: %+v err=%v", got, err)
	}

	// Zero-length vector is rejected too.
	err = s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero-length vector: want ErrInvalidInput, got %v", err)
	}
}

// TestReplaceSkillEmbeddingsZeroNorm guards against a NaN cosine score:
// pgvector's <=> returns NaN against an all-zero vector, and NaN sorts
// above every real score in Postgres, so one degenerate chunk would rank
// first for every query forever.
func TestReplaceSkillEmbeddingsZeroNorm(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")

	err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{1, 0, 0}, {0, 0, 0}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero-norm vector: want ErrInvalidInput, got %v", err)
	}

	got, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("expected no embeddings written: %+v err=%v", got, err)
	}
}

// TestRecommendSkillsZeroNormQuery guards the same NaN failure mode from
// the query side: a zero query vector would otherwise score every skill
// NaN and return the whole corpus in arbitrary order.
func TestRecommendSkillsZeroNormQuery(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	if err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatalf("embed: %v", err)
	}

	if _, err := s.RecommendSkills(ctx, []float32{0, 0, 0}, 5, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero-norm query: want ErrInvalidInput, got %v", err)
	}
	if _, err := s.RecommendSkills(ctx, nil, 5, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero-length query: want ErrInvalidInput, got %v", err)
	}
}

// TestRecommendSkillsInvalidLimit guards limit<=0, which would otherwise
// reach Postgres as a raw, unclassified "LIMIT must not be negative" error.
func TestRecommendSkillsInvalidLimit(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, 0, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit=0: want ErrInvalidInput, got %v", err)
	}
	if _, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, -1, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit=-1: want ErrInvalidInput, got %v", err)
	}
}

// TestRecommendSkillsLimitTruncates guards that limit is actually passed
// through to the query, not just accepted: with three matching skills and
// limit=2, only the two best-scoring should come back.
func TestRecommendSkillsLimitTruncates(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	names := []string{"tdd", "debugging", "diagnose"}
	vecs := [][]float32{{1, 0, 0}, {0.9, 0.1, 0}, {0.8, 0.2, 0}}
	for i, name := range names {
		if _, _, err := s.UpsertSkill(ctx, testSkillUpsert(name, "h-"+name)); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		sk, _ := s.GetSkill(ctx, name)
		if err := s.ReplaceSkillEmbeddings(ctx, sk.ID, [][]float32{vecs[i]}); err != nil {
			t.Fatalf("embed %s: %v", name, err)
		}
	}

	got, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, 2, 0)
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	if len(got) != 2 || got[0].Name != "p:tdd" || got[1].Name != "p:debugging" {
		t.Fatalf("limit truncation: %+v", got)
	}
}

// TestReplaceSkillEmbeddingsNilClears guards that passing nil is a legal
// way to wipe a skill's embeddings entirely, not just an incidental no-op:
// the sync engine uses it when a skill's chunks all disappear.
func TestReplaceSkillEmbeddingsNilClears(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	if err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatalf("embed: %v", err)
	}

	if err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, nil); err != nil {
		t.Fatalf("clear with nil: %v", err)
	}

	got, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("expected embeddings cleared: %+v err=%v", got, err)
	}
}

// TestReplaceSkillEmbeddingsKeepsChunkOrder pins the mapping the batched
// insert relies on: chunk_index is the vector's position in the argument
// slice, so the stored order is the caller's order and no vector is dropped
// or duplicated. WITH ORDINALITY is 1-based, so an off-by-one here would
// shift every chunk index by one.
func TestReplaceSkillEmbeddingsKeepsChunkOrder(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	vecs := [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}, {0.5, 0.5, 0}}
	if err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, vecs); err != nil {
		t.Fatalf("embed: %v", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT chunk_index, embedding::text FROM skill_embeddings
		  WHERE skill_id = $1 ORDER BY chunk_index`, tdd.ID)
	if err != nil {
		t.Fatalf("read chunks: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var idx int
		var lit string
		if err := rows.Scan(&idx, &lit); err != nil {
			t.Fatalf("scan chunk: %v", err)
		}
		if idx != len(got) {
			t.Fatalf("chunk_index = %d, want %d", idx, len(got))
		}
		got = append(got, lit)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read chunks: %v", err)
	}
	if len(got) != len(vecs) {
		t.Fatalf("stored %d chunks, want %d", len(got), len(vecs))
	}
	for i, v := range vecs {
		if got[i] != vectorLiteral(v) {
			t.Errorf("chunk %d = %s, want %s", i, got[i], vectorLiteral(v))
		}
	}
}

// TestRecommendSkillsSkipsSkillWithoutEmbeddings pins the inner JOIN:
// a live skill that was never embedded must not appear in results
// (LEFT JOIN would incorrectly surface it with a null score).
func TestRecommendSkillsSkipsSkillWithoutEmbeddings(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed tdd: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	if err := s.ReplaceSkillEmbeddings(ctx, tdd.ID, [][]float32{{1, 0, 0}}); err != nil {
		t.Fatalf("embed tdd: %v", err)
	}
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("debugging", "h2")); err != nil {
		t.Fatalf("seed debugging (never embedded): %v", err)
	}

	got, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0)
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	if len(got) != 1 || got[0].Name != "p:tdd" {
		t.Fatalf("expected only the embedded skill: %+v", got)
	}
}

func TestVectorLiteral(t *testing.T) {
	cases := []struct {
		in   []float32
		want string
	}{
		{[]float32{1, 0.5, -2}, "[1,0.5,-2]"},
		// Pins the format verb and bit size: %g would render 1e-07, and
		// float64 precision would render 0.10000000149011612. pgvector
		// rejects exponent notation.
		{[]float32{0.1, 1e-7}, "[0.1,0.0000001]"},
		{[]float32{0}, "[0]"},
		{[]float32{}, "[]"},
	}
	for _, c := range cases {
		if got := vectorLiteral(c.in); got != c.want {
			t.Fatalf("vectorLiteral(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestClearAllSkillEmbeddings covers the provider-change path: every vector
// in the table is invalidated at once, whatever skill it belongs to.
func TestClearAllSkillEmbeddings(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"tdd", "debugging"} {
		if _, _, err := s.UpsertSkill(ctx, testSkillUpsert(name, "h-"+name)); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		sk, _ := s.GetSkill(ctx, name)
		if err := s.ReplaceSkillEmbeddings(ctx, sk.ID, [][]float32{{1, 0, 0}, {0, 1, 0}}); err != nil {
			t.Fatalf("embed %s: %v", name, err)
		}
	}

	n, err := s.ClearAllSkillEmbeddings(ctx)
	if err != nil || n != 4 {
		t.Fatalf("clear = %d err=%v, want 4", n, err)
	}
	got, err := s.RecommendSkills(ctx, []float32{1, 0, 0}, 5, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("after clear: %+v err=%v", got, err)
	}

	// Clearing an already-empty table is a no-op, not an error.
	n, err = s.ClearAllSkillEmbeddings(ctx)
	if err != nil || n != 0 {
		t.Fatalf("second clear = %d err=%v, want 0", n, err)
	}
}
