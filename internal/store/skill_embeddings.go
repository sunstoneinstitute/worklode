package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// vectorLiteral renders v in pgvector's text input format, e.g. "[1,0.5]".
// Vectors are bound as text and cast with ::vector — no client-side pgvector
// dependency needed.
func vectorLiteral(v []float32) string {
	var b strings.Builder
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

// ReplaceSkillEmbeddings swaps the full chunk-vector set for one skill. All
// vectors must share one non-zero dimension: the embedding column has no
// dimension typmod, so mixed dimensions in the table make cosine queries
// error outright (see 0007_skills.up.sql). A zero-norm vector is rejected
// too: pgvector's <=> returns NaN against it, and NaN sorts above every
// real score, so one degenerate chunk would rank first for every query
// forever.
func (s *Store) ReplaceSkillEmbeddings(ctx context.Context, skillID int64, vecs [][]float32) error {
	if len(vecs) > 0 {
		dim := len(vecs[0])
		if dim == 0 {
			return fmt.Errorf("replace skill embeddings %d: zero-length vector: %w", skillID, ErrInvalidInput)
		}
		for i, v := range vecs {
			if len(v) != dim {
				return fmt.Errorf("replace skill embeddings %d: vector %d has dimension %d, want %d: %w",
					skillID, i, len(v), dim, ErrInvalidInput)
			}
			var sum float32
			for _, x := range v {
				sum += x * x
			}
			if sum == 0 {
				return fmt.Errorf("replace skill embeddings %d: vector %d is all zeros (cosine distance is undefined): %w",
					skillID, i, ErrInvalidInput)
			}
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace skill embeddings %d: %w", skillID, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM skill_embeddings WHERE skill_id = $1`, skillID); err != nil {
		return fmt.Errorf("clear skill embeddings %d: %w", skillID, err)
	}
	for i, v := range vecs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO skill_embeddings (skill_id, chunk_index, embedding)
			VALUES ($1, $2, $3::vector)`, skillID, i, vectorLiteral(v)); err != nil {
			return fmt.Errorf("insert skill embedding %d/%d: %w", skillID, i, err)
		}
	}
	return tx.Commit()
}

// ClearAllSkillEmbeddings deletes every stored chunk vector, returning how
// many rows went. Used when the embedding provider changes: vectors from a
// different provider or model are not comparable with the new ones, and
// mixed dimensions break cosine queries outright.
func (s *Store) ClearAllSkillEmbeddings(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM skill_embeddings`)
	if err != nil {
		return 0, fmt.Errorf("clear all skill embeddings: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("clear all skill embeddings rows affected: %w", err)
	}
	return n, nil
}

// RecommendSkills returns live skills scored by max cosine similarity over
// their chunks against query, best-first, at or above floor.
func (s *Store) RecommendSkills(ctx context.Context, query []float32, limit int, floor float64) ([]SkillMatch, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("recommend skills: limit must be positive, got %d: %w", limit, ErrInvalidInput)
	}
	if len(query) == 0 {
		return nil, fmt.Errorf("recommend skills: zero-length query vector: %w", ErrInvalidInput)
	}
	var sum float32
	for _, x := range query {
		sum += x * x
	}
	if sum == 0 {
		return nil, fmt.Errorf("recommend skills: query vector is all zeros (cosine distance is undefined): %w", ErrInvalidInput)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT s.name, s.description, coalesce(v.content_hash, ''),
		       max(1 - (e.embedding <=> $1::vector)) AS score
		FROM skill_embeddings e
		JOIN skills s ON s.id = e.skill_id
		LEFT JOIN skill_versions v ON v.id = s.latest_version_id
		WHERE s.deleted_at IS NULL
		GROUP BY s.id, v.content_hash
		HAVING max(1 - (e.embedding <=> $1::vector)) >= $2
		ORDER BY score DESC, s.name
		LIMIT $3`, vectorLiteral(query), floor, limit)
	if err != nil {
		return nil, fmt.Errorf("recommend skills: %w", err)
	}
	defer rows.Close()
	var out []SkillMatch
	for rows.Next() {
		var m SkillMatch
		if err := rows.Scan(&m.Name, &m.Description, &m.ContentHash, &m.Score); err != nil {
			return nil, fmt.Errorf("recommend skills: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recommend skills: %w", err)
	}
	return out, nil
}
