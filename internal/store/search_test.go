package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/corpusindex"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// fixtureChunk is one chunk of a fixture subject. A nil vec indexes the text
// for the lexical arm only, the way a no-provider instance writes it (§11).
type fixtureChunk struct {
	anchor string
	header string
	text   string
	vec    []float32
}

// seedChunks writes one subject's chunks. chunk_index runs per anchor, as
// 040 §4.2 requires.
func seedChunks(t *testing.T, s *Store, subj ChunkSubject, cs ...fixtureChunk) {
	t.Helper()
	var (
		chunks = make([]corpusindex.Chunk, len(cs))
		vecs   [][]float32
		perAnc = map[string]int{}
	)
	for i, c := range cs {
		chunks[i] = corpusindex.Chunk{Anchor: c.anchor, Index: perAnc[c.anchor], Header: c.header, Text: c.text}
		perAnc[c.anchor]++
		if c.vec != nil {
			vecs = append(vecs, c.vec)
		}
	}
	if len(vecs) > 0 && len(vecs) != len(cs) {
		t.Fatalf("seedChunks: %d vectors for %d chunks", len(vecs), len(cs))
	}
	if subj.ContentHash == "" {
		subj.ContentHash = "seed"
	}
	if err := s.ReplaceSubjectChunks(context.Background(), subj, chunks, vecs); err != nil {
		t.Fatalf("seed %s chunks: %v", subj.Kind, err)
	}
}

