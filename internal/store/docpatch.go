package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// DocPatchInput is one 025 §8.4 in-place amendment of an accepted spec or
// ADR. Body is the whole markdown source. Substantive is the caller's judged
// call, which only gets asked once the mechanical rules pass; Note is what a
// non-substantive patch records about itself (§8.5). TaskID and SessionID
// come from the worktree the caller stands in — TaskID also names the plan
// whose own claimed tasks do not block this amendment.
type DocPatchInput struct {
	ID          int64
	Body        string
	Substantive bool
	Note        string
	ActorID     string
	TaskID      string
	SessionID   string
}

// patchRefusal is a §8.3/§8.4 gate refusing an in-place amendment, carrying
// the rule that fired. The rule is what the doc.patched event payload and the
// worklode_doc_operations_total outcome need, and neither can recover it from
// a message. It wraps ErrInvalidInput like every other refusal in this
// package, so the API still answers 422.
type patchRefusal struct {
	rule string
	err  error
}

func (e *patchRefusal) Error() string { return e.err.Error() }
func (e *patchRefusal) Unwrap() error { return e.err }

// refusePatch builds a patchRefusal naming rule. The message always ends in
// where the edit goes instead: an amendment a rule refuses is a revision.
func refusePatch(rule, format string, args ...any) error {
	return &patchRefusal{
		rule: rule,
		err: fmt.Errorf(format+" (%s): revise it with `lode doc revise` instead: %w",
			append(args, rule, ErrInvalidInput)...),
	}
}

// PatchRule returns the §8.4 rule that refused a patch, or "" for any other
// error. The API reads it for the event payload; the metric reads it for the
// outcome label.
func PatchRule(err error) string {
	var r *patchRefusal
	if errors.As(err, &r) {
		return r.rule
	}
	return ""
}

// PatchDoc amends an accepted spec or ADR in place (025 §8.4): the one path
// that changes an accepted document without a revision cycle. Drafts and
// plans are edited with UpdateDocBody and a superseded document is not edited
// at all.
//
// The gates are mechanical and the server owns them (§8.3). A changed section
// that carries a wl:/wlc: term, a code surface or acceptance criteria, a
// frontmatter `requires` that grew, or a section open work already points at
// (§8.2) is refused outright — that edit is a revision. What survives is the
// caller's own judgment: a non-substantive patch records a note saying what
// changed and why, a substantive one reopens the document's reviewers on the
// new version and marks the sections it touched. The document stays accepted
// either way (§7.3).
func PatchDoc(tx *sql.Tx, now time.Time, in DocPatchInput, eventID int64) (*model.Doc, *model.DocPatchResult, error) {
	d, err := lockDoc(tx, in.ID)
	if err != nil {
		return nil, nil, err
	}
	if d.kind == "plan" {
		return nil, nil, fmt.Errorf("doc %d is a plan: plans are edited in place (025 §9): %w",
			in.ID, ErrInvalidInput)
	}
	if d.status != "accepted" {
		return nil, nil, fmt.Errorf(
			"doc %d is %s: only an accepted document is amended in place (025 §8.4): %w",
			in.ID, d.status, ErrInvalidInput)
	}
	if in.Body == d.body {
		return nil, nil, fmt.Errorf("doc %d: nothing changed: %w", in.ID, ErrInvalidInput)
	}

	old, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return nil, nil, fmt.Errorf("parse the accepted body of doc %d: %w", in.ID, err)
	}
	next, err := parseDocBody(d.kind, in.Body)
	if err != nil {
		return nil, nil, err
	}
	changed := designdoc.ChangedAnchors(old.doc, next.doc)

	prior, err := priorSections(tx, in.ID)
	if err != nil {
		return nil, nil, err
	}
	diff := designdoc.CompareSections(old.doc, next.doc, docDepthLimit)
	if err := checkAnchorFreeze(fmt.Sprintf("doc %d cannot be patched", in.ID), &diff, prior); err != nil {
		return nil, nil, err
	}

	// §8.3's text-level rules first: they read the two bodies alone, so a
	// finding is cheaper than the corpus query below.
	if f := designdoc.MechanicalFindings(old.doc, next.doc); len(f) > 0 {
		return nil, nil, refusePatch(f[0].Rule, "doc %d cannot be patched: %s", in.ID, f[0].Detail)
	}
	unexecuted, err := checkReferrers(tx, in, changed)
	if err != nil {
		return nil, nil, err
	}

	if !in.Substantive && strings.TrimSpace(in.Note) == "" {
		return nil, nil, fmt.Errorf(
			"doc %d: a non-substantive patch needs a note saying what changed and why (025 §8.5): %w",
			in.ID, ErrInvalidInput)
	}

	version := d.version + 1
	// Substantive means the reviewers who approved this document owe a
	// decision on the version this patch makes (§7.3). A document with none
	// assigned has nobody to re-approve it, so the patch is refused up front,
	// before anything is written: the honest paths are assigning reviewers or
	// revising.
	if in.Substantive {
		if err := RequestDocApproval(tx, now, in.ID, version); err != nil {
			return nil, nil, &patchRefusal{rule: "no-reviewers", err: err}
		}
	}

	if err := publishPatch(tx, now, in, d, next, version, changed, prior, eventID); err != nil {
		return nil, nil, err
	}
	if in.Substantive {
		if _, err := tx.Exec(
			`UPDATE doc_sections SET patched = true WHERE doc_id = $1 AND anchor = ANY($2::text[])`,
			in.ID, changed); err != nil {
			return nil, nil, fmt.Errorf("mark patched sections of doc %d: %w", in.ID, err)
		}
	} else if err := patchNote(tx, now, in, next, changed, eventID); err != nil {
		return nil, nil, err
	}

	doc, err := getDocTx(tx, in.ID)
	if err != nil {
		return nil, nil, err
	}
	res := &model.DocPatchResult{
		ChangedAnchors:          changed,
		Classification:          "non-substantive",
		RuleFired:               "none",
		NewVersion:              version,
		UnexecutedCoveringPlans: unexecuted,
	}
	if in.Substantive {
		// "judged", not a rule name: no mechanical rule fired — the caller
		// classified the edit itself, which is what §8.3 leaves to them.
		res.Classification, res.RuleFired = "substantive", "judged"
	}
	return doc, res, nil
}

