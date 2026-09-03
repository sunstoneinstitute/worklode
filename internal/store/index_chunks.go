package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/corpusindex"
)

// Subject kinds, matching index_chunks.subject_kind's CHECK (040 §5).
const (
	SubjectDoc   = "doc"
	SubjectTask  = "task"
	SubjectSkill = "skill"
)

// IndexDim is the embedding width every provider must produce (040 §2.2).
// The index_chunks.embedding typmod enforces it in Postgres; nothing here
// re-checks it, so a wrong-shaped vector is refused by the column that
// stores it rather than by a Go guard that could drift from it.
const IndexDim = 768

// ChunkSubject names one indexed subject: exactly one id field is set, and
// Kind says which. ContentHash is the subject's live hash (§5), stored
// identically on every one of its chunk rows; StaleSubjects computes it and
// callers pass back what it returned, so the freshness comparison has one
// definition and it lives in SQL.
type ChunkSubject struct {
	Kind        string
	DocID       int64
	TaskID      string
	SkillID     int64
	Project     string // "" stores NULL; always "" for skills (org-wide)
	ContentHash string
}

// subjectColumn returns the id column and value for s, or an error if Kind
// is not one of the three.
func (s ChunkSubject) subjectColumn() (string, any, error) {
	switch s.Kind {
	case SubjectDoc:
		return "doc_id", s.DocID, nil
	case SubjectTask:
		return "task_id", s.TaskID, nil
	case SubjectSkill:
		return "skill_id", s.SkillID, nil
	default:
		return "", nil, fmt.Errorf("unknown subject kind %q: %w", s.Kind, ErrInvalidInput)
	}
}

