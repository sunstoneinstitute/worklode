package indexer

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/embed"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// fakeProvider returns one fixed vector per text. id and vec identify the
// embedding space, so two of them stand in for a provider swap; fails makes
// that many leading calls return an error, standing in for a transient 429.
type fakeProvider struct {
	calls int
	id    string
	vec   []float32
	fails int
}

func (f *fakeProvider) Embed(ctx context.Context, role embed.Role, texts []string) ([][]float32, error) {
	f.calls++
	if role != embed.RoleDocument {
		return nil, errors.New("stored content must be embedded with RoleDocument")
	}
	if f.fails > 0 {
		f.fails--
		return nil, errors.New("embed unavailable")
	}
	v := f.vec
	if v == nil {
		v = []float32{1, 0, 0}
	}
	// index_chunks.embedding is vector(768) (040 §2.2), so a fixture written
	// as {1, 0, 0} has to reach the store at full width.
	v = store.VecForTests(v...)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = v
	}
	return out, nil
}

func (f *fakeProvider) ID() string {
	if f.id == "" {
		return "fake"
	}
	return f.id
}

func (f *fakeProvider) Dim() int { return store.IndexDim }

// seedCorpus creates one doc, one task and one skill — one subject of every
// kind the loop walks — through the same store calls production uses.
func seedCorpus(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.EnsureServiceActor(ctx, "stig", "Stig"); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	// state_log.event_id and docs' provenance are NOT NULL FKs to events, so
	// the seed rows are written under a real event.
	if _, _, err := st.RecordEvent(ctx, "cli", "seed-"+t.Name(), "test.seed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := st.Now()
			if _, err := store.CreateTask(tx, now, store.TaskInput{
				ProjectID: "demo", Title: "Fix the lease sweeper",
				Body: "Leases outlive their worktree.", Priority: "medium", Kind: "bug",
			}, eventID); err != nil {
				return err
			}
			_, err := store.CreateDoc(tx, now, store.DocInput{
				Project: "demo", Kind: "spec", Slug: "leases",
				Body:  "# Leases\n\n## 1 Scope {#sec-1}\n\nA lease binds a task to a worktree.\n",
				Owner: "stig", CreatedBy: "stig",
			}, eventID)
			return err
		}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := st.UpsertSkill(ctx, store.SkillUpsert{
		Qualifier: "p", Name: "tdd", Description: "Red-green-refactor discipline",
		SourceRepo: "acme/p", SourcePath: "skills/tdd", GitCommit: "aaa111",
		ContentHash: "h1", SkillMD: "# TDD\n\nWrite the test first.",
		Frontmatter: []byte(`{"name":"tdd"}`), Archive: []byte("not a real tarball"),
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
}

// chunkStats reads the index the way an operator would: how many rows exist
// and how many carry no vector.
func chunkStats(t *testing.T, st *store.Store) (rows, withoutVector int) {
	t.Helper()
	err := st.DBForTests().QueryRow(
		`SELECT count(*), count(*) FILTER (WHERE embedding IS NULL) FROM index_chunks`).
		Scan(&rows, &withoutVector)
	if err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	return rows, withoutVector
}

func newIndexer(st *store.Store, p embed.Provider) (*Indexer, *Metrics) {
	m := NewMetrics(prometheus.NewRegistry())
	return &Indexer{Store: st, Embed: p, Metrics: m}, m
}

func staleGauge(t *testing.T, m *Metrics) float64 {
	t.Helper()
	var total float64
	for _, kind := range kinds {
		total += testutil.ToFloat64(m.stale.WithLabelValues(kind))
	}
	return total
}

// TestConvergenceIsIdempotent is 040 §13.10: a second pass over an unchanged
// corpus re-embeds nothing and leaves worklode_index_subjects_stale at zero.
func TestConvergenceIsIdempotent(t *testing.T) {
	t.Parallel()
	st := store.OpenTestStore(t)
	seedCorpus(t, st)
	p := &fakeProvider{}
	ix, m := newIndexer(st, p)

	n, err := ix.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if n != 3 {
		t.Fatalf("first pass indexed %d subjects, want 3 (one doc, one task, one skill)", n)
	}
	rows, without := chunkStats(t, st)
	if rows == 0 || without != 0 {
		t.Fatalf("after first pass: %d rows, %d without a vector", rows, without)
	}
	if got := staleGauge(t, m); got != 0 {
		t.Fatalf("worklode_index_subjects_stale = %v after the first pass, want 0", got)
	}

	calls := p.calls
	n, err = ix.RunOnce(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("second pass indexed %d subjects err=%v, want 0", n, err)
	}
	if p.calls != calls {
		t.Fatalf("second pass made %d more embed calls, want 0", p.calls-calls)
	}
	if got := staleGauge(t, m); got != 0 {
		t.Fatalf("worklode_index_subjects_stale = %v after the second pass, want 0", got)
	}
	for _, kind := range kinds {
		if got := testutil.ToFloat64(m.reembed.WithLabelValues(kind, "ok")); got != 1 {
			t.Errorf("reembed{%s,ok} = %v, want 1", kind, got)
		}
		if got := testutil.ToFloat64(m.reembed.WithLabelValues(kind, "error")); got != 0 {
			t.Errorf("reembed{%s,error} = %v, want 0", kind, got)
		}
	}
}

// TestProviderChangeKeepsLexicalRows is 040 §13.7 and §8: swapping the
// provider nulls every vector but keeps the rows, so the lexical arm never
// stops serving, and the next pass rebuilds the vectors.
func TestProviderChangeKeepsLexicalRows(t *testing.T) {
	t.Parallel()
	st := store.OpenTestStore(t)
	ctx := context.Background()
	seedCorpus(t, st)

	first := &fakeProvider{id: "fake:a", vec: []float32{1, 0, 0}}
	if err := InvalidateOnProviderChange(ctx, st, first, nil); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	ix, _ := newIndexer(st, first)
	if _, err := ix.RunOnce(ctx); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	rows, without := chunkStats(t, st)
	if rows == 0 || without != 0 {
		t.Fatalf("after first pass: %d rows, %d without a vector", rows, without)
	}

	// A different model: same text, incomparable space.
	second := &fakeProvider{id: "fake:b", vec: []float32{0, 1, 0}}
	if err := InvalidateOnProviderChange(ctx, st, second, nil); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	got, gotWithout := chunkStats(t, st)
	if got != rows {
		t.Fatalf("invalidation deleted rows: %d, want the original %d", got, rows)
	}
	if gotWithout != rows {
		t.Fatalf("%d of %d rows still carry a vector after invalidation", rows-gotWithout, rows)
	}
	// The lexical arm's inputs survive: text and its tsv are provider-
	// independent, which is what "degrades to lexical-only, not to nothing"
	// means.
	var lexical int
	if err := st.DBForTests().QueryRow(
		`SELECT count(*) FROM index_chunks WHERE tsv @@ websearch_to_tsquery('simple', 'lease')`).
		Scan(&lexical); err != nil {
		t.Fatalf("lexical query: %v", err)
	}
	if lexical == 0 {
		t.Fatal("no chunk matches 'lease' lexically after invalidation")
	}
	if id, err := st.EmbeddingProviderID(ctx); err != nil || id != "fake:b" {
		t.Fatalf("provider id = %q err=%v, want fake:b", id, err)
	}

	// The next pass rebuilds them, with no content change anywhere.
	ix, _ = newIndexer(st, second)
	if _, err := ix.RunOnce(ctx); err != nil {
		t.Fatalf("re-embed pass: %v", err)
	}
	if _, without = chunkStats(t, st); without != 0 {
		t.Fatalf("%d rows still have no vector after the re-embed pass", without)
	}
}

// TestNoProviderWritesLexicalRows is 040 §13.8 and §11: an instance with no
// provider still indexes the whole corpus, with null embeddings.
func TestNoProviderWritesLexicalRows(t *testing.T) {
	t.Parallel()
	st := store.OpenTestStore(t)
	seedCorpus(t, st)
	ix, m := newIndexer(st, nil)

	n, err := ix.RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("pass indexed %d subjects err=%v, want 3", n, err)
	}
	rows, without := chunkStats(t, st)
	if rows == 0 || without != rows {
		t.Fatalf("want every one of %d rows without a vector, got %d", rows, without)
	}
	if got := staleGauge(t, m); got != 0 {
		t.Fatalf("stale = %v with no provider, want 0: a null vector is only work when a provider exists", got)
	}
	// A second pass must not churn: with no provider, a null-vector row is
	// finished work, not a backlog.
	if n, err := ix.RunOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("second pass indexed %d subjects err=%v, want 0", n, err)
	}
	if got := testutil.ToFloat64(m.withoutVector); got != float64(rows) {
		t.Fatalf("worklode_index_chunks_without_vector = %v, want %d", got, rows)
	}
}