// checkReferrers is §8.2's question asked of every section this patch
// changes: is there open work pointing at it. A referrer that is left is a
// refusal — the amendment would move text someone is already building
// against — with two exclusions the reader in docs.go cannot make, because
// they are facts about who is asking:
//
//   - the patching task itself, and every other task of the plan it was
//     minted from: a plan does not block the amendment it is executing;
//   - an accepted covering plan nobody has claimed work from, which
//     docSectionReferrers already leaves out of the blocking set. Those are
//     returned instead, to be reported and not acted on (§8.6).
func checkReferrers(tx *sql.Tx, in DocPatchInput, changed []string) ([]int64, error) {
	ctx := context.Background()
	ownPlan, err := taskPlanDoc(tx, in.TaskID)
	if err != nil {
		return nil, err
	}
	ownTasks, err := planTaskIDs(tx, ownPlan)
	if err != nil {
		return nil, err
	}

	var unexecuted []int64
	for _, anchor := range changed {
		refs, err := docSectionReferrers(ctx, tx, in.ID, anchor)
		if err != nil {
			return nil, err
		}
		for _, r := range refs {
			if r.Kind == "task" && (r.Ref == in.TaskID || ownTasks[r.Ref]) {
				continue
			}
			return nil, refusePatch("referrer",
				"doc %d cannot be patched: #%s is claimed by %s %s (%s)",
				in.ID, anchor, r.Kind, r.Ref, r.Title)
		}
		plans, err := unexecutedCoveringPlans(tx, in.ID, anchor, ownPlan)
		if err != nil {
			return nil, err
		}
		for _, p := range plans {
			if !slices.Contains(unexecuted, p) {
				unexecuted = append(unexecuted, p)
			}
		}
	}
	slices.Sort(unexecuted)
	return unexecuted, nil
}

// taskPlanDoc is the plan a task was minted from, 0 for a task bound to none
// and for an empty or unknown id — a caller standing outside a worktree is
// normal, not a refusal.
func taskPlanDoc(tx *sql.Tx, taskID string) (int64, error) {
	if taskID == "" {
		return 0, nil
	}
	var plan sql.NullInt64
	err := tx.QueryRow(`SELECT plan_doc FROM tasks WHERE id = $1`, taskID).Scan(&plan)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load plan of task %s: %w", taskID, err)
	}
	return plan.Int64, nil
}