// seedSearchTask inserts a task to hang chunks on.
func seedSearchTask(t *testing.T, s *Store, id, title string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO tasks (id, project_id, title, priority, kind, state, created_at, updated_at)
		 VALUES ($1, 'p1', $2, 'medium', 'feature', 'ready', now(), now())`,
		id, title); err != nil {
		t.Fatal(err)
	}
}

// seedSearchDoc creates a spec numbered n whose body carries title.
func seedSearchDoc(t *testing.T, s *Store, n int, title string) int64 {
	t.Helper()
	body := fmt.Sprintf("---\nstatus: draft\n---\n\n# %s\n\n## 1. Scope {#sec-1}\n\nscope\n", title)
	d := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: n,
		Slug: fmt.Sprintf("%03d-%d", n, n), Body: body, CreatedBy: "stig",
	})
	return d.ID
}

// seedSearchSkill upserts a skill and returns its id.
func seedSearchSkill(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	if _, _, err := s.UpsertSkill(context.Background(), testSkillUpsert(name, "h-"+name)); err != nil {
		t.Fatalf("upsert skill %s: %v", name, err)
	}
	sk, err := s.GetSkill(context.Background(), name)
	if err != nil {
		t.Fatalf("get skill %s: %v", name, err)
	}
	return sk.ID
}

// TestSearchFusesArmRankings is 040 §6.3's worked example, and the §13.2
// acceptance case with it. The dense arm puts the section that defines
// `child_of` third; the lexical arm puts it first; fusion has to put it
// first. The mode=dense half is the "and the test fails when the lexical arm
// is disabled" clause: the same assertion run over one arm does not hold.
func TestSearchFusesArmRankings(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	defines := seedSearchDoc(t, s, 4, "Execution backbone")
	accept := seedSearchDoc(t, s, 25, "Documents in the backbone")
	seedSearchTask(t, s, "P1-142", "Child ordering")
	deploy := seedSearchSkill(t, s, "deploying")

	// Vectors are the dense ranking the spec's table states: accept 1.0,
	// the task ~0.99, defines ~0.71, the skill 0 (below the 0.35 floor).
	seedChunks(t, s, ChunkSubject{Kind: SubjectDoc, DocID: defines, Project: "p1"},
		fixtureChunk{anchor: "sec-6.1", header: "6.1 Edges",
			text: "A child_of edge orders the work under a parent.", vec: vec(0.5, 0.5, 0)})
	seedChunks(t, s, ChunkSubject{Kind: SubjectDoc, DocID: accept, Project: "p1"},
		fixtureChunk{anchor: "sec-9.2", header: "9.2 Plan acceptance",
			text: "Accepting a plan mints one task per plan item.", vec: vec(1, 0, 0)})
	seedChunks(t, s, ChunkSubject{Kind: SubjectTask, TaskID: "P1-142", Project: "p1"},
		fixtureChunk{text: "Order the children of a task by rank.", vec: vec(0.9, 0.1, 0)})
	seedChunks(t, s, ChunkSubject{Kind: SubjectSkill, SkillID: deploy},
		fixtureChunk{text: "How to roll out a release.", vec: vec(0, 1, 0)})

	q := SearchQuery{Text: "child_of", Vector: vec(1, 0, 0), Limit: 10}
	hits, err := s.Search(t.Context(), q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("want 3 hits (the skill is below the dense floor and matches no term), got %d: %+v", len(hits), hits)
	}
	top := hits[0]
	if top.Kind != SubjectDoc || top.DocID != defines || top.Anchor != "sec-6.1" {
		t.Fatalf("fusion did not rank the child_of section first: %+v", top)
	}
	if top.DenseRank != 3 || top.LexicalRank != 1 {
		t.Fatalf("arm ranks: want dense 3 lexical 1, got dense %d lexical %d", top.DenseRank, top.LexicalRank)
	}
	// 1/(60+3) + 1/(60+1), the spec's 0.03227.
	if want := 1.0/63 + 1.0/61; math.Abs(top.Score-want) > 1e-9 {
		t.Fatalf("fused score: want %v, got %v", want, top.Score)
	}
	if top.Title != "Execution backbone" {
		t.Fatalf("title: %q", top.Title)
	}
	if top.Excerpt != "A child_of edge orders the work under a parent." {
		t.Fatalf("excerpt is not the chunk text: %q", top.Excerpt)
	}
	// The two dense-only subjects keep the dense arm's order behind it, and
	// report no lexical rank at all.
	if hits[1].DocID != accept || hits[1].DenseRank != 1 || hits[1].LexicalRank != 0 {
		t.Fatalf("second hit: %+v", hits[1])
	}
	if hits[2].TaskID != "P1-142" || hits[2].DenseRank != 2 || hits[2].LexicalRank != 0 {
		t.Fatalf("third hit: %+v", hits[2])
	}

	// §13.2: without the lexical arm the same assertion fails — the dense arm
	// alone leaves the correct answer third.
	q.Mode = model.SearchDense
	denseOnly, err := s.Search(t.Context(), q)
	if err != nil {
		t.Fatalf("dense search: %v", err)
	}
	if len(denseOnly) != 3 || denseOnly[0].DocID != accept || denseOnly[2].DocID != defines {
		t.Fatalf("dense-only ranking: %+v", denseOnly)
	}

	// The lexical arm on its own returns the one subject that literally
	// contains the identifier, which is what fusion promoted.
	q.Mode = model.SearchLexical
	lex, err := s.Search(t.Context(), q)
	if err != nil {
		t.Fatalf("lexical search: %v", err)
	}
	if len(lex) != 1 || lex[0].DocID != defines || lex[0].DenseRank != 0 || lex[0].LexicalRank != 1 {
		t.Fatalf("lexical-only ranking: %+v", lex)
	}
}

// TestSearchPoolsPerSubjectBeforeRanking is 040 §13.3: a long document with
// many mediocre chunks must not outrank a short exact match. It can only hold
// because each arm max-pools per subject before it ranks — fusing chunk
// rankings would give the long document eight shares of the score.
func TestSearchPoolsPerSubjectBeforeRanking(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	long := seedSearchDoc(t, s, 1, "The long one")
	short := seedSearchDoc(t, s, 2, "The short one")

	mediocre := make([]fixtureChunk, 8)
	for i := range mediocre {
		mediocre[i] = fixtureChunk{
			anchor: fmt.Sprintf("sec-%d", i+1),
			text:   fmt.Sprintf("Section %d says something roughly about leases.", i+1),
			vec:    vec(0.8, 0.6, 0),
		}
	}
	seedChunks(t, s, ChunkSubject{Kind: SubjectDoc, DocID: long, Project: "p1"}, mediocre...)
	seedChunks(t, s, ChunkSubject{Kind: SubjectDoc, DocID: short, Project: "p1"},
		fixtureChunk{anchor: "sec-1", text: "A lease expires after two hours.", vec: vec(1, 0, 0)})

	hits, err := s.Search(t.Context(), SearchQuery{
		Text: "lease", Vector: vec(1, 0, 0), Mode: model.SearchDense, Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// Nine chunks, nine ranked rows — one per (doc, anchor) subject, and the
	// short document's single one first.
	if len(hits) != 9 {
		t.Fatalf("want one row per subject section, got %d: %+v", len(hits), hits)
	}
	if hits[0].DocID != short {
		t.Fatalf("the long document outranked the exact match: %+v", hits[0])
	}

	// Hybrid says the same, with the lexical arm agreeing: the long
	// document's eight sections cannot outvote the one that answers.
	hits, err = s.Search(t.Context(), SearchQuery{
		Text: "lease", Vector: vec(1, 0, 0), Limit: 20})
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	if hits[0].DocID != short {
		t.Fatalf("hybrid: the long document outranked the exact match: %+v", hits[0])
	}
}

// TestSearchLexicalConfigIsSimple is 040 §13.6. Under `english`, `child_of`
// stems to `child` and the query matches prose reading "the child task of a
// parent". Under `simple` it does not, and it does match a chunk containing
// the identifier. This test is what stops someone "fixing" the configuration.
func TestSearchLexicalConfigIsSimple(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	seedSearchTask(t, s, "P1-1", "Prose")
	seedSearchTask(t, s, "P1-2", "Identifier")
	seedChunks(t, s, ChunkSubject{Kind: SubjectTask, TaskID: "P1-1", Project: "p1"},
		fixtureChunk{text: "The child task of a parent inherits its project."})
	seedChunks(t, s, ChunkSubject{Kind: SubjectTask, TaskID: "P1-2", Project: "p1"},
		fixtureChunk{text: "The child_of edge is what orders them."})

	hits, err := s.Search(t.Context(), SearchQuery{
		Text: "child_of", Mode: model.SearchLexical, Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].TaskID != "P1-2" {
		t.Fatalf("child_of matched prose about children: %+v", hits)
	}
}

// TestSearchHeaderOutranksBody pins the setweight pair in 040 §5: the context
// header is weight A and the chunk body weight B, so under ts_rank_cd a term
// in the header ranks above the same term in a body.
func TestSearchHeaderOutranksBody(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	seedSearchTask(t, s, "P1-1", "In the header")
	seedSearchTask(t, s, "P1-2", "In the body")
	seedChunks(t, s, ChunkSubject{Kind: SubjectTask, TaskID: "P1-1", Project: "p1"},
		fixtureChunk{header: "Worktree pruning", text: "Some words that do not name it."})
	seedChunks(t, s, ChunkSubject{Kind: SubjectTask, TaskID: "P1-2", Project: "p1"},
		fixtureChunk{header: "Some words", text: "This one mentions pruning in its body."})

	hits, err := s.Search(t.Context(), SearchQuery{
		Text: "pruning", Mode: model.SearchLexical, Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want both subjects, got %+v", hits)
	}
	if hits[0].TaskID != "P1-1" {
		t.Fatalf("a body match outranked a header match: %+v", hits)
	}
	// The excerpt is the chunk text, never the header the match came from.
	if hits[0].Excerpt != "Some words that do not name it." {
		t.Fatalf("excerpt: %q", hits[0].Excerpt)
	}
}

// TestSearchFilters covers 040 §6.4: kind and project narrow both arms, and
// the project filter keeps chunks carrying no project — which is how the
// org-wide skill registry stays visible from inside a project-scoped search.
func TestSearchFilters(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	if _, err := s.db.ExecContext(t.Context(),
		`INSERT INTO projects (id, name, key) VALUES ('p2','P2','P2')`); err != nil {
		t.Fatal(err)
	}

	mine := seedSearchDoc(t, s, 1, "Mine")
	seedSearchTask(t, s, "P1-1", "My task")
	skill := seedSearchSkill(t, s, "tdd")
	other := mustCreateDoc(t, s, DocInput{
		Project: "p2", Kind: "spec", Number: 1, Slug: "001-theirs",
		Body: "---\nstatus: draft\n---\n\n# Theirs\n\n## 1. Scope {#sec-1}\n\nscope\n", CreatedBy: "stig",
	})

	const text = "leases and pruning"
	seedChunks(t, s, ChunkSubject{Kind: SubjectDoc, DocID: mine, Project: "p1"}, fixtureChunk{anchor: "sec-1", text: text})
	seedChunks(t, s, ChunkSubject{Kind: SubjectTask, TaskID: "P1-1", Project: "p1"}, fixtureChunk{text: text})
	seedChunks(t, s, ChunkSubject{Kind: SubjectSkill, SkillID: skill}, fixtureChunk{text: text})
	seedChunks(t, s, ChunkSubject{Kind: SubjectDoc, DocID: other.ID, Project: "p2"}, fixtureChunk{anchor: "sec-1", text: text})

	scoped, err := s.Search(t.Context(), SearchQuery{Text: "pruning", Project: "p1", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(scoped) != 3 {
		t.Fatalf("want this project's two subjects plus the org-wide skill, got %+v", scoped)
	}
	for _, h := range scoped {
		if h.DocID == other.ID {
			t.Fatalf("another project's document leaked into a scoped search: %+v", h)
		}
	}
	var sawSkill bool
	for _, h := range scoped {
		sawSkill = sawSkill || h.Kind == SubjectSkill
	}
	if !sawSkill {
		t.Fatalf("the org-wide skill is invisible from inside a project: %+v", scoped)
	}

	byKind, err := s.Search(t.Context(), SearchQuery{
		Text: "pruning", Project: "p1", Kinds: []string{SubjectSkill}, Limit: 10})
	if err != nil {
		t.Fatalf("kind-filtered search: %v", err)
	}
	if len(byKind) != 1 || byKind[0].Kind != SubjectSkill || byKind[0].SkillID != skill {
		t.Fatalf("kind filter: %+v", byKind)
	}
	if byKind[0].Title != "p:tdd" {
		t.Fatalf("skill title: %q", byKind[0].Title)
	}
}

// TestSearchWithoutVectorIsLexicalOnly is 040 §13.8: an instance with no
// embedding provider serves real lexical results rather than an empty set.
func TestSearchWithoutVectorIsLexicalOnly(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	seedSearchTask(t, s, "P1-1", "A task")
	seedChunks(t, s, ChunkSubject{Kind: SubjectTask, TaskID: "P1-1", Project: "p1"},
		fixtureChunk{text: "Pruning a worktree releases its lease."})

	hits, err := s.Search(t.Context(), SearchQuery{Text: "pruning", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].TaskID != "P1-1" || hits[0].DenseRank != 0 || hits[0].LexicalRank != 1 {
		t.Fatalf("no-provider search: %+v", hits)
	}
}

// TestSearchTombstonedSubjectsStayHidden: a soft-deleted subject keeps its
// chunk rows until the next convergence pass, and must not surface meanwhile.
func TestSearchTombstonedSubjectsStayHidden(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	seedSearchTask(t, s, "P1-1", "A task")
	seedChunks(t, s, ChunkSubject{Kind: SubjectTask, TaskID: "P1-1", Project: "p1"},
		fixtureChunk{text: "Pruning a worktree releases its lease."})
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE tasks SET deleted_at = now() WHERE id = 'P1-1'`); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search(t.Context(), SearchQuery{Text: "pruning", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("tombstoned task surfaced: %+v", hits)
	}
}

