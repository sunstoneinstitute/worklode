package store

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Retrieval constants from spec 040 §6. None is a caller's knob: the floor
// and the candidate depth shape what each arm offers fusion, and k is the
// constant from the original RRF formulation.
const (
	// searchDenseFloor is the cosine similarity below which the dense arm
	// stops offering candidates (§6.1, from 016). It is a candidate filter on
	// that arm only, never a threshold on the fused score — after fusion the
	// score is a rank-reciprocal sum, and comparing that to 0.35 would be a
	// category error.
	searchDenseFloor = 0.35
	// searchCandidates is how deep each arm ranks before fusion, independent
	// of the caller's limit: fusion can only reorder what the arms offered it.
	searchCandidates = 50
	// searchExcerptRunes caps the returned excerpt. A chunk runs to
	// corpusindex.ChunkRunes (3600), which is an embedding unit, not something
	// a result list should carry twenty of.
	searchExcerptRunes = 400
)

// SearchQuery is one retrieval request over the corpus index (040 §6).
//
// Vector is the query text already embedded by the caller — the store does no
// embedding — and nil means the dense arm does not run at all. That is the
// no-provider instance (§11): it serves real lexical results rather than an
// empty set.
type SearchQuery struct {
	Text   string
	Vector []float32
	// Kinds narrows to some of doc|task|skill; empty means all three.
	Kinds []string
	// Project scopes the search. Chunks carrying no project stay visible —
	// that disjunct is what keeps org-wide skills findable from inside a
	// project-scoped search (§6.4).
	Project string
	Limit   int
	// Mode is hybrid (default), dense or lexical.
	Mode string
}

// searchSQL is the whole of §6 in one statement: two arms ranked
// independently, then fused by reciprocal rank.
//
// Both arms pool by subject *before* ranking, and that is not cosmetic. RRF
// gives every row in a ranked list a share of the score, so fusing chunk
// rankings would let a long document accumulate mass by placing eight
// mediocre chunks in the top 50 and outrank a short document that answered
// the question exactly once (§6.1).
//
// Each arm is switched by a bound boolean rather than by assembling a
// different query per mode, so all three modes run the statement a reader has
// in front of them. A disabled dense arm binds a NULL vector, which makes its
// distance NULL and its floor comparison false — no rows, no error.
//
// $1 query vector  $2 dense floor  $3 query text  $4 kinds  $5 project
// $6 candidates per arm  $7 limit  $8 dense on  $9 lexical on  $10 excerpt cap
const searchSQL = `
WITH q AS (
    SELECT websearch_to_tsquery('simple', $3) AS tq
),
dense AS (
    SELECT c.subject_kind, c.doc_id, c.task_id, c.skill_id, c.anchor,
           max(1 - (c.embedding <=> $1::vector)) AS score,
           (array_agg(c.id ORDER BY c.embedding <=> $1::vector))[1] AS chunk_id
      FROM index_chunks c
     WHERE $8::bool
       AND c.embedding IS NOT NULL
       AND ($4::text[] IS NULL OR c.subject_kind = ANY($4))
       AND ($5::text IS NULL OR c.project = $5 OR c.project IS NULL)
       AND (1 - (c.embedding <=> $1::vector)) >= $2
     GROUP BY c.subject_kind, c.doc_id, c.task_id, c.skill_id, c.anchor
     ORDER BY score DESC
     LIMIT $6
),
dense_ranked AS (
    SELECT d.*, row_number() OVER (ORDER BY d.score DESC) AS rank FROM dense d
),
lexical AS (
    SELECT c.subject_kind, c.doc_id, c.task_id, c.skill_id, c.anchor,
           max(ts_rank_cd(c.tsv, q.tq)) AS score,
           (array_agg(c.id ORDER BY ts_rank_cd(c.tsv, q.tq) DESC))[1] AS chunk_id
      FROM index_chunks c, q
     WHERE $9::bool
       AND ($4::text[] IS NULL OR c.subject_kind = ANY($4))
       AND ($5::text IS NULL OR c.project = $5 OR c.project IS NULL)
       AND c.tsv @@ q.tq
     GROUP BY c.subject_kind, c.doc_id, c.task_id, c.skill_id, c.anchor
     ORDER BY score DESC
     LIMIT $6
),
lexical_ranked AS (
    SELECT l.*, row_number() OVER (ORDER BY l.score DESC) AS rank FROM lexical l
),
fused AS (
    SELECT coalesce(d.subject_kind, l.subject_kind) AS subject_kind,
           coalesce(d.doc_id, l.doc_id)             AS doc_id,
           coalesce(d.task_id, l.task_id)           AS task_id,
           coalesce(d.skill_id, l.skill_id)         AS skill_id,
           coalesce(d.anchor, l.anchor)             AS anchor,
           -- The lexical arm's best chunk when it has one: on an identifier
           -- query that is the chunk literally containing the identifier,
           -- which is the excerpt a reader wants to see.
           coalesce(l.chunk_id, d.chunk_id)         AS chunk_id,
           -- Reciprocal rank, k = 60, weight 1.0 on both arms (§6.3). A
           -- missing arm contributes zero rather than dropping the row.
           coalesce(1.0 / (60 + d.rank), 0) + coalesce(1.0 / (60 + l.rank), 0) AS score,
           coalesce(d.rank, 0) AS dense_rank,
           coalesce(l.rank, 0) AS lexical_rank
      FROM dense_ranked d
      FULL OUTER JOIN lexical_ranked l
        ON d.subject_kind = l.subject_kind
       AND d.doc_id   IS NOT DISTINCT FROM l.doc_id
       AND d.task_id  IS NOT DISTINCT FROM l.task_id
       AND d.skill_id IS NOT DISTINCT FROM l.skill_id
       AND d.anchor = l.anchor
)
SELECT f.subject_kind,
       coalesce(f.doc_id, 0), coalesce(f.task_id, ''), coalesce(f.skill_id, 0),
       f.anchor,
       coalesce(d.title, t.title, sk.qualifier || ':' || sk.name, ''),
       left(c.chunk_text, $10),
       f.score, f.dense_rank, f.lexical_rank,
       (SELECT count(*) FROM dense_ranked),
       (SELECT count(*) FROM lexical_ranked)
  FROM fused f
  JOIN index_chunks c ON c.id = f.chunk_id
  LEFT JOIN docs   d  ON d.id  = f.doc_id
  LEFT JOIN tasks  t  ON t.id  = f.task_id
  LEFT JOIN skills sk ON sk.id = f.skill_id
 -- Exactly one of the three joins matches, so this reads that subject's
 -- tombstone: a soft-deleted subject keeps its chunk rows until the next
 -- convergence pass, and must not surface in the meantime.
 WHERE coalesce(d.deleted_at, t.deleted_at, sk.deleted_at) IS NULL
 ORDER BY f.score DESC, f.subject_kind, f.doc_id, f.task_id, f.skill_id, f.anchor
 LIMIT $7`

