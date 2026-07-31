package store

import (
	"context"
	"errors"
	"fmt"
)

// Brief is the bounded payload an agent needs to start work on a task: the
// task row, its conventional branch, the open blockers still pointing at it,
// and the active lease (nil when the task is unleased). It is deliberately
// bounded — no unbounded lists — so a brief is one cheap, predictable read;
// pinned SKILL.md bodies are the one deliberate exception, budget-bounded by
// the pin list the task author wrote.
//
// GoverningDesign, AffectedComponents, and DefinitionOfDone are reserved for
// spec 006 (Deliverable/design links) and stay nil in v1; the shape is fixed
// now so the wire contract does not change when they are populated.
//
// Parent is exactly one hop up — an agent should know its task belongs to
// "Delivery lifecycle" without spelunking, while the full ancestry and the
// sibling list are both unbounded and stay out.
type Brief struct {
	Task               Task     // the task row
	Body               string   // task body (mirrors Task.Body for the wire contract)
	Branch             string   // <prefix><id>-<slug>
	OpenBlockers       []Task   // open 'blocks' edges pointing at this task; only ID/Title/State are populated
	Parent             *Task    // the task's epic, or nil; only ID/Title/State are populated
	Lease              *Lease   // active lease, or nil
	GoverningDesign    *string  // reserved: spec 006 (nil in v1)
	AffectedComponents []string // reserved: spec 006 (nil in v1)
	DefinitionOfDone   *string  // reserved: spec 006 Deliverable (nil in v1)
	// PinnedSkills are the task's pinned skills, content included; deleted
	// pins still resolve (with a warning) so briefs never break.
	PinnedSkills []Skill
	// SkillWarnings surface unknown/deleted pins.
	SkillWarnings []string
}

// BriefOptions selects the optional work a brief does.
type BriefOptions struct {
	// Skills resolves the task's pinned skills and inlines their bodies.
	// Callers that only need the task row or the lease turn it off: pinned
	// bodies dominate the payload (a dozen pins run to hundreds of KB) and
	// cost a query and, on the API side, an embedding round trip.
	Skills bool
}

// Brief assembles the brief for taskID: the task row, its branch, its open
// blockers, any active lease, and — when opts.Skills is set — its pinned
// skills. Returns ErrNotFound if the task does not exist. It runs a bounded,
// fixed number of queries — one more only when pins are asked for and the
// task has some — and never returns unbounded lists.
func (s *Store) Brief(ctx context.Context, taskID string, opts BriefOptions) (*Brief, error) {
	t, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}

	blockers, err := s.openBlockers(ctx, taskID)
	if err != nil {
		return nil, err
	}

	lease, err := s.ActiveLease(ctx, taskID)
	if errors.Is(err, ErrNotFound) {
		lease = nil
	} else if err != nil {
		return nil, err
	}

	var pinned []Skill
	var warnings []string
	if opts.Skills && len(t.Skills) > 0 {
		pinned, warnings, err = s.ResolvePins(ctx, t.Skills)
		if err != nil {
			return nil, err
		}
	}

	return &Brief{
		Task:          *t,
		Body:          t.Body,
		Branch:        BranchFor(t),
		OpenBlockers:  blockers,
		Lease:         lease,
		PinnedSkills:  pinned,
		SkillWarnings: warnings,
	}, nil
}

// ResolvePins resolves pinned skill names into skills with content, in pin
// order, deduped. An unknown pin produces a "not found" warning; a pin that
// resolves to a soft-deleted skill still comes back with its content, plus a
// "removed from its source repo" warning — a brief must never break because
// a skill was withdrawn or misspelled upstream.
//
// The brief and POST /api/v1/skills/recommend both go through here, so the
// two agree on the warning text without hand-copying it.
func (s *Store) ResolvePins(ctx context.Context, pins []string) ([]Skill, []string, error) {
	skills, err := s.SkillsByNames(ctx, pins)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve pinned skills: %w", err)
	}
	found := make(map[string]Skill, len(skills))
	for _, sk := range skills {
		found[sk.Name] = sk
	}

	var pinned []Skill
	var warnings []string
	for _, name := range dedupeFirst(pins) {
		sk, ok := found[name]
		if !ok {
			warnings = append(warnings, "pinned skill not found: "+name)
			continue
		}
		if sk.Deleted {
			warnings = append(warnings, "pinned skill removed from its source repo: "+name)
		}
		pinned = append(pinned, sk)
	}
	return pinned, warnings, nil
}

// openBlockers returns the tasks that are the from_task of an open 'blocks'
// edge whose to_task is taskID — i.e. the blockers still blocking it. "Open"
// uses the same predicate as blockedCondition: the blocker's state is not one
// of closedStates. Only ID, Title, and State are populated (the brief surfaces no
// more than that). Ordered by numeric id for a stable payload.
func (s *Store) openBlockers(ctx context.Context, taskID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.title, t.state
		   FROM task_edges e
		   JOIN tasks t ON t.id = e.from_task
		  WHERE e.to_task = $1
		    AND e.type = 'blocks'
		    AND t.state NOT IN `+closedStates+`
		  ORDER BY CAST(split_part(t.id, '-', 2) AS INTEGER)`, taskID)
	if err != nil {
		return nil, fmt.Errorf("open blockers of %s: %w", taskID, err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.State); err != nil {
			return nil, fmt.Errorf("scan open blocker: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("open blockers of %s: %w", taskID, err)
	}
	return out, nil
}