// vectorLiteral renders v in pgvector's text input format, e.g. "[1,0.5]".
// Vectors are bound as text and cast with ::vector — no client-side pgvector
// dependency needed.
func vectorLiteral(v []float32) string {
	var b strings.Builder
	// A 768-dimension vector renders to ~8 KB; sizing up front spares the
	// builder a dozen reallocation-and-copy rounds per chunk.
	b.Grow(len(v)*12 + 2)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// isZeroVector reports whether v has zero magnitude. Cosine distance is
// undefined against such a vector, so neither a stored chunk nor a query may
// be one.
func isZeroVector(v []float32) bool {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	return sum == 0
}

// ReplaceSubjectChunks swaps one subject's whole chunk set in a single
// transaction. vectors is either nil — an instance with no embedding
// provider, which still indexes the text for the lexical arm (040 §11) —
// or one vector per chunk, positionally.
//
// Vector width is not checked here: the vector(768) typmod refuses a
// wrong-shaped one at INSERT (§2.2). A zero-norm vector is checked, because
// no column constraint catches it: pgvector's <=> returns NaN against it and
// NaN sorts above every real score, so one degenerate chunk would rank first
// for every query forever.
func (s *Store) ReplaceSubjectChunks(ctx context.Context, subj ChunkSubject, chunks []corpusindex.Chunk, vectors [][]float32) error {
	col, id, err := subj.subjectColumn()
	if err != nil {
		return fmt.Errorf("replace subject chunks: %w", err)
	}
	if vectors != nil && len(vectors) != len(chunks) {
		return fmt.Errorf("replace subject chunks %s %v: %d vectors for %d chunks: %w",
			subj.Kind, id, len(vectors), len(chunks), ErrInvalidInput)
	}
	for i, v := range vectors {
		if isZeroVector(v) {
			return fmt.Errorf("replace subject chunks %s %v: vector %d is all zeros (cosine distance is undefined): %w",
				subj.Kind, id, i, ErrInvalidInput)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace subject chunks %s %v: %w", subj.Kind, id, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM index_chunks WHERE `+col+` = $1`, id); err != nil {
		return fmt.Errorf("clear subject chunks %s %v: %w", subj.Kind, id, err)
	}

	if len(chunks) > 0 {
		// One INSERT over parallel arrays rather than one per chunk: a full
		// re-embed is thousands of chunks in one transaction, and every round
		// trip is paid with that transaction open. WITH ORDINALITY is not used
		// — chunk_index comes from the Chunk itself, since a doc's sub-chunks
		// number per anchor, not per subject (§4.2).
		var (
			idxs    = make([]int32, len(chunks))
			anchors = make([]string, len(chunks))
			heads   = make([]string, len(chunks))
			texts   = make([]string, len(chunks))
			// Empty string means "no vector"; NULLIF below turns it into a
			// NULL embedding, which is what a no-provider instance writes
			// (§11). A text[] of literals keeps this one bind parameter
			// rather than a per-chunk round trip.
			vecs = make([]string, len(chunks))
		)
		for i, c := range chunks {
			idxs[i] = int32(c.Index)
			anchors[i] = c.Anchor
			heads[i] = c.Header
			texts[i] = c.Text
			if vectors != nil {
				vecs[i] = vectorLiteral(vectors[i])
			}
		}
		var project any
		if subj.Project != "" {
			project = subj.Project
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO index_chunks
			    (subject_kind, `+col+`, project, anchor, chunk_index,
			     context_header, chunk_text, content_hash, embedding, indexed_at)
			SELECT $1, $2, $3, c.anchor, c.idx, c.header, c.text, $4,
			       nullif(c.vec, '')::vector, $5
			  FROM unnest($6::int[], $7::text[], $8::text[], $9::text[], $10::text[])
			       AS c(idx, anchor, header, text, vec)`,
			subj.Kind, id, project, subj.ContentHash, s.Now(),
			idxs, anchors, heads, texts, vecs); err != nil {
			return fmt.Errorf("insert subject chunks %s %v: %w", subj.Kind, id, err)
		}
	}
	return tx.Commit()
}

// liveHashSQL is the live content-hash expression per subject kind — the one
// definition of "has this subject changed since it was indexed" (040 §7).
// Skills reuse skill_versions.content_hash, which 016 already maintains over
// the whole skill dir; docs and tasks have no such column, so the hash is
// taken over exactly the text and metadata the chunker feeds the index,
// including the header fields (a task's kind and state are embedded and
// lexically indexed, so changing one must re-index).
var liveHashSQL = map[string]string{
	SubjectDoc:   `md5(d.title || E'\n' || d.body)`,
	SubjectTask:  `md5(t.kind || E'\n' || t.state || E'\n' || t.title || E'\n' || coalesce(t.body, ''))`,
	SubjectSkill: `v.content_hash`,
}

// staleSubjectsSQL is the per-kind query behind StaleSubjects. The LEFT JOIN
// LATERAL aggregates a subject's chunk rows into its hash — every chunk of a
// subject carries the same one, so max() is that hash — and whether any of
// them lacks a vector. IS DISTINCT FROM makes a subject with no chunk rows at
// all stale, which is what makes a never-indexed subject converge (§7); the
// $2 disjunct is what makes a provider change converge, since invalidation
// nulls vectors without touching the text or its hash (§8).
var staleSubjectsSQL = map[string]string{
	SubjectDoc: `
		SELECT d.id::text, d.project_id, ` + liveHashSQL[SubjectDoc] + `
		  FROM docs d
		  LEFT JOIN LATERAL (
		       SELECT max(content_hash) AS content_hash,
		              bool_or(embedding IS NULL) AS no_vector
		         FROM index_chunks c WHERE c.doc_id = d.id
		  ) ic ON true
		 WHERE ic.content_hash IS DISTINCT FROM ` + liveHashSQL[SubjectDoc] + `
		    OR ($2 AND ic.no_vector)
		 ORDER BY d.id
		 LIMIT $1`,
	SubjectTask: `
		SELECT t.id, t.project_id, ` + liveHashSQL[SubjectTask] + `
		  FROM tasks t
		  LEFT JOIN LATERAL (
		       SELECT max(content_hash) AS content_hash,
		              bool_or(embedding IS NULL) AS no_vector
		         FROM index_chunks c WHERE c.task_id = t.id
		  ) ic ON true
		 WHERE ic.content_hash IS DISTINCT FROM ` + liveHashSQL[SubjectTask] + `
		    OR ($2 AND ic.no_vector)
		 ORDER BY t.id
		 LIMIT $1`,
	// Skills are org-wide, so they carry no project. Soft-deleted ones are
	// excluded (§1); their chunk rows are deleted by the caller rather than
	// filtered, so the index carries no tombstones.
	SubjectSkill: `
		SELECT s.id::text, '', ` + liveHashSQL[SubjectSkill] + `
		  FROM skills s
		  JOIN skill_versions v ON v.id = s.latest_version_id
		  LEFT JOIN LATERAL (
		       SELECT max(content_hash) AS content_hash,
		              bool_or(embedding IS NULL) AS no_vector
		         FROM index_chunks c WHERE c.skill_id = s.id
		  ) ic ON true
		 WHERE s.deleted_at IS NULL
		   AND (ic.content_hash IS DISTINCT FROM ` + liveHashSQL[SubjectSkill] + `
		        OR ($2 AND ic.no_vector))
		 ORDER BY s.name
		 LIMIT $1`,
}

// StaleSubjects returns up to limit subjects of kind that need indexing:
// those whose indexed text no longer matches the live row, never-indexed ones
// included, plus — when needVectors is set, meaning an embedding provider is
// configured — those whose chunk rows carry no vector. That second set is
// what a provider change leaves behind (§8) and what a failed embed call
// leaves behind mid-pass; with no provider it is every row, so a lexical-only
// instance passes false and converges. The returned ContentHash is the live
// hash the caller writes back through ReplaceSubjectChunks.
func (s *Store) StaleSubjects(ctx context.Context, kind string, limit int, needVectors bool) ([]ChunkSubject, error) {
	q, ok := staleSubjectsSQL[kind]
	if !ok {
		return nil, fmt.Errorf("stale subjects: unknown subject kind %q: %w", kind, ErrInvalidInput)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("stale subjects: limit must be positive, got %d: %w", limit, ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, q, limit, needVectors)
	if err != nil {
		return nil, fmt.Errorf("stale subjects %s: %w", kind, err)
	}
	return collectRows(rows, "stale subjects "+kind, func(r rowScanner) (ChunkSubject, error) {
		var (
			id   string
			subj = ChunkSubject{Kind: kind}
		)
		if err := r.Scan(&id, &subj.Project, &subj.ContentHash); err != nil {
			return ChunkSubject{}, err
		}
		switch kind {
		case SubjectTask:
			subj.TaskID = id
		default:
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				return ChunkSubject{}, fmt.Errorf("subject id %q: %w", id, err)
			}
			if kind == SubjectDoc {
				subj.DocID = n
			} else {
				subj.SkillID = n
			}
		}
		return subj, nil
	})
}

// ClearAllChunkVectors nulls every stored vector, returning how many rows it
// touched. This is the provider-change invalidation primitive (040 §8): the
// chunk text and its tsv are provider-independent, so the lexical arm keeps
// serving while the next convergence pass rebuilds the vectors.
func (s *Store) ClearAllChunkVectors(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE index_chunks SET embedding = NULL WHERE embedding IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("clear all chunk vectors: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("clear all chunk vectors rows affected: %w", err)
	}
	return n, nil
}

// IndexCounts is the index's size, for 040 §10's gauges.
type IndexCounts struct {
	ByKind        map[string]int64 // chunk rows per subject_kind
	WithoutVector int64            // chunk rows whose embedding is NULL
}

// IndexCounts reports chunk counts per subject kind and how many rows carry
// no vector.
func (s *Store) IndexCounts(ctx context.Context) (IndexCounts, error) {
	out := IndexCounts{ByKind: map[string]int64{}}
	rows, err := s.db.QueryContext(ctx, `
		SELECT subject_kind, count(*), count(*) FILTER (WHERE embedding IS NULL)
		  FROM index_chunks
		 GROUP BY subject_kind`)
	if err != nil {
		return IndexCounts{}, fmt.Errorf("index counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			kind     string
			n, noVec int64
		)
		if err := rows.Scan(&kind, &n, &noVec); err != nil {
			return IndexCounts{}, fmt.Errorf("index counts scan: %w", err)
		}
		out.ByKind[kind] = n
		out.WithoutVector += noVec
	}
	if err := rows.Err(); err != nil {
		return IndexCounts{}, fmt.Errorf("index counts: %w", err)
	}
	return out, nil
}

// RecommendSkills returns live skills scored by max cosine similarity over
// their chunks against query, best-first, at or above floor. It reads the
// skill slice of index_chunks (040 §5); rows with no vector are the dense
// arm's business to skip (§8), not a reason to drop the skill.
func (s *Store) RecommendSkills(ctx context.Context, query []float32, limit int, floor float64) ([]SkillMatch, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("recommend skills: limit must be positive, got %d: %w", limit, ErrInvalidInput)
	}
	if len(query) == 0 {
		return nil, fmt.Errorf("recommend skills: zero-length query vector: %w", ErrInvalidInput)
	}
	if isZeroVector(query) {
		return nil, fmt.Errorf("recommend skills: query vector is all zeros (cosine distance is undefined): %w", ErrInvalidInput)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT s.qualifier || ':' || s.name, s.description, coalesce(v.content_hash, ''),
		       max(1 - (e.embedding <=> $1::vector)) AS score
		FROM index_chunks e
		JOIN skills s ON s.id = e.skill_id
		LEFT JOIN skill_versions v ON v.id = s.latest_version_id
		WHERE e.subject_kind = 'skill' AND e.embedding IS NOT NULL
		  AND s.deleted_at IS NULL
		GROUP BY s.id, v.content_hash
		HAVING max(1 - (e.embedding <=> $1::vector)) >= $2
		ORDER BY score DESC, s.name
		LIMIT $3`, vectorLiteral(query), floor, limit)
	if err != nil {
		return nil, fmt.Errorf("recommend skills: %w", err)
	}
	return collectRows(rows, "recommend skills", func(r rowScanner) (SkillMatch, error) {
		var m SkillMatch
		if err := r.Scan(&m.Name, &m.Description, &m.ContentHash, &m.Score); err != nil {
			return SkillMatch{}, err
		}
		return m, nil
	})
}
