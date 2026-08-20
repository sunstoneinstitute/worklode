package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Brief is the bounded payload an agent needs to start work on a task: the
// task row, its conventional branch, what holds it (open blocker tasks and
// unfinished blocking plans), and the active lease (nil when the task is
// unleased). It is deliberately bounded — no unbounded lists — so a brief is
// one cheap, predictable read;
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
	Task         model.Task   // the task row
	Body         string       // task body (mirrors Task.Body for the wire contract)
	Branch       string       // <prefix><id>-<slug>
	OpenBlockers []model.Task // the open tasks holding this one; only ID/Title/State are populated
	// BlockingPlans are the plan documents ordered before this task's plan
	// (025 §9.3) whose work is unfinished. A blocking plan still draft has
	// minted no task, so it lands here with nothing in OpenBlockers.
	BlockingPlans      []model.DocRef
	Parent             *model.Task // the task's parent, or nil; only ID/Title/State are populated
	Lease              *Lease      // active lease, or nil
	GoverningDesign    *string     // reserved: spec 006 (nil in v1)
	AffectedComponents []string    // reserved: spec 006 (nil in v1)
	DefinitionOfDone   *string     // reserved: spec 006 Deliverable (nil in v1)
	// PinnedSkills are the task's pinned skills, content included; deleted
	// pins still resolve (with a warning) so briefs never break.
	PinnedSkills []Skill
	// SkillWarnings surface unknown/deleted pins.
	SkillWarnings []string
	// Blobs are the task's images and attachments, so an agent never has to
	// parse markdown to find them. A vision-capable agent can read the
	// screenshot the reporter actually saw; any agent can pull the log.
	Blobs []model.TaskBlob
}

// BriefOptions selects the optional work a brief does.
type BriefOptions struct {
	// Skills resolves the task's pinned skills and inlines their bodies.
	// Callers that only need the task row or the lease turn it off: pinned
	// bodies dominate the payload (a dozen pins run to hundreds of KB) and
	// cost a query and, on the API side, an embedding round trip.
	Skills bool
}

// Brief assembles the brief for taskID: the task row, its branch, what holds
// it, its parent, any active lease, and — when opts.Skills is set — its
// pinned skills. Returns ErrNotFound if the task does not exist. It runs a
// bounded, fixed number of queries — one more only when pins are asked for and
// the task has some — and never returns unbounded lists.
//
// A tombstoned task still briefs, since this is a fetch by id (044 §4): the
// brief carries Task.Tombstone, and an agent holding the id learns it was
// deleted, by whom and why, rather than "not found". What it will not do is
// hand the task to anyone — Claim refuses it, and the blocker and plan lists
// below read live rows only.
func (s *Store) Brief(ctx context.Context, taskID string, opts BriefOptions) (*Brief, error) {
	t, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}

	blockers, err := s.openBlockers(ctx, taskID)
	if err != nil {
		return nil, err
	}

	plans, err := s.blockingPlans(ctx, taskID)
	if err != nil {
		return nil, err
	}

	lease, err := s.ActiveLease(ctx, taskID)
	if errors.Is(err, ErrNotFound) {
		lease = nil
	} else if err != nil {
		return nil, err
	}

	parent, err := s.ParentOf(ctx, taskID)
	if err != nil {
		return nil, err
	}

	blobs, err := s.ListTaskBlobs(ctx, taskID)
	if err != nil {
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
		BlockingPlans: plans,
		Parent:        parent,
		Lease:         lease,
		PinnedSkills:  pinned,
		SkillWarnings: warnings,
		Blobs:         blobs,
	}, nil
}

