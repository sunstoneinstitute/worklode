// references.go implements spec 029 §5's entity_edges: typed references
// between entities of different kinds, the only edges allowed to cross a
// project boundary. Containment (a deliverable's milestone, a task's
// project) always stays same-project and lives on the owning row instead;
// entity_edges is deliberately the exception, so its rel vocabulary is kept
// closed here rather than left to whatever string a caller passes.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// referenceShape names the kinds a rel's from and to ends must be.
type referenceShape struct {
	FromKind string
	ToKind   string
}

// referenceShapes is the rel vocabulary (029 §5), one row per rel naming its
// end kinds. A rel outside this table, or ends of the wrong kind, is
// ErrInvalidInput.
var referenceShapes = map[string]referenceShape{
	"depends_on": {FromKind: "milestone", ToKind: "deliverable"},
	"seeded_by":  {FromKind: "project", ToKind: "task"},
}

// entityKindTables names the table each end kind's existence is checked
// against, mirroring the CHECK constraints on entity_edges.from_kind/to_kind.
var entityKindTables = map[string]string{
	"project":     "projects",
	"milestone":   "milestones",
	"deliverable": "deliverables",
	"task":        "tasks",
}

// entityExists reports whether an id exists in the table for the given kind.
func entityExists(tx *sql.Tx, kind, id string) (bool, error) {
	table, ok := entityKindTables[kind]
	if !ok {
		return false, fmt.Errorf("unknown entity kind %q: %w", kind, ErrInvalidInput)
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("look up %s %s: %w", kind, id, err)
	}
	return exists, nil
}

// CreateEntityEdgeInput carries the fields for one reference.
type CreateEntityEdgeInput struct {
	FromKind  string
	From      string
	ToKind    string
	To        string
	Rel       string
	CreatedBy string
}

// CreateEntityEdge validates in.Rel against referenceShapes, checks both
// ends exist in their kind's table, and inserts the reference inside the
// given transaction, like CreateDeliverable, so part 4's promotion
// transaction can compose it with the rest of its writes. Cross-project ends
// are the point of entity_edges, so unlike containment there is no
// same-project check. A rel outside referenceShapes or ends of the wrong
// kind is ErrInvalidInput; a missing end is ErrNotFound naming the kind and
// id; a duplicate (from_kind, from_id, to_kind, to_id, rel) is
// ErrReferenceExists.
func CreateEntityEdge(tx *sql.Tx, now time.Time, in CreateEntityEdgeInput) (*model.EntityEdge, error) {
	shape, ok := referenceShapes[in.Rel]
	if !ok {
		return nil, fmt.Errorf("unknown reference rel %q: %w", in.Rel, ErrInvalidInput)
	}
	if in.FromKind != shape.FromKind || in.ToKind != shape.ToKind {
		return nil, fmt.Errorf("rel %q wants %s -> %s, got %s -> %s: %w",
			in.Rel, shape.FromKind, shape.ToKind, in.FromKind, in.ToKind, ErrInvalidInput)
	}

	fromOK, err := entityExists(tx, in.FromKind, in.From)
	if err != nil {
		return nil, err
	}
	if !fromOK {
		return nil, fmt.Errorf("%s %s: %w", in.FromKind, in.From, ErrNotFound)
	}
	toOK, err := entityExists(tx, in.ToKind, in.To)
	if err != nil {
		return nil, err
	}
	if !toOK {
		return nil, fmt.Errorf("%s %s: %w", in.ToKind, in.To, ErrNotFound)
	}

	ts := now.UTC().Truncate(time.Second)
	var createdBy sql.NullString
	if in.CreatedBy != "" {
		createdBy = sql.NullString{String: in.CreatedBy, Valid: true}
	}
	if _, err := tx.Exec(
		`INSERT INTO entity_edges (from_kind, from_id, to_kind, to_id, rel, created_at, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		in.FromKind, in.From, in.ToKind, in.To, in.Rel, ts, createdBy,
	); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("reference %s %s %s %s %s: %w",
				in.FromKind, in.From, in.Rel, in.ToKind, in.To, ErrReferenceExists)
		}
		return nil, fmt.Errorf("insert reference %s %s -> %s %s: %w", in.FromKind, in.From, in.ToKind, in.To, err)
	}

	return &model.EntityEdge{
		FromKind:  in.FromKind,
		From:      in.From,
		ToKind:    in.ToKind,
		To:        in.To,
		Rel:       in.Rel,
		CreatedBy: in.CreatedBy,
		CreatedAt: ts,
	}, nil
}

// scanEntityEdge reads one row selected as (from_kind, from_id, to_kind,
// to_id, rel, created_by, created_at).
func scanEntityEdge(row rowScanner) (*model.EntityEdge, error) {
	var e model.EntityEdge
	var createdBy sql.NullString
	if err := row.Scan(&e.FromKind, &e.From, &e.ToKind, &e.To, &e.Rel, &createdBy, &e.CreatedAt); err != nil {
		return nil, err
	}
	e.CreatedBy = createdBy.String
	return &e, nil
}

// ReferencesFor returns every reference touching the given entity, from
// either end, from-side matches first and otherwise in a stable order.
func (s *Store) ReferencesFor(ctx context.Context, kind, id string) ([]model.EntityEdge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_kind, from_id, to_kind, to_id, rel, created_by, created_at
		 FROM entity_edges
		 WHERE (from_kind = $1 AND from_id = $2) OR (to_kind = $1 AND to_id = $2)
		 ORDER BY (from_kind = $1 AND from_id = $2) DESC, from_kind, from_id, to_kind, to_id, rel`,
		kind, id)
	if err != nil {
		return nil, fmt.Errorf("references for %s %s: %w", kind, id, err)
	}
	out, err := collectRows(rows, fmt.Sprintf("references for %s %s", kind, id), byValue(scanEntityEdge))
	if err != nil {
		return nil, err
	}
	return nonNil(out), nil
}

// MilestoneDeliverableRefs returns the deliverables a milestone depends_on
// (029 §5), joined through deliverableFrom so each one carries its own
// reported state — the read the project Progress page needs to show a
// dependency on an unpublished deliverable as blocked, not as a bare id.
func (s *Store) MilestoneDeliverableRefs(ctx context.Context, milestoneID string) ([]model.Deliverable, error) {
	// The join is through a derived table naming only to_id: entity_edges and
	// deliverables both have a created_by/created_at column, and joining the
	// bare table into deliverableFrom's scope makes deliverableSelect's
	// unqualified column names ambiguous.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deliverableSelect+` `+deliverableFrom+`
		 JOIN (
		     SELECT to_id FROM entity_edges
		      WHERE rel = 'depends_on' AND from_kind = 'milestone' AND to_kind = 'deliverable' AND from_id = $1
		 ) ref ON ref.to_id = deliverables.id
		 ORDER BY deliverables.created_at, deliverables.id`,
		milestoneID)
	if err != nil {
		return nil, fmt.Errorf("deliverables referenced by milestone %s: %w", milestoneID, err)
	}
	out, err := collectRows(rows, fmt.Sprintf("deliverables referenced by milestone %s", milestoneID), byValue(scanDeliverable))
	if err != nil {
		return nil, err
	}
	return nonNil(out), nil
}
