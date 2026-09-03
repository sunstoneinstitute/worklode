package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/corpusindex"
)

// vec is VecForTests under a shorter name, since every fixture here needs it.
func vec(vals ...float32) []float32 { return VecForTests(vals...) }

func TestRecommendSkillsOverIndexChunks(t *testing.T) {
	t.Parallel()
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
	if err := s.SeedSkillChunksForTests(ctx, tdd.ID, [][]float32{vec(1, 0, 0), vec(0.9, 0.1, 0)}); err != nil {
		t.Fatalf("embed tdd: %v", err)
	}
	if err := s.SeedSkillChunksForTests(ctx, dbg.ID, [][]float32{vec(0, 1, 0)}); err != nil {
		t.Fatalf("embed dbg: %v", err)
	}

	got, err := s.RecommendSkills(ctx, vec(1, 0, 0), 5, 0.5)
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	if len(got) != 1 || got[0].Name != "p:tdd" || got[0].Score < 0.99 {
		t.Fatalf("recommend: %+v", got)
	}

	// Floor at 0 returns both, best-first, max over chunks.
	got, err = s.RecommendSkills(ctx, vec(1, 0, 0), 5, 0)
	if err != nil || len(got) != 2 || got[0].Name != "p:tdd" {
		t.Fatalf("recommend all: %+v err=%v", got, err)
	}

	// Replace wipes old chunks.
	if err := s.SeedSkillChunksForTests(ctx, tdd.ID, [][]float32{vec(0, 0, 1)}); err != nil {
		t.Fatalf("re-embed: %v", err)
	}
	got, _ = s.RecommendSkills(ctx, vec(1, 0, 0), 5, 0.5)
	if len(got) != 0 {
		t.Fatalf("after replace: %+v", got)
	}

	// Soft-deleted skills are never recommended.
	if err := s.SeedSkillChunksForTests(ctx, dbg.ID, [][]float32{vec(1, 0, 0)}); err != nil {
		t.Fatalf("re-embed dbg: %v", err)
	}
	if _, err := s.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ = s.RecommendSkills(ctx, vec(1, 0, 0), 5, 0.5)
	if len(got) != 0 {
		t.Fatalf("deleted still recommended: %+v", got)
	}
}

// TestReplaceSubjectChunksWrongWidth is spec 040 §13.5: the vector(768)
// typmod is what refuses a wrong-shaped vector, not a Go length check. 016
// had no typmod and needed the guard in the store; 0061 moved the invariant
// into the column, so this asserts Postgres does the refusing.
func TestReplaceSubjectChunksWrongWidth(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")

	for _, width := range []int{767, 769} {
		v := make([]float32, width)
		v[0] = 1
		err := s.SeedSkillChunksForTests(ctx, tdd.ID, [][]float32{v})
		if err == nil {
			t.Fatalf("width %d: want a Postgres error, got nil", width)
		}
		if errors.Is(err, ErrInvalidInput) {
			t.Fatalf("width %d: refused by Go, want refused by Postgres: %v", width, err)
		}
		if !strings.Contains(err.Error(), "dimension") {
			t.Fatalf("width %d: want a pgvector dimension error, got %v", width, err)
		}
	}

	// Nothing was written, and the correct width still is.
	if err := s.SeedSkillChunksForTests(ctx, tdd.ID, [][]float32{vec(1, 0, 0)}); err != nil {
		t.Fatalf("768-wide vector rejected: %v", err)
	}
}

// TestReplaceSubjectChunksZeroNorm guards against a NaN cosine score:
// pgvector's <=> returns NaN against an all-zero vector, and NaN sorts
// above every real score in Postgres, so one degenerate chunk would rank
// first for every query forever. No column constraint catches this, so the
// store still does.
func TestReplaceSubjectChunksZeroNorm(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")

	err := s.SeedSkillChunksForTests(ctx, tdd.ID, [][]float32{vec(1, 0, 0), vec()})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero-norm vector: want ErrInvalidInput, got %v", err)
	}

	got, err := s.RecommendSkills(ctx, vec(1, 0, 0), 5, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("expected no embeddings written: %+v err=%v", got, err)
	}
}