func TestSearchRejectsBadInput(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)

	for name, q := range map[string]SearchQuery{
		"unknown mode":      {Text: "x", Mode: "fuzzy", Limit: 5},
		"unknown kind":      {Text: "x", Kinds: []string{"repo"}, Limit: 5},
		"zero limit":        {Text: "x"},
		"dense no vector":   {Text: "x", Mode: model.SearchDense, Limit: 5},
		"no text":           {Limit: 5},
		"zero query vector": {Text: "x", Vector: make([]float32, IndexDim), Limit: 5},
	} {
		if _, err := s.Search(t.Context(), q); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: want ErrInvalidInput, got %v", name, err)
		}
	}
}

// TestSearchMetrics covers 040 §10's search instruments, including the one
// that matters operationally: an arm that ran and offered nothing.
func TestSearchMetrics(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewPedanticRegistry()
	s := OpenTestStore(t, WithMetrics(reg))
	seedDocsProject(t, s)
	seedSearchTask(t, s, "P1-1", "A task")
	seedChunks(t, s, ChunkSubject{Kind: SubjectTask, TaskID: "P1-1", Project: "p1"},
		fixtureChunk{text: "Pruning a worktree releases its lease.", vec: vec(1, 0, 0)})

	if _, err := s.Search(t.Context(), SearchQuery{Text: "pruning", Vector: vec(1, 0, 0), Limit: 5}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.searchRequests.WithLabelValues("hybrid", "ok")); got != 1 {
		t.Errorf("hybrid ok: %v", got)
	}
	if got := testutil.CollectAndCount(s.metrics.searchSeconds); got != 1 {
		t.Errorf("duration series: %v", got)
	}

	// A query no chunk matches: the lexical arm ran and offered nothing, the
	// dense arm ran and offered nothing (the one chunk sits below the floor),
	// and the request itself is "empty", not "ok".
	if _, err := s.Search(t.Context(), SearchQuery{Text: "kubernetes", Vector: vec(0, 1, 0), Limit: 5}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.searchRequests.WithLabelValues("hybrid", "empty")); got != 1 {
		t.Errorf("hybrid empty: %v", got)
	}
	for _, arm := range []string{"dense", "lexical"} {
		if got := testutil.ToFloat64(s.metrics.searchArmEmpties.WithLabelValues(arm)); got != 1 {
			t.Errorf("%s arm empty: %v", arm, got)
		}
	}

	// A rejected request is counted under the mode the caller asked for, and
	// an unrecognised mode folds to one bounded label rather than minting a
	// series per typo.
	if _, err := s.Search(t.Context(), SearchQuery{Text: "x", Mode: "fuzzy", Limit: 5}); err == nil {
		t.Fatal("want an error for mode=fuzzy")
	}
	if got := testutil.ToFloat64(s.metrics.searchRequests.WithLabelValues("invalid", "error")); got != 1 {
		t.Errorf("invalid mode: %v", got)
	}
}
