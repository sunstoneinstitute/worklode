package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// DocSection is one anchored heading within a document, as the sync client
// ships it (025 §5.1).
type DocSection struct {
	Anchor, Heading string
	Depth, Position int
}

// DocEdge is one frontmatter-derived relation between documents (025 §5.1).
type DocEdge struct {
	SrcAnchor, Rel, Target, TargetAnchor string
}

// DocUpsert is one document as the sync client ships it (025 §5.1).
type DocUpsert struct {
	Kind, Ordinal, Status, Title, Body string
	Frontmatter                        json.RawMessage
	Sections                           []DocSection
	Edges                              []DocEdge
}

// DocSyncProvenance records where a sync came from (025 §16.2).
type DocSyncProvenance struct {
	SourceBranch string
	Dirty        bool
}

// DocSyncResult is one document's sync outcome: "added", "updated", or
// "unchanged".
type DocSyncResult struct {
	DocID, Kind, Outcome string
}

// Doc is one stored document. Body is "" in ListDocs rows (list is metadata;
// the full text comes from GetDoc).
type Doc struct {
	Project, Kind, Ordinal, DocID  string
	Status, Title, Body            string
	Frontmatter                    json.RawMessage
	Version                        int
	SourceBranch                   string
	SourceDirty                    bool
	SyncedAt, CreatedAt, UpdatedAt time.Time
}

// docKindTokens maps a doc's kind to the token used in its rendered id
// (KEY-SPEC-n, KEY-ADR-n, KEY-PLAN-s-p).
var docKindTokens = map[string]string{"spec": "SPEC", "adr": "ADR", "plan": "PLAN"}

var (
	specOrdinalRe = regexp.MustCompile(`^[1-9][0-9]*$`)
	planOrdinalRe = regexp.MustCompile(`^(0|[1-9][0-9]*)-[1-9][0-9]*$`)
)

// validDocEdgeRels mirrors the doc_edges.rel CHECK constraint (migrations
// 0011, 0012).
var validDocEdgeRels = map[string]bool{
	"implements": true, "covers": true, "amends": true, "amendedBy": true,
	"replaces": true, "isReplacedBy": true, "blocks": true,
}

// validateDocUpsert checks one upsert's shape (025 §5.1/§5).
func validateDocUpsert(d DocUpsert) error {
	token, ok := docKindTokens[d.Kind]
	if !ok {
		return fmt.Errorf("doc kind %q: %w", d.Kind, ErrInvalidInput)
	}
	re := specOrdinalRe
	if d.Kind == "plan" {
		re = planOrdinalRe
	}
	if !re.MatchString(d.Ordinal) {
		return fmt.Errorf("%s ordinal %q: %w", d.Kind, d.Ordinal, ErrInvalidInput)
	}
	if d.Status == "" || d.Title == "" {
		return fmt.Errorf("%s-%s: empty status or title: %w", token, d.Ordinal, ErrInvalidInput)
	}
	// json.Valid rejects both nil and "" (empty RawMessage), so this also
	// catches the unset-frontmatter case that would otherwise reach
	// frontmatter = $n::jsonb as an empty string and fail as a raw Postgres
	// error instead of ErrInvalidInput.
	if !json.Valid(d.Frontmatter) {
		return fmt.Errorf("%s-%s: frontmatter is not valid JSON: %w", token, d.Ordinal, ErrInvalidInput)
	}
	for _, e := range d.Edges {
		if !validDocEdgeRels[e.Rel] {
			return fmt.Errorf("edge rel %q: %w", e.Rel, ErrInvalidInput)
		}
	}
	return nil
}