// Search runs the two retrieval arms over index_chunks and returns their
// fused ranking, best first (040 §6). Neither arm is primary and neither is a
// fallback: mode=dense and mode=lexical run one arm each and return its own
// ranking, for comparing the two on a real query.
func (s *Store) Search(ctx context.Context, q SearchQuery) ([]model.SearchHit, error) {
	mode, err := q.normalize()
	if err != nil {
		s.metrics.searchRequest(mode, "error")
		return nil, err
	}

	var qvec any
	dense := mode != model.SearchLexical && len(q.Vector) > 0
	if dense {
		qvec = vectorLiteral(q.Vector)
	}
	lexical := mode != model.SearchDense
	var kinds any
	if len(q.Kinds) > 0 {
		kinds = q.Kinds
	}
	var project any
	if q.Project != "" {
		project = q.Project
	}

	start := time.Now()
	rows, err := s.db.QueryContext(ctx, searchSQL,
		qvec, searchDenseFloor, q.Text, kinds, project,
		searchCandidates, q.Limit, dense, lexical, searchExcerptRunes)
	if err != nil {
		s.metrics.searchRequest(mode, "error")
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	var (
		hits             []model.SearchHit
		denseN, lexicalN int64
	)
	for rows.Next() {
		var h model.SearchHit
		if err := rows.Scan(&h.Kind, &h.DocID, &h.TaskID, &h.SkillID, &h.Anchor,
			&h.Title, &h.Excerpt, &h.Score, &h.DenseRank, &h.LexicalRank,
			&denseN, &lexicalN); err != nil {
			s.metrics.searchRequest(mode, "error")
			return nil, fmt.Errorf("search scan: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		s.metrics.searchRequest(mode, "error")
		return nil, fmt.Errorf("search: %w", err)
	}
	s.metrics.searchDuration(mode, time.Since(start))

	// An arm that ran and offered nothing is the failure this spec is most
	// exposed to (§10): a silently broken tsv degrades the system into the
	// dense-only setup §0 rejects, and nothing else would notice. No rows at
	// all means both arms came back empty, since fusion drops nothing.
	if dense && denseN == 0 {
		s.metrics.searchArmEmpty("dense")
	}
	if lexical && lexicalN == 0 {
		s.metrics.searchArmEmpty("lexical")
	}
	if len(hits) == 0 {
		s.metrics.searchRequest(mode, "empty")
		return nil, nil
	}
	s.metrics.searchRequest(mode, "ok")
	return hits, nil
}

// normalize validates q and returns the mode it runs in. The mode comes back
// even on error so the metric label is the one the caller asked for.
func (q SearchQuery) normalize() (string, error) {
	mode := q.Mode
	if mode == "" {
		mode = model.SearchHybrid
	}
	switch mode {
	case model.SearchHybrid, model.SearchDense, model.SearchLexical:
	default:
		return mode, fmt.Errorf("search: unknown mode %q, want hybrid|dense|lexical: %w", q.Mode, ErrInvalidInput)
	}
	if q.Limit <= 0 {
		return mode, fmt.Errorf("search: limit must be positive, got %d: %w", q.Limit, ErrInvalidInput)
	}
	for _, k := range q.Kinds {
		if k != SubjectDoc && k != SubjectTask && k != SubjectSkill {
			return mode, fmt.Errorf("search: unknown kind %q, want doc|task|skill: %w", k, ErrInvalidInput)
		}
	}
	if len(q.Vector) > 0 && isZeroVector(q.Vector) {
		return mode, fmt.Errorf("search: query vector is all zeros (cosine distance is undefined): %w", ErrInvalidInput)
	}
	if mode == model.SearchDense && len(q.Vector) == 0 {
		return mode, fmt.Errorf("search: mode=dense needs a query vector: %w", ErrInvalidInput)
	}
	if mode != model.SearchDense && q.Text == "" {
		return mode, fmt.Errorf("search: empty query text: %w", ErrInvalidInput)
	}
	return mode, nil
}

// searchModes is the bounded label set for the search metrics (§10).
var searchModes = []string{model.SearchHybrid, model.SearchDense, model.SearchLexical}

func validSearchMode(mode string) bool { return slices.Contains(searchModes, mode) }