// TestFailedSubjectStaysStale: a provider that errors leaves the subject
// unindexed and the pass alive, and the next pass picks it up. This is the
// self-healing property §7 buys by converging rather than hooking writes.
func TestFailedSubjectStaysStale(t *testing.T) {
	t.Parallel()
	st := store.OpenTestStore(t)
	seedCorpus(t, st)
	// Three subjects, three embed calls: fail them all, then recover.
	p := &fakeProvider{fails: 3}
	ix, m := newIndexer(st, p)

	n, err := ix.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("failing pass returned an error: %v", err)
	}
	if n != 0 {
		t.Fatalf("failing pass indexed %d subjects, want 0", n)
	}
	if rows, _ := chunkStats(t, st); rows != 0 {
		t.Fatalf("failing pass wrote %d rows", rows)
	}
	if got := staleGauge(t, m); got != 3 {
		t.Fatalf("worklode_index_subjects_stale = %v after a failed pass, want 3", got)
	}
	for _, kind := range kinds {
		if got := testutil.ToFloat64(m.reembed.WithLabelValues(kind, "error")); got != 1 {
			t.Errorf("reembed{%s,error} = %v, want 1", kind, got)
		}
	}

	if n, err = ix.RunOnce(context.Background()); err != nil || n != 3 {
		t.Fatalf("recovery pass indexed %d subjects err=%v, want 3", n, err)
	}
	if got := staleGauge(t, m); got != 0 {
		t.Fatalf("stale = %v after recovery, want 0", got)
	}
}

// TestLoopStopsOnContextCancel: the loop is the caller's to shut down, and a
// cancelled BackgroundCtx must end it rather than leave a goroutine querying
// a closing database.
func TestLoopStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	st := store.OpenTestStore(t)
	seedCorpus(t, st)
	ix, _ := newIndexer(st, &fakeProvider{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ix.Loop(ctx, time.Hour); close(done) }()

	// The boot pass runs before the first tick, so cancelling immediately
	// still leaves an indexed corpus behind.
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Loop did not return after its context was cancelled")
	}
}