// ApplyDocSync upserts docs for projectID inside tx, idempotent on
// (project, kind, ordinal). Meant to be called from a RecordEvent apply
// callback; eventID attributes the state_log rows.
//
// Every doc is validated before any write happens, so a bad doc later in
// the slice never leaves earlier docs half-applied. status is carried as
// data throughout — no editorial transition logic lives here (025 §5.1).
func (s *Store) ApplyDocSync(tx *sql.Tx, now time.Time, eventID int64,
	projectID string, prov DocSyncProvenance, docs []DocUpsert) ([]DocSyncResult, error) {
	for _, d := range docs {
		if err := validateDocUpsert(d); err != nil {
			return nil, err
		}
	}

	var key string
	if err := tx.QueryRow(`SELECT key FROM projects WHERE id = $1`, projectID).Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %s: %w", projectID, ErrNotFound)
		}
		return nil, fmt.Errorf("look up project: %w", err)
	}

	ts := now.UTC().Truncate(time.Second)
	results := make([]DocSyncResult, 0, len(docs))
	for _, d := range docs {
		docID := key + "-" + docKindTokens[d.Kind] + "-" + d.Ordinal

		var outcome string
		var version int
		err := tx.QueryRow(`
			SELECT CASE WHEN status = $4 AND title = $5 AND body = $6
			            AND frontmatter = $7::jsonb
			       THEN 'unchanged' ELSE 'updated' END, version
			  FROM docs WHERE project = $1 AND kind = $2 AND ordinal = $3
			  FOR UPDATE`,
			projectID, d.Kind, d.Ordinal, d.Status, d.Title, d.Body, string(d.Frontmatter),
		).Scan(&outcome, &version)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			outcome = "added"
		case err != nil:
			return nil, fmt.Errorf("check doc %s: %w", docID, err)
		}

		switch outcome {
		case "added":
			version = 1
			_, err = tx.Exec(`
				INSERT INTO docs (project, kind, ordinal, doc_id, status, title, body,
				                  frontmatter, version, source_branch, source_dirty,
				                  synced_at, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, 1, $9, $10, $11, $11, $11)`,
				projectID, d.Kind, d.Ordinal, docID, d.Status, d.Title, d.Body,
				string(d.Frontmatter), prov.SourceBranch, prov.Dirty, ts)
		case "updated":
			version++
			_, err = tx.Exec(`
				UPDATE docs SET status = $4, title = $5, body = $6, frontmatter = $7::jsonb,
				       version = version + 1, source_branch = $8, source_dirty = $9,
				       synced_at = $10, updated_at = $10
				 WHERE project = $1 AND kind = $2 AND ordinal = $3`,
				projectID, d.Kind, d.Ordinal, d.Status, d.Title, d.Body,
				string(d.Frontmatter), prov.SourceBranch, prov.Dirty, ts)
		case "unchanged": // provenance still overwritten (025 §16.2)
			_, err = tx.Exec(`
				UPDATE docs SET source_branch = $4, source_dirty = $5,
				       synced_at = $6, updated_at = $6
				 WHERE project = $1 AND kind = $2 AND ordinal = $3`,
				projectID, d.Kind, d.Ordinal, prov.SourceBranch, prov.Dirty, ts)
		}
		if err != nil {
			return nil, fmt.Errorf("write doc %s: %w", docID, err)
		}

		if outcome != "unchanged" {
			for _, q := range []string{
				`DELETE FROM doc_sections WHERE project = $1 AND kind = $2 AND ordinal = $3`,
				`DELETE FROM doc_edges WHERE project = $1 AND kind = $2 AND ordinal = $3`,
			} {
				if _, err := tx.Exec(q, projectID, d.Kind, d.Ordinal); err != nil {
					return nil, fmt.Errorf("clear derived rows for %s: %w", docID, err)
				}
			}
			for _, sec := range d.Sections {
				if _, err := tx.Exec(`
					INSERT INTO doc_sections (project, kind, ordinal, anchor, heading, depth, position)
					VALUES ($1, $2, $3, $4, $5, $6, $7)`,
					projectID, d.Kind, d.Ordinal, sec.Anchor, sec.Heading, sec.Depth, sec.Position); err != nil {
					return nil, fmt.Errorf("insert section %s#%s: %w", docID, sec.Anchor, err)
				}
			}
			for _, e := range d.Edges {
				if _, err := tx.Exec(`
					INSERT INTO doc_edges (project, kind, ordinal, src_anchor, rel, target, target_anchor)
					VALUES ($1, $2, $3, $4, $5, $6, $7)`,
					projectID, d.Kind, d.Ordinal, e.SrcAnchor, e.Rel, e.Target, e.TargetAnchor); err != nil {
					return nil, fmt.Errorf("insert edge %s %s %s: %w", docID, e.Rel, e.Target, err)
				}
			}
			if err := LogChange(tx, "doc", docID, eventID, map[string]any{
				"outcome": outcome, "version": version, "status": d.Status,
			}); err != nil {
				return nil, fmt.Errorf("log change for doc %s: %w", docID, err)
			}
		}

		s.metrics.docUpsert(outcome)
		results = append(results, DocSyncResult{DocID: docID, Kind: d.Kind, Outcome: outcome})
	}
	return results, nil
}

// DocSyncOutcomes is ApplyDocSync's read-only twin: the per-doc outcomes a
// sync WOULD produce, writing nothing (--dry-run, 025 §16.2).
func (s *Store) DocSyncOutcomes(ctx context.Context, projectID string, docs []DocUpsert) ([]DocSyncResult, error) {
	// Validate before the project lookup, matching ApplyDocSync's order, so
	// the two return the same error class (ErrInvalidInput vs ErrNotFound)
	// for identical bad input regardless of which is wrong.
	for _, d := range docs {
		if err := validateDocUpsert(d); err != nil {
			return nil, err
		}
	}

	var key string
	if err := s.db.QueryRowContext(ctx, `SELECT key FROM projects WHERE id = $1`, projectID).Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %s: %w", projectID, ErrNotFound)
		}
		return nil, fmt.Errorf("look up project: %w", err)
	}
	var out []DocSyncResult
	for _, d := range docs {
		docID := key + "-" + docKindTokens[d.Kind] + "-" + d.Ordinal
		var outcome string
		err := s.db.QueryRowContext(ctx, `
			SELECT CASE WHEN status = $4 AND title = $5 AND body = $6
			            AND frontmatter = $7::jsonb
			       THEN 'unchanged' ELSE 'updated' END
			  FROM docs WHERE project = $1 AND kind = $2 AND ordinal = $3`,
			projectID, d.Kind, d.Ordinal, d.Status, d.Title, d.Body, string(d.Frontmatter),
		).Scan(&outcome)
		if errors.Is(err, sql.ErrNoRows) {
			outcome = "added"
		} else if err != nil {
			return nil, fmt.Errorf("check doc %s: %w", docID, err)
		}
		out = append(out, DocSyncResult{DocID: docID, Kind: d.Kind, Outcome: outcome})
	}
	return out, nil
}