// ResolvePins resolves pinned skill names into skills with content, in pin
// order, deduped. A pin in "plugin:skill" form resolves exactly when the
// registry name is qualified; when the exact name misses, the segment after
// the pin's first colon is tried against the registry as a fallback name —
// the skill-identifier rule of 025 §9.1. An exact hit is authoritative for
// its own name and never consults the fallback. An unknown pin produces a
// "not found" warning; a pin that resolves to a soft-deleted skill still
// comes back with its content, plus a "removed from its source repo"
// warning — a brief must never break because a skill was withdrawn or
// misspelled upstream.
//
// Dedupe is on the resolved skill, not the pin: the fallback lets two pins
// ("tdd" and "superpowers:tdd") name one registry row, and inlining that
// skill's content twice would just inflate the brief. Warnings stay per pin —
// each names the spelling the task author wrote, and a pin resolving to a
// skill another pin already brought in is not an error to report.
//
// The brief and POST /api/v1/skills/recommend both go through here, so the
// two agree on the warning text without hand-copying it.
func (s *Store) ResolvePins(ctx context.Context, pins []string) ([]Skill, []string, error) {
	names := dedupeFirst(pins)

	skills, err := s.SkillsByNames(ctx, names)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve pinned skills: %w", err)
	}
	found := make(map[string]Skill, len(skills))
	for _, sk := range skills {
		found[sk.Name] = sk
	}

	// Still-unresolved, colon-qualified pins get a second pass against the
	// segment after their first colon (deduped) — an unqualified registry
	// name a plugin-qualified pin should still hit.
	suffixOf := make(map[string]string, len(names))
	var suffixes []string
	for _, name := range names {
		if _, ok := found[name]; ok {
			continue
		}
		if _, suffix, ok := strings.Cut(name, ":"); ok {
			suffixOf[name] = suffix
			suffixes = append(suffixes, suffix)
		}
	}
	fallback := make(map[string]Skill, len(suffixes))
	if len(suffixes) > 0 {
		hits, err := s.SkillsByNames(ctx, dedupeFirst(suffixes))
		if err != nil {
			return nil, nil, fmt.Errorf("resolve pinned skills: %w", err)
		}
		for _, sk := range hits {
			fallback[sk.Name] = sk
		}
	}

	var pinned []Skill
	var warnings []string
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		sk, ok := found[name]
		if !ok {
			if suffix, has := suffixOf[name]; has {
				sk, ok = fallback[suffix]
			}
		}
		if !ok {
			warnings = append(warnings, "pinned skill not found: "+name)
			continue
		}
		if sk.Deleted {
			warnings = append(warnings, "pinned skill removed from its source repo: "+name)
		}
		if seen[sk.Name] {
			continue
		}
		seen[sk.Name] = true
		pinned = append(pinned, sk)
	}
	return pinned, warnings, nil
}

// openBlockers returns the open tasks holding taskID: the from_task of a
// 'blocks' edge pointing at it, and the open tasks of any plan ordered before
// its plan (025 §9.3). "Open" uses the same predicate as blockedCondition and
// planBlockedCondition: the blocker has not reached its repo's done_state
// (taskClosed). Only ID, Title, and State are populated (the brief surfaces no
// more than that). Ordered by numeric id for a stable payload.
//
// A blocking plan still draft has minted no task and so names none here; the
// brief reports it through blockingPlans instead.
func (s *Store) openBlockers(ctx context.Context, taskID string) ([]model.Task, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, state FROM (
		   SELECT b.id, b.title, b.state
		     FROM task_edges e
		     JOIN tasks b ON b.id = e.from_task
		    WHERE e.to_task = $1
		      AND e.type = 'blocks'
		      AND NOT `+taskClosed("b")+`
		   UNION
		   SELECT b.id, b.title, b.state
		     FROM tasks dep
		     JOIN doc_edges de ON de.type = 'blocks' AND de.to_doc = dep.plan_doc
		     JOIN tasks b ON b.plan_doc = de.from_doc
		    WHERE dep.id = $1
		      AND NOT `+taskClosed("b")+`
		 ) t
		  ORDER BY CAST(split_part(t.id, '-', 2) AS INTEGER)`, taskID)
	if err != nil {
		return nil, fmt.Errorf("open blockers of %s: %w", taskID, err)
	}
	return collectRows(rows, fmt.Sprintf("open blockers of %s", taskID), func(r rowScanner) (model.Task, error) {
		var t model.Task
		if err := r.Scan(&t.ID, &t.Title, &t.State); err != nil {
			return model.Task{}, err
		}
		return t, nil
	})
}

// blockingPlans returns the unfinished plans ordered before taskID's own plan
// (planUnfinished, the predicate planBlockedCondition gates the ready set on),
// oldest document first. It is what tells an agent *which* plan is holding a
// task the claim path refuses — including a blocking plan still draft, whose
// unminted set leaves openBlockers nothing to name.
func (s *Store) blockingPlans(ctx context.Context, taskID string) ([]model.DocRef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT bd.id, bd.slug, bd.title, bd.status
		   FROM tasks dep
		   JOIN doc_edges de ON de.type = 'blocks' AND de.to_doc = dep.plan_doc
		   JOIN docs bd ON bd.id = de.from_doc
		  WHERE dep.id = $1
		    AND `+planUnfinished("bd")+`
		  ORDER BY bd.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("blocking plans of %s: %w", taskID, err)
	}
	return collectRows(rows, fmt.Sprintf("blocking plans of %s", taskID), func(r rowScanner) (model.DocRef, error) {
		var ref model.DocRef
		if err := r.Scan(&ref.ID, &ref.Slug, &ref.Title, &ref.Status); err != nil {
			return model.DocRef{}, err
		}
		return ref, nil
	})
}