// TestRecommendSkillsZeroNormQuery guards the same NaN failure mode from
// the query side: a zero query vector would otherwise score every skill
// NaN and return the whole corpus in arbitrary order.
func TestRecommendSkillsZeroNormQuery(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	if err := s.SeedSkillChunksForTests(ctx, tdd.ID, [][]float32{vec(1, 0, 0)}); err != nil {
		t.Fatalf("embed: %v", err)
	}

	if _, err := s.RecommendSkills(ctx, vec(), 5, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero-norm query: want ErrInvalidInput, got %v", err)
	}
	if _, err := s.RecommendSkills(ctx, nil, 5, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero-length query: want ErrInvalidInput, got %v", err)
	}
}

// TestRecommendSkillsInvalidLimit guards limit<=0, which would otherwise
// reach Postgres as a raw, unclassified "LIMIT must not be negative" error.
func TestRecommendSkillsInvalidLimit(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, err := s.RecommendSkills(ctx, vec(1, 0, 0), 0, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit=0: want ErrInvalidInput, got %v", err)
	}
	if _, err := s.RecommendSkills(ctx, vec(1, 0, 0), -1, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit=-1: want ErrInvalidInput, got %v", err)
	}
}

// TestRecommendSkillsLimitTruncates guards that limit is actually passed
// through to the query, not just accepted: with three matching skills and
// limit=2, only the two best-scoring should come back.
func TestRecommendSkillsLimitTruncates(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	names := []string{"tdd", "debugging", "diagnose"}
	vecs := [][]float32{vec(1, 0, 0), vec(0.9, 0.1, 0), vec(0.8, 0.2, 0)}
	for i, name := range names {
		if _, _, err := s.UpsertSkill(ctx, testSkillUpsert(name, "h-"+name)); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		sk, _ := s.GetSkill(ctx, name)
		if err := s.SeedSkillChunksForTests(ctx, sk.ID, [][]float32{vecs[i]}); err != nil {
			t.Fatalf("embed %s: %v", name, err)
		}
	}

	got, err := s.RecommendSkills(ctx, vec(1, 0, 0), 2, 0)
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	if len(got) != 2 || got[0].Name != "p:tdd" || got[1].Name != "p:debugging" {
		t.Fatalf("limit truncation: %+v", got)
	}
}

// TestReplaceSubjectChunksEmptyClears guards that passing no chunks is a
// legal way to wipe a subject's rows entirely, not just an incidental no-op:
// skillsync uses it to drop the old vectors before a re-embed.
func TestReplaceSubjectChunksEmptyClears(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	if err := s.SeedSkillChunksForTests(ctx, tdd.ID, [][]float32{vec(1, 0, 0)}); err != nil {
		t.Fatalf("embed: %v", err)
	}

	if err := s.SeedSkillChunksForTests(ctx, tdd.ID, nil); err != nil {
		t.Fatalf("clear with nil: %v", err)
	}

	got, err := s.RecommendSkills(ctx, vec(1, 0, 0), 5, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("expected embeddings cleared: %+v err=%v", got, err)
	}
}