// GetDoc returns one document by its rendered id ("WL-SPEC-25"), with its
// sections (ordered by position) and edges (ordered by src_anchor, rel,
// target, target_anchor). ErrNotFound when no such doc.
func (s *Store) GetDoc(ctx context.Context, docID string) (*Doc, []DocSection, []DocEdge, error) {
	var d Doc
	var frontmatter []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT project, kind, ordinal, doc_id, status, title, body, frontmatter,
		       version, source_branch, source_dirty, synced_at, created_at, updated_at
		  FROM docs WHERE doc_id = $1`, docID,
	).Scan(&d.Project, &d.Kind, &d.Ordinal, &d.DocID, &d.Status, &d.Title, &d.Body, &frontmatter,
		&d.Version, &d.SourceBranch, &d.SourceDirty, &d.SyncedAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get doc %s: %w", docID, err)
	}
	d.Frontmatter = json.RawMessage(frontmatter)
	d.SyncedAt = d.SyncedAt.UTC()
	d.CreatedAt = d.CreatedAt.UTC()
	d.UpdatedAt = d.UpdatedAt.UTC()

	secRows, err := s.db.QueryContext(ctx, `
		SELECT anchor, heading, depth, position FROM doc_sections
		 WHERE project = $1 AND kind = $2 AND ordinal = $3
		 ORDER BY position`, d.Project, d.Kind, d.Ordinal)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get doc %s sections: %w", docID, err)
	}
	defer secRows.Close()
	var sections []DocSection
	for secRows.Next() {
		var sec DocSection
		if err := secRows.Scan(&sec.Anchor, &sec.Heading, &sec.Depth, &sec.Position); err != nil {
			return nil, nil, nil, fmt.Errorf("scan doc %s section: %w", docID, err)
		}
		sections = append(sections, sec)
	}
	if err := secRows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("get doc %s sections: %w", docID, err)
	}

	edgeRows, err := s.db.QueryContext(ctx, `
		SELECT src_anchor, rel, target, target_anchor FROM doc_edges
		 WHERE project = $1 AND kind = $2 AND ordinal = $3
		 ORDER BY src_anchor, rel, target, target_anchor`, d.Project, d.Kind, d.Ordinal)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get doc %s edges: %w", docID, err)
	}
	defer edgeRows.Close()
	var edges []DocEdge
	for edgeRows.Next() {
		var e DocEdge
		if err := edgeRows.Scan(&e.SrcAnchor, &e.Rel, &e.Target, &e.TargetAnchor); err != nil {
			return nil, nil, nil, fmt.Errorf("scan doc %s edge: %w", docID, err)
		}
		edges = append(edges, e)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("get doc %s edges: %w", docID, err)
	}

	return &d, sections, edges, nil
}

// DocFilter narrows ListDocs; zero fields do not filter.
type DocFilter struct {
	Project, Kind, Status string
}

// ListDocs returns matching documents ordered by project, kind, then ordinal
// numerically (spec 10 after spec 9; plan 34-2 after 34-1). Rows are
// bodyless (Body == ""); GetDoc carries the full text.
func (s *Store) ListDocs(ctx context.Context, f DocFilter) ([]Doc, error) {
	q := `SELECT project, kind, ordinal, doc_id, status, title, frontmatter,
	             version, source_branch, source_dirty, synced_at, created_at, updated_at
	        FROM docs`
	var conds []string
	var args []any
	if f.Project != "" {
		args = append(args, f.Project)
		conds = append(conds, fmt.Sprintf(`project = $%d`, len(args)))
	}
	if f.Kind != "" {
		args = append(args, f.Kind)
		conds = append(conds, fmt.Sprintf(`kind = $%d`, len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf(`status = $%d`, len(args)))
	}
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	// Ordinal is always dash-separated integers (Task 2's validation
	// guarantees the cast is safe), so this sorts spec 9 before spec 10 and
	// plan 34-1 before 34-2 numerically rather than lexically.
	q += ` ORDER BY project, kind, string_to_array(ordinal, '-')::int[]`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list docs: %w", err)
	}
	defer rows.Close()

	var out []Doc
	for rows.Next() {
		var d Doc
		var frontmatter []byte
		if err := rows.Scan(&d.Project, &d.Kind, &d.Ordinal, &d.DocID, &d.Status, &d.Title, &frontmatter,
			&d.Version, &d.SourceBranch, &d.SourceDirty, &d.SyncedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan doc: %w", err)
		}
		d.Frontmatter = json.RawMessage(frontmatter)
		d.SyncedAt = d.SyncedAt.UTC()
		d.CreatedAt = d.CreatedAt.UTC()
		d.UpdatedAt = d.UpdatedAt.UTC()
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list docs: %w", err)
	}
	return out, nil
}