// planTaskIDs is the id set of a plan's tasks, empty for plan 0.
func planTaskIDs(tx *sql.Tx, plan int64) (map[string]bool, error) {
	out := map[string]bool{}
	if plan == 0 {
		return out, nil
	}
	rows, err := tx.Query(`SELECT id FROM tasks WHERE plan_doc = $1`, plan)
	if err != nil {
		return nil, fmt.Errorf("load tasks of plan %d: %w", plan, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan task of plan %d: %w", plan, err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// unexecutedCoveringPlans lists the accepted plans covering one section that
// nobody has claimed work from yet — the half of §8.2 docSectionReferrers
// deliberately leaves out, since an intention is not open work. exclude drops
// the plan the patching task belongs to, which is the one plan that is not
// news to the caller.
func unexecutedCoveringPlans(tx *sql.Tx, docID int64, anchor string, exclude int64) ([]int64, error) {
	rows, err := tx.Query(
		`SELECT DISTINCT p.id
		   FROM doc_edges e
		   JOIN docs p ON p.id = e.from_doc
		  WHERE e.to_doc = $1 AND e.to_anchor = $2 AND e.type = 'covers'
		    AND p.kind = 'plan' AND p.status = 'accepted' AND p.deleted_at IS NULL
		    AND p.id <> $3
		    AND NOT EXISTS (
		          SELECT 1 FROM tasks t
		           WHERE t.plan_doc = p.id AND t.deleted_at IS NULL
		             AND t.state IN (`+claimedOpenStates+`))
		  ORDER BY p.id`,
		docID, anchor, exclude)
	if err != nil {
		return nil, fmt.Errorf("doc %d section %s covering plans: %w", docID, anchor, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan covering plan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// publishPatch lands the patched body as the document's next version: the
// same publication AcceptRevision performs, minus the candidate and the
// status move. Sections keep their existing published and patched flags
// through the rebuild, last_revised_in moves on exactly the changed anchors
// (§6 rule 5), and a section the patch added is published like the rest of
// the accepted text it now belongs to.
func publishPatch(tx *sql.Tx, now time.Time, in DocPatchInput, d lockedDoc,
	next parsedDoc, version int, changed []string, prior map[string]priorSection, eventID int64) error {

	title, ok := designdoc.Title(next.doc)
	if !ok {
		title = d.slug
	}
	if err := snapshotDocVersion(tx, in.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE docs SET body = $2, title = $3, issued = coalesce($4::date, issued),
		                 version = $5, updated_at = $6
		  WHERE id = $1`,
		in.ID, in.Body, title, nullText(next.issued), version, now.UTC().Truncate(time.Second),
	); err != nil {
		return fmt.Errorf("patch doc %d: %w", in.ID, err)
	}
	if _, err := rebuildSectionsFrom(tx, in.ID, d.kind, next.doc, version, prior); err != nil {
		return err
	}
	if len(changed) > 0 {
		if _, err := tx.Exec(
			`UPDATE doc_sections SET last_revised_in = $3
			  WHERE doc_id = $1 AND anchor = ANY($2::text[])`,
			in.ID, changed, version); err != nil {
			return fmt.Errorf("stamp last_revised_in on doc %d: %w", in.ID, err)
		}
	}
	if _, err := tx.Exec(
		`UPDATE doc_sections SET published = true WHERE doc_id = $1`, in.ID); err != nil {
		return fmt.Errorf("publish sections of doc %d: %w", in.ID, err)
	}
	if err := rebuildEdges(tx, now, in.ID, d.kind, d.project, next.doc.Frontmatter); err != nil {
		return err
	}
	return logDocChange(tx, in.ID, eventID, map[string]string{
		"field": "version",
		"old":   strconv.Itoa(d.version),
		"new":   strconv.Itoa(version),
	})
}

// patchNote records §8.5's note on a non-substantive patch: one row, anchored
// at the first section the patch changed and prefixed with all of them, so
// the reader of any one changed section can see the whole edit. It runs after
// the section rebuild, since the anchor may be a section this patch added.
//
// A patch that changed no anchored section at all — frontmatter or preamble
// prose — anchors the note at the document's first section rather than
// dropping what the fixer said. A document with no sections gets no note;
// AddDocNote would refuse it, and there is nowhere to render it.
func patchNote(tx *sql.Tx, now time.Time, in DocPatchInput, next parsedDoc,
	changed []string, eventID int64) error {

	anchor := ""
	if len(changed) > 0 {
		anchor = changed[0]
	} else {
		for _, sec := range next.doc.Sections {
			if sec.Anchor != "" {
				anchor = sec.Anchor
				break
			}
		}
	}
	if anchor == "" {
		return nil
	}
	body := in.Note
	if len(changed) > 0 {
		body = "[" + strings.Join(changed, ", ") + "] " + body
	}
	_, err := AddDocNote(tx, now, in.ID, model.AddDocNoteInput{
		Anchor: anchor, Body: body, Task: in.TaskID, Session: in.SessionID,
	}, in.ActorID, eventID)
	return err
}