// TestReplaceSubjectChunksNilVectors is 040 §11: an instance with no
// embedding provider still writes chunk rows, text and all, with a null
// embedding. Those rows must be invisible to the dense arm and present in
// the table.
func TestReplaceSubjectChunksNilVectors(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	chunks := []corpusindex.Chunk{{Index: 0, Header: "skill: tdd", Text: "write the test first"}}
	subj := ChunkSubject{Kind: SubjectSkill, SkillID: tdd.ID, ContentHash: "h1"}
	if err := s.ReplaceSubjectChunks(ctx, subj, chunks, nil); err != nil {
		t.Fatalf("replace without vectors: %v", err)
	}

	counts, err := s.IndexCounts(ctx)
	if err != nil {
		t.Fatalf("index counts: %v", err)
	}
	if counts.ByKind[SubjectSkill] != 1 || counts.WithoutVector != 1 {
		t.Fatalf("counts = %+v, want 1 skill chunk with no vector", counts)
	}

	got, err := s.RecommendSkills(ctx, vec(1, 0, 0), 5, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("null-vector rows must not reach the dense arm: %+v err=%v", got, err)
	}
}

// TestReplaceSubjectChunksKeepsChunkOrder pins the mapping the batched
// insert relies on: chunk_index comes from the Chunk, and the vector at the
// same slice position is the one stored with it, so nothing is dropped,
// duplicated, or shifted.
func TestReplaceSubjectChunksKeepsChunkOrder(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	vecs := [][]float32{vec(1, 0, 0), vec(0, 1, 0), vec(0, 0, 1), vec(0.5, 0.5, 0)}
	if err := s.SeedSkillChunksForTests(ctx, tdd.ID, vecs); err != nil {
		t.Fatalf("embed: %v", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT chunk_index, embedding::text FROM index_chunks
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
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed tdd: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")
	if err := s.SeedSkillChunksForTests(ctx, tdd.ID, [][]float32{vec(1, 0, 0)}); err != nil {
		t.Fatalf("embed tdd: %v", err)
	}
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("debugging", "h2")); err != nil {
		t.Fatalf("seed debugging (never embedded): %v", err)
	}

	got, err := s.RecommendSkills(ctx, vec(1, 0, 0), 5, 0)
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	if len(got) != 1 || got[0].Name != "p:tdd" {
		t.Fatalf("expected only the embedded skill: %+v", got)
	}
}

func TestVectorLiteral(t *testing.T) {
	t.Parallel()
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

// TestClearAllChunkVectors covers the provider-change path (040 §8): every
// vector is invalidated at once, and the rows survive so the lexical arm
// keeps serving while the next convergence pass rebuilds them.
func TestClearAllChunkVectors(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"tdd", "debugging"} {
		if _, _, err := s.UpsertSkill(ctx, testSkillUpsert(name, "h-"+name)); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		sk, _ := s.GetSkill(ctx, name)
		if err := s.SeedSkillChunksForTests(ctx, sk.ID, [][]float32{vec(1, 0, 0), vec(0, 1, 0)}); err != nil {
			t.Fatalf("embed %s: %v", name, err)
		}
	}

	n, err := s.ClearAllChunkVectors(ctx)
	if err != nil || n != 4 {
		t.Fatalf("clear = %d err=%v, want 4", n, err)
	}
	got, err := s.RecommendSkills(ctx, vec(1, 0, 0), 5, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("after clear: %+v err=%v", got, err)
	}

	// The rows themselves are still there, text intact.
	counts, err := s.IndexCounts(ctx)
	if err != nil || counts.ByKind[SubjectSkill] != 4 || counts.WithoutVector != 4 {
		t.Fatalf("after clear counts = %+v err=%v, want 4 vectorless skill chunks", counts, err)
	}

	// Clearing an already-cleared table is a no-op, not an error.
	n, err = s.ClearAllChunkVectors(ctx)
	if err != nil || n != 0 {
		t.Fatalf("second clear = %d err=%v, want 0", n, err)
	}
}

// TestIndexChunksCascadeOnTaskDelete pins the FK cascade that lets the index
// carry no tombstones (040 §5): dropping a subject drops its chunks.
func TestIndexChunksCascadeOnTaskDelete(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if err := seedTask(t, s, "WL-1"); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	subj := ChunkSubject{Kind: SubjectTask, TaskID: "WL-1", Project: "p", ContentHash: "h1"}
	chunks := []corpusindex.Chunk{{Index: 0, Header: "WL-1 [feature/ready] T", Text: "body"}}
	if err := s.ReplaceSubjectChunks(ctx, subj, chunks, [][]float32{vec(1, 0, 0)}); err != nil {
		t.Fatalf("index task: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = 'WL-1'`); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	counts, err := s.IndexCounts(ctx)
	if err != nil {
		t.Fatalf("index counts: %v", err)
	}
	if counts.ByKind[SubjectTask] != 0 {
		t.Fatalf("chunks survived the task: %+v", counts)
	}
}

// TestIndexChunksOneSubjectCheck pins index_chunks_one_subject: a row naming
// two subjects, or none, is refused by the constraint rather than by
// whatever reads it later.
func TestIndexChunksOneSubjectCheck(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if err := seedTask(t, s, "WL-1"); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")

	insert := `INSERT INTO index_chunks
	    (subject_kind, task_id, skill_id, anchor, chunk_index, chunk_text, content_hash, indexed_at)
	 VALUES ('task', $1, $2, '', 0, 'x', 'h', now())`
	if _, err := s.db.ExecContext(ctx, insert, "WL-1", tdd.ID); err == nil {
		t.Fatal("two subjects: want a CHECK violation, got nil")
	} else if !strings.Contains(err.Error(), "index_chunks_one_subject") {
		t.Fatalf("two subjects: want index_chunks_one_subject, got %v", err)
	}
	// A row naming no subject breaks both CHECKs at once — the kind matches
	// nothing either — and Postgres reports whichever it evaluated first.
	if _, err := s.db.ExecContext(ctx, insert, nil, nil); err == nil {
		t.Fatal("no subject: want a CHECK violation, got nil")
	} else if !strings.Contains(err.Error(), "index_chunks_one_subject") &&
		!strings.Contains(err.Error(), "index_chunks_kind_matches_subject") {
		t.Fatalf("no subject: want an index_chunks CHECK violation, got %v", err)
	}
}

// TestStaleSubjectsConverges is 040 §13.10: a never-indexed subject is
// stale, an edited one goes stale again, and a second pass over an unchanged
// corpus finds nothing.
func TestStaleSubjectsConverges(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if err := seedTask(t, s, "WL-1"); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// Never indexed: stale, with no chunk rows at all.
	stale, err := s.StaleSubjects(ctx, SubjectTask, 10, false)
	if err != nil {
		t.Fatalf("stale: %v", err)
	}
	if len(stale) != 1 || stale[0].TaskID != "WL-1" || stale[0].ContentHash == "" {
		t.Fatalf("never-indexed task: %+v", stale)
	}

	index := func(subj ChunkSubject) {
		t.Helper()
		chunks := []corpusindex.Chunk{{Index: 0, Text: "body"}}
		if err := s.ReplaceSubjectChunks(ctx, subj, chunks, nil); err != nil {
			t.Fatalf("index %s: %v", subj.TaskID, err)
		}
	}
	index(stale[0])

	// Second pass over an unchanged corpus: nothing.
	if stale, err = s.StaleSubjects(ctx, SubjectTask, 10, false); err != nil || len(stale) != 0 {
		t.Fatalf("second pass: %+v err=%v", stale, err)
	}

	// An edit makes it stale again, with a different hash.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET title = 'edited' WHERE id = 'WL-1'`); err != nil {
		t.Fatalf("edit task: %v", err)
	}
	stale, err = s.StaleSubjects(ctx, SubjectTask, 10, false)
	if err != nil || len(stale) != 1 {
		t.Fatalf("after edit: %+v err=%v", stale, err)
	}
	index(stale[0])
	if stale, err = s.StaleSubjects(ctx, SubjectTask, 10, false); err != nil || len(stale) != 0 {
		t.Fatalf("after re-index: %+v err=%v", stale, err)
	}
}

// TestStaleSubjectsNeedVectors is the other half of 040 §8: invalidation
// nulls vectors without touching the text or its hash, so a row with no
// vector has to count as needing work — but only on an instance that has a
// provider to compute one with (§11).
func TestStaleSubjectsNeedVectors(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if err := seedTask(t, s, "WL-1"); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	stale, err := s.StaleSubjects(ctx, SubjectTask, 10, true)
	if err != nil || len(stale) != 1 {
		t.Fatalf("never-indexed task: %+v err=%v", stale, err)
	}
	// Indexed with a vector: not stale either way.
	chunks := []corpusindex.Chunk{{Index: 0, Text: "body"}}
	if err := s.ReplaceSubjectChunks(ctx, stale[0], chunks, [][]float32{vec(1, 0, 0)}); err != nil {
		t.Fatalf("index: %v", err)
	}
	for _, needVectors := range []bool{false, true} {
		if got, err := s.StaleSubjects(ctx, SubjectTask, 10, needVectors); err != nil || len(got) != 0 {
			t.Fatalf("indexed with a vector, needVectors=%v: %+v err=%v", needVectors, got, err)
		}
	}

	if _, err := s.ClearAllChunkVectors(ctx); err != nil {
		t.Fatalf("clear vectors: %v", err)
	}
	if got, err := s.StaleSubjects(ctx, SubjectTask, 10, false); err != nil || len(got) != 0 {
		t.Fatalf("no provider: a null vector is finished work, got %+v err=%v", got, err)
	}
	got, err := s.StaleSubjects(ctx, SubjectTask, 10, true)
	if err != nil || len(got) != 1 || got[0].TaskID != "WL-1" {
		t.Fatalf("with a provider: want WL-1 back for re-embedding, got %+v err=%v", got, err)
	}
}

// TestStaleSubjectsSkills covers the skill arm: skill_versions.content_hash
// is the live hash, and a soft-deleted skill is never stale (040 §1) — its
// chunk rows are deleted by the caller, not filtered at query time.
func TestStaleSubjectsSkills(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, _, err := s.UpsertSkill(ctx, testSkillUpsert("tdd", "h1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tdd, _ := s.GetSkill(ctx, "tdd")

	stale, err := s.StaleSubjects(ctx, SubjectSkill, 10, false)
	if err != nil || len(stale) != 1 || stale[0].SkillID != tdd.ID || stale[0].ContentHash != "h1" {
		t.Fatalf("never-indexed skill: %+v err=%v", stale, err)
	}
	if stale[0].Project != "" {
		t.Fatalf("skills are org-wide, want no project: %q", stale[0].Project)
	}

	if err := s.ReplaceSubjectChunks(ctx, stale[0],
		[]corpusindex.Chunk{{Index: 0, Text: "body"}}, nil); err != nil {
		t.Fatalf("index skill: %v", err)
	}
	if stale, err = s.StaleSubjects(ctx, SubjectSkill, 10, false); err != nil || len(stale) != 0 {
		t.Fatalf("second pass: %+v err=%v", stale, err)
	}

	if _, err := s.SoftDeleteSkillsExcept(ctx, "acme/claude-plugins", nil); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if stale, err = s.StaleSubjects(ctx, SubjectSkill, 10, false); err != nil || len(stale) != 0 {
		t.Fatalf("soft-deleted skill is stale: %+v err=%v", stale, err)
	}
}

// TestStaleSubjectsRejectsUnknownKind keeps a typo'd kind from silently
// returning an empty set, which would read as "nothing to converge".
func TestStaleSubjectsRejectsUnknownKind(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()

	if _, err := s.StaleSubjects(ctx, "event", 10, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown kind: want ErrInvalidInput, got %v", err)
	}
	if _, err := s.StaleSubjects(ctx, SubjectTask, 0, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("limit=0: want ErrInvalidInput, got %v", err)
	}
}
