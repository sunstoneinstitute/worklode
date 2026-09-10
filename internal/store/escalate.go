// escalate.go is the upward path of the escalation ladder (025 §8.1): an
// executor that cannot proceed hands the design defect to the tier that owns
// it, in one transaction, and stops holding the task while that happens.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// EscalateInput is one escalation. DocID names the document the caller is
// escalating against — the API resolves it from To and the task when the
// caller does not name one — and Anchor narrows it to a section ("sec-3"), ""
// for the whole document. ActorID must hold the task's active lease.
type EscalateInput struct {
	TaskID  string
	To      string // "plan" | "spec"
	DocID   int64
	Anchor  string
	Reason  string
	ActorID string
}

// EscalateResult reports what the escalation did: exactly one of Minted (the
// new design task) and Joined (the id of the open escalation it joined) is
// set.
type EscalateResult struct {
	Minted *model.Task
	Joined string
}

// EscalateTask runs 025 §8.1's five steps as one transaction: release the
// caller's lease and put the task back in `ready`; join an open design task
// already covering this document and section, or mint one; block the
// escalating task on it; record `task.gap_found`.
//
// Dedup comes before the mint, so a second executor hitting the same gap joins
// the first escalation rather than minting a rival (§8.1.5). The design task
// is assigned to whoever authored the target document, when they are on the
// project's crew (029 §6.1) — an author who is not gets an unassigned task
// rather than a failed escalation, the same trade MintAcceptDecision makes.
//
// The task ends in `ready` rather than a state of its own: 004 has no
// `blocked` state, blockedness is the 'blocks' edge, and the edge this writes
// is what keeps the task out of the ready set until the design task closes.
//
// Errors: ErrInvalidInput for a bad To, a missing document or a blank reason;
// ErrNotFound when the task, the document, or the caller's active lease on the
// task is missing.
func (s *Store) EscalateTask(ctx context.Context, in EscalateInput) (*EscalateResult, error) {
	if in.To != "plan" && in.To != "spec" {
		return nil, fmt.Errorf("escalate --to %q: must be \"plan\" or \"spec\": %w", in.To, ErrInvalidInput)
	}
	if in.DocID == 0 {
		return nil, fmt.Errorf("escalate %s: no document to escalate against: %w", in.TaskID, ErrInvalidInput)
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return nil, fmt.Errorf("escalate %s: a reason is required: %w", in.TaskID, ErrInvalidInput)
	}
	in.Reason = reason

	// The payload names the document by slug, and RecordEvent marshals it
	// before the transaction opens, so the document is read first. It also
	// tells the caller "no such document" before anything is written.
	doc, err := s.GetDoc(ctx, in.DocID)
	if err != nil {
		s.metrics.escalation("error")
		return nil, err
	}
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}
	payload, err := EventPayload(map[string]any{
		"task": in.TaskID, "doc": doc.Slug, "anchor": in.Anchor,
		"to": in.To, "reason": in.Reason,
	})
	if err != nil {
		return nil, err
	}

	var res EscalateResult
	_, _, err = s.RecordEvent(ctx, "cli", extID, "task.gap_found", payload,
		func(tx *sql.Tx, eventID int64) error {
			res = EscalateResult{}
			now := s.nowFn().UTC().Truncate(time.Second)
			if err := releaseLeaseTx(tx, now, in.TaskID, in.ActorID, eventID); err != nil {
				return err
			}
			joined, err := openEscalationFor(tx, in.DocID, in.Anchor)
			if err != nil {
				return err
			}
			if joined == "" {
				minted, err := mintEscalationTask(tx, now, in, eventID)
				if err != nil {
					return err
				}
				res.Minted, joined = minted, minted.ID
			} else {
				res.Joined = joined
			}
			// A re-escalation of a gap this task is already blocked on has
			// nothing left to add, so the existing edge stands.
			if err := AddEdge(tx, now, joined, in.TaskID, "blocks", eventID); err != nil &&
				!errors.Is(err, ErrEdgeExists) {
				return err
			}
			return nil
		})
	switch {
	case err != nil:
		s.metrics.escalation("error")
		return nil, err
	case res.Joined != "":
		s.metrics.escalation("joined")
	default:
		s.metrics.escalation("minted")
	}
	return &res, nil
}

// openEscalationFor is the §8.1.5 dedup: the oldest open design task about
// this document and section, or "". Open is taskClosed's complement and live,
// the same notion OpenTaskForDoc uses — this is that guard with the section
// added, and IS NOT DISTINCT FROM is what makes "the whole document" (a NULL
// anchor) dedup against itself rather than against every section.
func openEscalationFor(tx *sql.Tx, docID int64, anchor string) (string, error) {
	var id string
	err := tx.QueryRow(
		`SELECT id FROM tasks
		  WHERE about_doc = $1 AND about_anchor IS NOT DISTINCT FROM $2
		    AND kind = 'design' AND deleted_at IS NULL
		    AND NOT `+taskClosed("tasks")+`
		  ORDER BY created_at, id LIMIT 1`, docID, nullText(anchor),
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open escalation for doc %d anchor %q: %w", docID, anchor, err)
	}
	return id, nil
}

// mintEscalationTask creates the design task the escalation asks for and
// assigns it to the document's author. The document row is re-read here, under
// the transaction, for the project and the author the mint needs.
func mintEscalationTask(tx *sql.Tx, now time.Time, in EscalateInput, eventID int64) (*model.Task, error) {
	var d model.Doc
	var projectID, createdBy, owner string
	err := tx.QueryRow(
		`SELECT d.project_id, d.kind, coalesce(d.number, 0), coalesce(p.key, ''),
		        coalesce(d.created_by, ''), coalesce(d.owner, '')
		   FROM docs d JOIN projects p ON p.id = d.project_id
		  WHERE d.id = $1 AND d.deleted_at IS NULL`, in.DocID,
	).Scan(&projectID, &d.Kind, &d.Number, &d.ProjectKey, &createdBy, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d: %w", in.DocID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read escalation target %d: %w", in.DocID, err)
	}

	where := d.FormatRef()
	if in.Anchor != "" {
		where += " §" + strings.TrimPrefix(in.Anchor, "sec-")
	}
	t, err := CreateTask(tx, now, TaskInput{
		ProjectID: projectID,
		Title:     "Gap in " + where,
		Body: fmt.Sprintf("Escalated from %s: the %s does not cover the case in front of the executor (025 §8.1).\n\n%s",
			in.TaskID, in.To, in.Reason),
		Kind:        "design",
		Priority:    "high",
		AboutDoc:    in.DocID,
		AboutAnchor: in.Anchor,
		CreatedBy:   in.ActorID,
	}, eventID)
	if err != nil {
		return nil, err
	}
	// §8.1.4 puts the fix in the author's queue. created_by is who wrote it;
	// owner is the fallback for a document whose author predates the column or
	// has since handed it on.
	for _, candidate := range []string{createdBy, owner} {
		if candidate == "" {
			continue
		}
		crew, err := isCrewMember(tx, projectID, candidate)
		if err != nil {
			return nil, err
		}
		if !crew {
			continue
		}
		if err := AssignTask(tx, now, t.ID, candidate, eventID); err != nil {
			return nil, err
		}
		t.Assignee = candidate
		break
	}
	return t, nil
}
