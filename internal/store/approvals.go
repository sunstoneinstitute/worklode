// Spec 029 §7.1: one approvals table for every entity that needs a
// human decision. Functions here are tx-scoped so the GitHub review ingest
// and the web decide handler both call them inside a RecordEvent
// transaction (021 §4).

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Approval is one row of the approvals table. It is model.Approval: the row
// crosses the HTTP boundary on GET /api/v1/approvals, so it is declared once,
// in internal/model (ADR 036 §2), and scanned into directly here.
type Approval = model.Approval

// PREntityID renders the approvals entity_id for a pull request: the webhook
// ingest and the queue reader must share this one spelling.
func PREntityID(repo string, number int64) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

// DocEntityID renders the approvals entity_id for a document: "doc:" plus the
// docs.id. Same contract as PREntityID — writer and reader share one spelling
// — and it stays parseable back to the id, which is what lets the queue query
// correlate a row to its docs row (and its project) in SQL. The spelling
// itself lives in internal/model, where the CLI can reach it too.
func DocEntityID(docID int64) string {
	return model.DocEntityID(docID)
}

// InsertAwaitingApproval materializes one requirement as an 'awaiting' row,
// idempotent on the whole unique key: entity_kind, entity_id,
// subject_revision, lane and review_kind. This writer creates ordinary
// reviews, so review_kind takes its 'review' default. Two reviewer lanes on
// one document revision coexist; a redelivered or reopened PR, which rewrites
// the same no-lane row, still does not duplicate the requirement. Returns
// whether a row was inserted, so materialization can count and event payloads
// can say what actually changed.
func InsertAwaitingApproval(tx *sql.Tx, now time.Time,
	entityKind, entityID, subjectRevision, lane string,
	requiredRole, requiredActor, createdBy *string) (bool, error) {
	res, err := tx.Exec(
		`INSERT INTO approvals
		   (entity_kind, entity_id, subject_revision, required_role,
		    required_actor, lane, state, created_at, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, 'awaiting', $7, $8)
		 ON CONFLICT (entity_kind, entity_id, subject_revision, lane, review_kind) DO NOTHING`,
		entityKind, entityID, subjectRevision, requiredRole, requiredActor, lane,
		now.UTC(), createdBy)
	if err != nil {
		return false, fmt.Errorf("insert awaiting approval %s %s@%s lane %q: %w",
			entityKind, entityID, subjectRevision, lane, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert awaiting approval %s %s@%s lane %q: %w",
			entityKind, entityID, subjectRevision, lane, err)
	}
	return n > 0, nil
}

// RequestDocApproval materializes 025 §7.3's durable reviewer set (WL-359:
// doc_reviewers, assigned separately via SetDocReviewers) for one document
// revision: one 'awaiting' row per assigned reviewer, all on the same
// subject_revision (the docs.version the reviewers see), which is exactly the
// shape migration 0064's per-lane unique key permits. Re-running it for the
// same version is a no-op, and a reviewer assigned later gets only the new
// lane the next time this runs.
//
// 029 §7.2's role-scoped lanes (a flow requiring "someone in this group")
// call InsertAwaitingApproval directly with a required_role: that assignment
// is project policy, not a document's own reviewer set, and the two lane
// kinds coexist on one revision under 0064's key without conflict.
func RequestDocApproval(tx *sql.Tx, now time.Time, docID int64, version int) error {
	reviewers, err := docReviewers(tx, docID)
	if err != nil {
		return err
	}
	if len(reviewers) == 0 {
		return fmt.Errorf("%w: doc %d has no assigned reviewers; set them with `lode doc set reviewers` first", ErrInvalidInput, docID)
	}
	entityID := DocEntityID(docID)
	revision := strconv.Itoa(version)
	for _, r := range reviewers {
		// The reviewer is the lane: one reviewer owes one decision on this
		// revision, which is exactly the row 0064's key keeps distinct.
		if _, err := InsertAwaitingApproval(tx, now, "doc", entityID, revision,
			r, nil, &r, nil); err != nil {
			return err
		}
	}
	return nil
}

// DefaultSubjectRevision returns the revision an ad-hoc requirement binds to
// when the caller names none: a document's current version, "" for every
// other kind — nothing else carries a revision the backbone owns.
//
// It doubles as the existence check the ad-hoc route needs: ErrNotFound when
// no row under kind has entityID, ErrInvalidInput for a kind outside
// model.ApprovalEntityKinds. Each arm matches entity_id the way the writer
// spells it, the same correlation approvalEntityJoins states.
func DefaultSubjectRevision(tx *sql.Tx, kind, entityID string) (string, error) {
	if kind == "doc" {
		var version int
		err := tx.QueryRow(`SELECT version FROM docs WHERE 'doc:' || id = $1`,
			entityID).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", fmt.Errorf("current version of %s: %w", entityID, err)
		}
		return strconv.Itoa(version), nil
	}
	var query string
	switch kind {
	case "deliverable":
		query = `SELECT 1 FROM deliverables WHERE id = $1`
	case "task":
		query = `SELECT 1 FROM tasks WHERE id = $1`
	case "pr":
		query = `SELECT 1 FROM pull_requests WHERE ` + prEntityIDSQL + ` = $1`
	default:
		return "", fmt.Errorf("%w: entity_kind %q is not one of %v",
			ErrInvalidInput, kind, model.ApprovalEntityKinds)
	}
	var one int
	err := tx.QueryRow(query, entityID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("look up %s %s: %w", kind, entityID, err)
	}
	return "", nil
}

// ApprovalByKey loads the row migration 0064's unique key names — entity
// kind, entity id, subject revision and lane — so a caller that has just run
// InsertAwaitingApproval can return the row whether or not its own insert
// won. ErrNotFound when absent.
func ApprovalByKey(tx *sql.Tx, entityKind, entityID, subjectRevision, lane string) (*Approval, error) {
	a, err := scanApproval(tx.QueryRow(
		`SELECT `+approvalColumns+` FROM approvals
		 WHERE entity_kind = $1 AND entity_id = $2
		   AND subject_revision = $3 AND lane = $4`,
		entityKind, entityID, subjectRevision, lane))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("approval for %s %s@%s lane %q: %w",
			entityKind, entityID, subjectRevision, lane, err)
	}
	return a, nil
}

// scanString reads a single-column string row — the shared scan collectRows
// takes for every one-column query in this file.
func scanString(row rowScanner) (string, error) {
	var s string
	err := row.Scan(&s)
	return s, err
}

// docReviewers returns doc's durable reviewer set (WL-359), in assignment
// order.
func docReviewers(tx *sql.Tx, docID int64) ([]string, error) {
	rows, err := tx.Query(
		`SELECT actor_id FROM doc_reviewers WHERE doc_id = $1 ORDER BY assigned_at, actor_id`,
		docID)
	if err != nil {
		return nil, fmt.Errorf("load reviewers for doc %d: %w", docID, err)
	}
	return collectRows(rows, fmt.Sprintf("load reviewers for doc %d", docID), scanString)
}

// docReviewersCtx is docReviewers for a caller with a context and no open
// transaction — GetDoc's shape, not RequestDocApproval's.
func (s *Store) docReviewersCtx(ctx context.Context, docID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT actor_id FROM doc_reviewers WHERE doc_id = $1 ORDER BY assigned_at, actor_id`,
		docID)
	if err != nil {
		return nil, fmt.Errorf("load reviewers for doc %d: %w", docID, err)
	}
	return collectRows(rows, fmt.Sprintf("load reviewers for doc %d", docID), scanString)
}

// SetDocReviewers replaces doc's durable reviewer set wholesale (025 §7.3):
// "who reviews stays a social choice", decided once per change the way a
// PR's reviewer list is, not accumulated a name at a time — so this is a
// replace, with no separate add/remove verb. The set is not versioned: it
// survives an accept/revise cycle, which is what lets a review task minted
// for a §8.2 in-place amendment name "the original approvers" (§7.3) without
// the caller having to re-assign them.
//
// The current owner or an admin may set it — the same authority
// TransferDocOwner checks — since who reviews a document is the same kind of
// call as who owns it: the author's, not a role's.
func SetDocReviewers(tx *sql.Tx, now time.Time, docID int64, actorID string, reviewers []string, eventID int64) error {
	d, err := lockDoc(tx, docID)
	if err != nil {
		return err
	}
	if err := checkDocOwnerOrAdmin(tx, docID, d.owner, actorID); err != nil {
		return err
	}
	for _, r := range reviewers {
		// required_actor (via doc_reviewers.actor_id) is an FK to actors, so
		// an unknown reviewer would otherwise surface as a constraint
		// violation — a 500 naming a constraint instead of the name the
		// caller got wrong.
		if err := checkActorExists(tx, r); err != nil {
			return err
		}
	}
	old, err := docReviewers(tx, docID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM doc_reviewers WHERE doc_id = $1`, docID); err != nil {
		return fmt.Errorf("clear reviewers for doc %d: %w", docID, err)
	}
	for _, r := range reviewers {
		if _, err := tx.Exec(
			`INSERT INTO doc_reviewers (doc_id, actor_id, assigned_at) VALUES ($1, $2, $3)`,
			docID, r, now.UTC()); err != nil {
			return fmt.Errorf("assign reviewer %s to doc %d: %w", r, docID, err)
		}
	}
	return logDocChange(tx, docID, eventID, map[string]string{
		"field": "reviewers",
		"old":   strings.Join(old, ","),
		"new":   strings.Join(reviewers, ","),
	})
}

// DocReviewersAwaiting returns the reviewer ids doc's current version still
// owes an approval from — 025 §7.3's "who still owes a review on this
// document" as a query, oldest lane first. A document's reviewer set is
// several open lanes on purpose, unlike the PR ingest's single no-lane row,
// so this reads every open one rather than the newest.
func (s *Store) DocReviewersAwaiting(ctx context.Context, docID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT required_actor FROM approvals
		 WHERE entity_kind = 'doc' AND entity_id = $1
		   AND state IN ('awaiting', 'changes_requested')
		   AND required_actor IS NOT NULL
		 ORDER BY id`,
		DocEntityID(docID))
	if err != nil {
		return nil, fmt.Errorf("reviewers awaiting for doc %d: %w", docID, err)
	}
	return collectRows(rows, fmt.Sprintf("reviewers awaiting for doc %d", docID), scanString)
}

// checkDocReviewerGate is AcceptDoc's mechanical multi-approval gate over the
// stored reviewer set (025 §7.3): every reviewer WL-359's doc_reviewers
// names for id must hold an 'approved' approvals row at version, or the
// accept is refused naming who is still owed a decision. A reviewer with no
// row at all, an 'awaiting' row, or a 'changes_requested' row all read the
// same here — not yet approved — so there is no separate "anything still
// open" check to drift from this one. A row at an older version never
// blocks: RequestDocApproval opens a fresh set of lanes on every revision,
// and the version bump superseded whatever came before. An empty reviewer
// set is a no-op, keeping the owner-only behavior from before this gate
// existed — nothing already in flight is trapped by it.
func checkDocReviewerGate(tx *sql.Tx, id int64, version int) error {
	reviewers, err := docReviewers(tx, id)
	if err != nil {
		return err
	}
	if len(reviewers) == 0 {
		return nil
	}
	entityID := DocEntityID(id)
	revision := strconv.Itoa(version)
	states := make(map[string]string, len(reviewers))
	for _, r := range reviewers {
		a, err := ApprovalByKey(tx, "doc", entityID, revision, r)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		states[r] = a.State
	}
	return reviewerGateRefusal(id, reviewers, states)
}

// checkDocReviewerGateCtx is checkDocReviewerGate for CheckDocAcceptable's
// pre-flight, which reads outside a transaction — so the two cannot answer
// differently about whether an accept would be refused.
func (s *Store) checkDocReviewerGateCtx(ctx context.Context, id int64, version int) error {
	reviewers, err := s.docReviewersCtx(ctx, id)
	if err != nil {
		return err
	}
	if len(reviewers) == 0 {
		return nil
	}
	entityID := DocEntityID(id)
	revision := strconv.Itoa(version)
	rows, err := s.db.QueryContext(ctx,
		`SELECT lane, state FROM approvals
		 WHERE entity_kind = 'doc' AND entity_id = $1 AND subject_revision = $2
		   AND lane = ANY($3)`,
		entityID, revision, reviewers)
	if err != nil {
		return fmt.Errorf("reviewer approvals for doc %d: %w", id, err)
	}
	states := make(map[string]string, len(reviewers))
	for rows.Next() {
		var lane, state string
		if err := rows.Scan(&lane, &state); err != nil {
			rows.Close()
			return fmt.Errorf("reviewer approvals for doc %d: %w", id, err)
		}
		states[lane] = state
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reviewer approvals for doc %d: %w", id, err)
	}
	return reviewerGateRefusal(id, reviewers, states)
}

// reviewerGateRefusal builds checkDocReviewerGate's refusal from each
// reviewer's approval state ("" when none is on file at this version):
// anything but "approved" counts as missing, named in reviewer (assignment)
// order. nil once nothing is missing.
func reviewerGateRefusal(id int64, reviewers []string, states map[string]string) error {
	var missing []string
	for _, r := range reviewers {
		if states[r] != "approved" {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("doc %d is missing review approval from %s; see /reviews: %w: %w",
		id, strings.Join(missing, ", "), ErrMissingApprovals, ErrForbidden)
}

// checkActorExists returns ErrInvalidInput naming id when no actor has it.
func checkActorExists(tx *sql.Tx, id string) error {
	var exists bool
	err := tx.QueryRow(`SELECT true FROM actors WHERE id = $1`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: no actor %q", ErrInvalidInput, id)
	}
	if err != nil {
		return fmt.Errorf("look up actor %s: %w", id, err)
	}
	return nil
}

// approvalColumns is the SELECT list scanApproval expects, in order.
// review_kind, note and exception_authorized_by are migration 0075's columns.
const approvalColumns = `id, entity_kind, entity_id, subject_revision, lane,
	required_role, required_actor, resolving_actor, state, review_kind, note,
	exception_authorized_by, created_at, created_by, resolved_at`

func scanApproval(row rowScanner) (*Approval, error) {
	var a Approval
	err := row.Scan(&a.ID, &a.EntityKind, &a.EntityID, &a.SubjectRevision,
		&a.Lane, &a.RequiredRole, &a.RequiredActor, &a.ResolvingActor, &a.State,
		&a.ReviewKind, &a.Note, &a.ExceptionAuthorizedBy,
		&a.CreatedAt, &a.CreatedBy, &a.ResolvedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// OpenApprovalForLane returns the open row — state 'awaiting' or
// 'changes_requested', both counting as open — for one lane of one entity;
// ErrNotFound otherwise. It replaces OpenApprovalForEntity: with 029 §7.2's
// lanes, "the" open row of an entity is no longer a single thing. The PR
// ingest reads lane "". ORDER BY id DESC is a deterministic tiebreak, not a
// selector: the key keeps at most one open row per lane in practice.
func OpenApprovalForLane(tx *sql.Tx, entityKind, entityID, lane string) (*Approval, error) {
	a, err := scanApproval(tx.QueryRow(
		`SELECT `+approvalColumns+` FROM approvals
		 WHERE entity_kind = $1 AND entity_id = $2 AND lane = $3
		   AND state IN ('awaiting', 'changes_requested')
		 ORDER BY id DESC LIMIT 1`,
		entityKind, entityID, lane))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open approval for %s %s lane %q: %w",
			entityKind, entityID, lane, err)
	}
	return a, nil
}

// OpenApprovalsForEntity lists every open row of one entity, in lane order —
// the whole set of decisions it is still waiting on.
func OpenApprovalsForEntity(tx *sql.Tx, entityKind, entityID string) ([]Approval, error) {
	rows, err := tx.Query(
		`SELECT `+approvalColumns+` FROM approvals
		 WHERE entity_kind = $1 AND entity_id = $2
		   AND state IN ('awaiting', 'changes_requested')
		 ORDER BY lane, id`,
		entityKind, entityID)
	if err != nil {
		return nil, fmt.Errorf("open approvals for %s %s: %w", entityKind, entityID, err)
	}
	return collectRows(rows, fmt.Sprintf("open approvals for %s %s", entityKind, entityID),
		byValue(scanApproval))
}

// ResolveApproval stamps state, resolving_actor, resolved_at. Shared by the
// review ingest and the web act, so the two resolution paths cannot drift.
// There is no state guard here: callers reach the row through
// OpenApprovalForLane (or their own open-state check) first.
func ResolveApproval(tx *sql.Tx, id int64, state string,
	resolvingActor *string, at time.Time) error {
	_, err := tx.Exec(
		`UPDATE approvals SET state = $1, resolving_actor = $2, resolved_at = $3
		 WHERE id = $4`,
		state, resolvingActor, at.UTC(), id)
	if err != nil {
		return fmt.Errorf("resolve approval %d: %w", id, err)
	}
	return nil
}

// DecideInput is one decision on an approval, as the web act submits it.
// Groups is the decider's stored groups claim, carried from the session
// subject: 029 §7.3 gates the act on a session precisely so it is no older
// than the login that refreshed it.
type DecideInput struct {
	ApprovalID int64
	Decision   string // approve | request_changes | reject
	ActorID    string // the deciding actor; never "" (requireSession holds)
	Groups     []string
	Now        time.Time
}

// DecideApproval records a decision on an open approval, composing the pure
// rules in approval_rules.go with ResolveApproval. It enforces, in order:
// the row exists (ErrNotFound); it is open — awaiting or changes_requested
// (ErrApprovalResolved); it names a subject_revision (ErrNoRevision); the
// decider holds the group required_role names (ErrNotQualified); and the
// decider did not author the thing under review (ErrSelfApproval). Then it
// resolves the row.
//
// Self-approval is refused by default (029 §7.1). The one exception is
// SelfReviewExceptionValid: the effective policy permits self-review and a
// different actor authorized the exception before review. SelfReviewAllowed
// is false for every project today, so the refusal is unconditional in
// practice — see its doc comment. An unknown author on either side proves
// nothing, so it does not refuse — see IsSelfApproval. The author comparison
// happens in the entity's own namespace (authorAndActorForEntity), while the
// exception's authorizer is always an actor id, so that check compares
// against in.ActorID rather than the comparison identity.
//
// The row is locked FOR UPDATE, so two concurrent decisions serialize and the
// second sees the resolved state rather than overwriting it: ResolveApproval
// itself has no state guard.
func DecideApproval(tx *sql.Tx, in DecideInput) (*Approval, error) {
	state, ok := DecisionState(in.Decision)
	if !ok {
		return nil, fmt.Errorf("%w: unknown decision %q", ErrInvalidInput, in.Decision)
	}
	if in.ActorID == "" {
		return nil, fmt.Errorf("%w: a decision needs a deciding actor", ErrInvalidInput)
	}

	a, err := scanApproval(tx.QueryRow(
		`SELECT `+approvalColumns+` FROM approvals WHERE id = $1 FOR UPDATE`,
		in.ApprovalID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load approval %d: %w", in.ApprovalID, err)
	}
	if a.State != "awaiting" && a.State != "changes_requested" {
		return nil, ErrApprovalResolved
	}
	if a.SubjectRevision == "" {
		return nil, ErrNoRevision
	}
	if !QualifiedForRole(a.RequiredRole, in.Groups) {
		return nil, ErrNotQualified
	}
	if a.ReviewKind == "impact" {
		// 029 §7.1 puts the impact question to a qualified prior approver:
		// the person whose own approval is at stake. Self-approval does not
		// apply — the decider is deciding their own past decision by design.
		history, err := ListApprovalsForEntity(tx, a.EntityKind, a.EntityID)
		if err != nil {
			return nil, err
		}
		if !PriorApprover(history, in.ActorID) {
			return nil, ErrNotPriorApprover
		}
	} else {
		author, decider, err := authorAndActorForEntity(tx, a.EntityKind, a.EntityID, in.ActorID)
		if err != nil {
			return nil, err
		}
		if IsSelfApproval(author, decider) {
			allowed, err := SelfReviewAllowed(tx, a.EntityKind, a.EntityID)
			if err != nil {
				return nil, err
			}
			if !SelfReviewExceptionValid(allowed, a.ExceptionAuthorizedBy, in.ActorID) {
				return nil, ErrSelfApproval
			}
		}
	}

	if err := ResolveApproval(tx, a.ID, state, &in.ActorID, in.Now); err != nil {
		return nil, err
	}
	if err := clearPatchedOnFullApproval(tx, a, state); err != nil {
		return nil, err
	}
	if a.ReviewKind == "impact" && ImpactDecisionEffect(state) == ImpactReopen {
		if err := reopenDependentReview(tx, in.Now, a); err != nil {
			return nil, err
		}
	}
	a.State = state
	a.ResolvingActor = &in.ActorID
	resolvedAt := in.Now.UTC()
	a.ResolvedAt = &resolvedAt
	return a, nil
}

// reopenDependentReview is ImpactReopen's side effect (029 §7.1): the prior
// approver says their decision no longer holds, so the dependent goes back
// into review at the revision that approval bound.
//
// The insert is what the spec asks for, and it lands whenever the approved
// row sits in a named reviewer lane. On an unlaned row — every PR, which is
// what governed references are recorded from today — the awaiting row would
// occupy the approved row's own unique key (entity_kind, entity_id,
// subject_revision, lane, review_kind), so the insert is absorbed and the
// approved row itself is reopened instead. Either way the dependent ends with
// one open review row at that revision, which is the outcome the reopen
// exists for.
func reopenDependentReview(tx *sql.Tx, now time.Time, impact *Approval) error {
	approved, err := approvedReviewFor(tx, impact.EntityKind, impact.EntityID)
	if err != nil || approved == nil {
		return err
	}
	inserted, err := InsertAwaitingApproval(tx, now, impact.EntityKind, impact.EntityID,
		approved.SubjectRevision, "", approved.RequiredRole, approved.RequiredActor, nil)
	if err != nil || inserted {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE approvals SET state = 'awaiting', resolving_actor = NULL,
		   resolved_at = NULL
		 WHERE id = $1`, approved.ID); err != nil {
		return fmt.Errorf("reopen review %d after an impact decision: %w", approved.ID, err)
	}
	return nil
}

// clearPatchedOnFullApproval is 025 §7.3's other half: the §8.4 patched marks
// come off a document once its reviewers have settled the version that
// carries them. It runs on a document row only, after the decision is
// recorded, and clears nothing until every lane on that revision reads
// 'approved' — an open lane means someone still owes a decision, and a
// rejected one means the amendment did not pass, which is not a state the
// last approver's decision can undo.
//
// It lives here rather than in the web handler because it is a consequence of
// the decision, like the self-approval and qualification checks above it, and
// this is the one place a decision is recorded.
func clearPatchedOnFullApproval(tx *sql.Tx, a *Approval, state string) error {
	if a.EntityKind != "doc" || state != "approved" {
		return nil
	}
	docID, ok := model.DocIDFromEntityID(a.EntityID)
	if !ok {
		return nil
	}
	version, err := strconv.Atoi(a.SubjectRevision)
	if err != nil {
		// A document revision is always docs.version; anything else was
		// written by an ad-hoc requester and names no version to clear at.
		return nil
	}
	var unsettled int
	if err := tx.QueryRow(
		`SELECT count(*) FROM approvals
		  WHERE entity_kind = 'doc' AND entity_id = $1 AND subject_revision = $2
		    AND state <> 'approved'`,
		a.EntityID, a.SubjectRevision).Scan(&unsettled); err != nil {
		return fmt.Errorf("count open lanes on doc %d@%d: %w", docID, version, err)
	}
	if unsettled > 0 {
		return nil
	}
	return ClearPatchedSections(tx, docID, version)
}

// prEntityIDSQL renders a pull_requests row's approvals entity_id in SQL,
// unaliased (no "pr." table prefix); prAuthorForEntity uses it directly.
const prEntityIDSQL = `repo || '#' || number`

// prAuthorForEntity returns the GitHub login that opened the pull request
// entityID names; "" when the column is NULL (a row ingested before the
// column existed) or no PR matches. "" never counts as a match, so an unknown
// author cannot be read as self-approval.
func prAuthorForEntity(tx *sql.Tx, entityID string) (string, error) {
	var author string
	err := tx.QueryRow(
		`SELECT coalesce(author, '') FROM pull_requests WHERE `+prEntityIDSQL+` = $1`,
		entityID).Scan(&author)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("pr author for %s: %w", entityID, err)
	}
	return author, nil
}

// authorActorForEntity returns the actor id that created what entityID names
// under kind: docs, deliverables and tasks all record it as created_by. "" on
// no row, a NULL column, or a kind with no created_by to read — and "" never
// counts as a match, so an unknown author cannot be read as self-approval.
//
// Each arm matches entity_id the way the writer spelled it (DocEntityID's
// "doc:<id>", a bare id elsewhere), the same correlation approvalEntityJoins
// states for the queue readers.
func authorActorForEntity(tx *sql.Tx, kind, entityID string) (string, error) {
	var query string
	switch kind {
	case "doc":
		query = `SELECT coalesce(created_by, '') FROM docs WHERE 'doc:' || id = $1`
	case "deliverable":
		query = `SELECT coalesce(created_by, '') FROM deliverables WHERE id = $1`
	case "task":
		query = `SELECT coalesce(created_by, '') FROM tasks WHERE id = $1`
	default:
		return "", nil
	}
	var author string
	err := tx.QueryRow(query, entityID).Scan(&author)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("author for %s %s: %w", kind, entityID, err)
	}
	return author, nil
}

// gitHubLoginForActor returns actorID's expected_github_login; "" when the
// actor names none. The inverse of ActorIDForGitHubLogin.
func gitHubLoginForActor(tx *sql.Tx, actorID string) (string, error) {
	var login string
	err := tx.QueryRow(
		`SELECT coalesce(expected_github_login, '') FROM actors WHERE id = $1`,
		actorID).Scan(&login)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("github login for actor %s: %w", actorID, err)
	}
	return login, nil
}

// authorAndActorForEntity returns the entity's author and actorID rendered in
// the same namespace, so IsSelfApproval can compare them: a 'pr' row records
// its author as the GitHub login that opened it, every other kind records an
// actor id. Either side may come back "" — an unknown author proves nothing,
// and IsSelfApproval refuses to match on it.
func authorAndActorForEntity(tx *sql.Tx, kind, entityID, actorID string) (string, string, error) {
	if kind == "pr" {
		author, err := prAuthorForEntity(tx, entityID)
		if err != nil {
			return "", "", err
		}
		login, err := gitHubLoginForActor(tx, actorID)
		return author, login, err
	}
	author, err := authorActorForEntity(tx, kind, entityID)
	return author, actorID, err
}

// projectForApproval returns the project owning what an approval governs; ""
// when nothing correlates (a PR with no task, a kind with no project column).
// A 'pr' reaches its project through the task its PR is correlated to; every
// other kind carries project_id on its own row.
func projectForApproval(tx *sql.Tx, kind, entityID string) (string, error) {
	var query string
	switch kind {
	case "pr":
		query = `SELECT coalesce(t.project_id, '') FROM pull_requests p
		           JOIN tasks t ON t.id = p.task_id
		          WHERE p.repo || '#' || p.number = $1`
	case "doc":
		query = `SELECT coalesce(project_id, '') FROM docs WHERE 'doc:' || id = $1`
	case "deliverable":
		query = `SELECT coalesce(project_id, '') FROM deliverables WHERE id = $1`
	case "task":
		query = `SELECT coalesce(project_id, '') FROM tasks WHERE id = $1`
	default:
		return "", nil
	}
	var projectID string
	err := tx.QueryRow(query, entityID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("project for %s %s: %w", kind, entityID, err)
	}
	return projectID, nil
}

// SelfReviewAllowed reports whether the effective review policy lets an author
// decide their own work (029 §7.1's "the effective review policy allows it").
//
// It returns false for every project today, and that is not a stub oversight:
// §7.1 requires the allowance, but §7.2 defines a flow as declaring only which
// entity kinds need which role's sign-off, so no accepted spec says where the
// allowance lives and model.ApprovalFlow carries no field for it. The
// resolution is written out anyway — project, then stamped snapshot — so the
// day a flow field is defined, reading it is the only change here and no
// caller moves.
func SelfReviewAllowed(tx *sql.Tx, kind, entityID string) (bool, error) {
	projectID, err := projectForApproval(tx, kind, entityID)
	if err != nil || projectID == "" {
		return false, err
	}
	snap, err := ProjectApprovalFlow(tx, projectID)
	if err != nil || snap == nil {
		return false, err
	}
	// snap.Flow declares no self-review permission; see the doc comment.
	return false, nil
}

// AuthorizeSelfReviewException stamps exception_authorized_by on an open
// approval (029 §7.1): a different authorized actor says this author may
// review their own work, before the review happens. DecideApproval then reads
// the column through SelfReviewExceptionValid.
//
// Refused when the row is decided (ErrApprovalResolved); an exception is
// already authorized (ErrInvalidInput); the authorizer authored the thing
// under review (ErrSelfApproval — authorizing your own exception is the
// self-approval the rule exists to prevent); or the effective policy does not
// permit self-review (ErrForbidden). The row-local checks come first because
// they hold whatever the policy says.
//
// SelfReviewAllowed is false for every project today, so this refuses every
// call. See its doc comment for why that is the honest answer rather than a
// stub.
func AuthorizeSelfReviewException(tx *sql.Tx, id int64, actorID string) error {
	if actorID == "" {
		return fmt.Errorf("%w: authorizing an exception needs an actor", ErrInvalidInput)
	}
	a, err := scanApproval(tx.QueryRow(
		`SELECT `+approvalColumns+` FROM approvals WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load approval %d: %w", id, err)
	}
	if a.State != "awaiting" && a.State != "changes_requested" {
		return ErrApprovalResolved
	}
	if a.ExceptionAuthorizedBy != nil && *a.ExceptionAuthorizedBy != "" {
		return fmt.Errorf("%w: this approval already carries an authorized self-review exception",
			ErrInvalidInput)
	}
	author, authorizer, err := authorAndActorForEntity(tx, a.EntityKind, a.EntityID, actorID)
	if err != nil {
		return err
	}
	if IsSelfApproval(author, authorizer) {
		return ErrSelfApproval
	}
	allowed, err := SelfReviewAllowed(tx, a.EntityKind, a.EntityID)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: this project's review flow does not permit self-review", ErrForbidden)
	}
	if _, err := tx.Exec(
		`UPDATE approvals SET exception_authorized_by = $2 WHERE id = $1`, id, actorID); err != nil {
		return fmt.Errorf("authorize self-review exception on approval %d: %w", id, err)
	}
	return nil
}

// ReopenApproval flips changes_requested back to awaiting (029 §7.1's
// re-request edge), clearing resolving_actor and resolved_at. No-op on any
// other state, including approved.
func ReopenApproval(tx *sql.Tx, id int64) error {
	_, err := tx.Exec(
		`UPDATE approvals SET state = 'awaiting', resolving_actor = NULL,
		   resolved_at = NULL
		 WHERE id = $1 AND state = 'changes_requested'`,
		id)
	if err != nil {
		return fmt.Errorf("reopen approval %d: %w", id, err)
	}
	return nil
}

// SetRequiredActor fills required_actor when it is currently NULL (a later
// review_requested resolves a reviewer the open ingest could not).
func SetRequiredActor(tx *sql.Tx, id int64, actorID string) error {
	_, err := tx.Exec(
		`UPDATE approvals SET required_actor = $1
		 WHERE id = $2 AND required_actor IS NULL`,
		actorID, id)
	if err != nil {
		return fmt.Errorf("set required actor on approval %d: %w", id, err)
	}
	return nil
}

// ActorIDForGitHubLogin maps a GitHub login to an actor id via
// lower(expected_github_login); "" when no actor matches.
func ActorIDForGitHubLogin(tx *sql.Tx, login string) (string, error) {
	var id string
	err := tx.QueryRow(
		`SELECT id FROM actors WHERE lower(expected_github_login) = lower($1)`,
		login).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("actor for github login %s: %w", login, err)
	}
	return id, nil
}

// GetApproval loads one row by id; ErrNotFound when absent.
func (s *Store) GetApproval(ctx context.Context, id int64) (*Approval, error) {
	a, err := scanApproval(s.db.QueryRowContext(ctx,
		`SELECT `+approvalColumns+` FROM approvals WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get approval %d: %w", id, err)
	}
	return a, nil
}

// approvalEntityJoins correlates an approvals row to whatever it governs,
// one LEFT JOIN per entity_kind, each matching entity_id the way the writer
// spelled it (PREntityID, DocEntityID). Kept in one place so
// ListAwaitingApprovals and ApprovalsAwaiting cannot drift apart.
//
// Every join is a LEFT JOIN, and that is the point: a doc has no task between
// it and its project, and an entity_kind added later has no join here at all.
// An inner join would silently drop those rows from the queue — the one thing
// 029 §7.1's "a missing approval is a visible row" cannot afford. A row whose
// kind nothing correlates still lists, with empty columns. (approvalPROpen
// below adds a narrower, deliberate exclusion on top of these joins — a
// pr-kind row whose PR has closed — which is a display decision, not a
// correlation failure; see its comment.)
//
// Every join is on a primary key (or pull_requests' unique repo+number), so
// no branch can fan a single approvals row out into several queue rows.
const approvalEntityJoins = `LEFT JOIN pull_requests pr
		ON a.entity_kind = 'pr' AND a.entity_id = pr.repo || '#' || pr.number
	LEFT JOIN tasks t ON t.id = pr.task_id
	LEFT JOIN docs d ON a.entity_kind = 'doc' AND a.entity_id = 'doc:' || d.id
	LEFT JOIN deliverables del ON a.entity_kind = 'deliverable' AND a.entity_id = del.id
	LEFT JOIN tasks tk ON a.entity_kind = 'task' AND a.entity_id = tk.id`

// approvalProjectID is the project an approvals row belongs to under those
// joins: through its task for a PR, directly for every other kind.
const approvalProjectID = `coalesce(t.project_id, d.project_id, del.project_id, tk.project_id)`

// approvalEntityTitle and approvalEntityURL are what a queue row renders of
// whichever entity join matched: the title a reader scans for, and the one
// address that opens it. A PR jumps out to GitHub and a deliverable to its
// declared address if it has one; the rest are cockpit pages. Every arm
// resolves to somewhere, because the queue renders the title as a link and
// an empty href is a link that reloads the page. A row no join correlates
// still lists, with both columns empty (see approvalEntityJoins).
//
// A task-kind row's own id is its EntityID, so the Task column below stays
// the PR's task and does not restate it.
const (
	approvalEntityTitle = `coalesce(pr.title, d.title, del.name, tk.title)`
	approvalEntityURL   = `coalesce(pr.url, '/docs/' || d.id, nullif(del.url, ''),
		'/projects/' || del.project_id || '/deliverables', '/tasks/' || tk.id)`
)

// approvalPROpen is true for every non-pr approval, every pr-kind approval
// not yet correlated to a pull_requests row, and every pr-kind approval
// whose PR is still open. Shared so the open-approval readers below cannot
// spell "hide a closed PR's stale approval" three different ways.
//
// This is a display filter, not a resolution: it never marks a row decided,
// and OpenApprovalForEntity (unfiltered, ingest-only) still finds it if a
// late review lands. Tasks carry no review requirement by default (029
// §7.3) — the requirement pull_request_review ingest materializes as an
// awaiting row on every task-correlated PR open, regardless of policy — so
// a closed PR's still-awaiting row is not evidence a required sign-off was
// skipped, only that nobody can act on it any more (WL-663).
const approvalPROpen = `(a.entity_kind <> 'pr' OR pr.state IS NULL OR pr.state = 'open')`

// AwaitingApproval is one queue row: the approval plus what a person needs to
// act on it. Declared in internal/model for the same reason Approval is; see
// there for what each field means and when it is empty.
type AwaitingApproval = model.AwaitingApproval

// scanAwaitingApproval reads one row selected with the SELECT list
// ListAwaitingApprovals builds: approvalColumns qualified under "a", then the
// entity/task/project/actor columns in the order below. All of them scan
// through sql.NullString: under the LEFT JOINs every one can be NULL.
func scanAwaitingApproval(row rowScanner) (*AwaitingApproval, error) {
	var aa AwaitingApproval
	var title, url, author, taskID, projectID, projectName, actorName sql.NullString
	err := row.Scan(&aa.ID, &aa.EntityKind, &aa.EntityID, &aa.SubjectRevision,
		&aa.Lane, &aa.RequiredRole, &aa.RequiredActor, &aa.ResolvingActor,
		&aa.State, &aa.ReviewKind, &aa.Note, &aa.ExceptionAuthorizedBy,
		&aa.CreatedAt, &aa.CreatedBy, &aa.ResolvedAt,
		&title, &url, &author, &taskID, &projectID, &projectName, &actorName)
	if err != nil {
		return nil, err
	}
	aa.Title = title.String
	aa.URL = url.String
	aa.Author = author.String
	aa.Task = taskID.String
	aa.Project = projectID.String
	aa.ProjectName = projectName.String
	if actorName.Valid {
		aa.RequiredActorName = &actorName.String
	}
	return &aa, nil
}

// ListAwaitingApprovals returns every awaiting approval with the entity it
// governs, oldest first, excluding a pr-kind row whose PR has closed
// (approvalPROpen — WL-663). Title/URL come from whichever entity join
// matched (approvalEntityTitle/URL); Author is the PR's login where there is
// one and otherwise the actor that filed the requirement.
func (s *Store) ListAwaitingApprovals(ctx context.Context) ([]AwaitingApproval, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+qualifyColumns(approvalColumns, "a")+`,
		        `+approvalEntityTitle+`, `+approvalEntityURL+`,
		        coalesce(pr.author, a.created_by), t.id,
		        `+approvalProjectID+`, p.name, ra.display_name
		 FROM approvals a
		 `+approvalEntityJoins+`
		 LEFT JOIN projects p ON p.id = `+approvalProjectID+`
		 LEFT JOIN actors ra ON ra.id = a.required_actor
		 WHERE a.state = 'awaiting'
		   AND `+approvalPROpen+`
		 ORDER BY a.created_at, a.id`)
	if err != nil {
		return nil, fmt.Errorf("list awaiting approvals: %w", err)
	}
	return collectRows(rows, "list awaiting approvals", byValue(scanAwaitingApproval))
}

// ApprovalCount is one project's tally of awaiting approvals that name an
// actor or a required_role the actor's groups contain, for the Home page's
// per-project badge.
type ApprovalCount struct {
	ProjectID string
	Count     int
}

// ApprovalsAwaiting counts awaiting approvals whose required_actor is
// actorID or whose required_role names a group actorID belongs to, grouped
// by project, excluding a pr-kind row whose PR has closed (approvalPROpen —
// WL-663). An empty actorID with no groups (the open-instance subject)
// matches nothing, by design. Unlike the queue, a row no entity join
// correlates is dropped rather than listed: this feeds a per-project badge,
// and a row with no project has no badge to land on.
func (s *Store) ApprovalsAwaiting(ctx context.Context,
	actorID string, groups []string) ([]ApprovalCount, error) {
	if actorID == "" && len(groups) == 0 {
		return nil, nil
	}
	if groups == nil {
		groups = []string{}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+approvalProjectID+`, count(*)
		 FROM approvals a
		 `+approvalEntityJoins+`
		 WHERE a.state = 'awaiting'
		   AND `+approvalPROpen+`
		   AND (a.required_actor = $1 OR a.required_role = ANY($2))
		   AND `+approvalProjectID+` IS NOT NULL
		 GROUP BY 1`,
		actorID, groups)
	if err != nil {
		return nil, fmt.Errorf("approvals awaiting for %s: %w", actorID, err)
	}
	return collectRows(rows, "approvals awaiting", func(r rowScanner) (ApprovalCount, error) {
		var c ApprovalCount
		err := r.Scan(&c.ProjectID, &c.Count)
		return c, err
	})
}

// InboxReview is one open pr-kind approval as the cross-project inbox
// consumes it (spec 056 §3.1).
type InboxReview struct {
	ApprovalID    int64
	Project       string
	EntityID      string // repo#number
	Title, URL    string
	AuthorLogin   string
	RequiredActor *string
	RequiredRole  *string
	CreatedAt     time.Time
}

func scanInboxReview(row rowScanner) (*InboxReview, error) {
	var r InboxReview
	var project, title, url, author sql.NullString
	err := row.Scan(&r.ApprovalID, &project, &r.EntityID, &title, &url, &author,
		&r.RequiredActor, &r.RequiredRole, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	r.Project = project.String
	r.Title = title.String
	r.URL = url.String
	r.AuthorLogin = author.String
	return &r, nil
}

// ListInboxReviews returns every open ('awaiting' | 'changes_requested')
// pr-kind approval whose PR is still open (approvalPROpen — WL-663) across
// all projects, oldest first, id tiebreak — the membership scoping happens
// in the pure assembly (056 §3.3's score-wide-filter-late rule applied
// uniformly). Join shape is approvalEntityJoins/approvalProjectID, the same
// as ListAwaitingApprovals,
// plus the PR's author column.
func (s *Store) ListInboxReviews(ctx context.Context) ([]InboxReview, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, `+approvalProjectID+`, a.entity_id,
		        coalesce(pr.title, ''), coalesce(pr.url, ''), coalesce(pr.author, ''),
		        a.required_actor, a.required_role, a.created_at
		 FROM approvals a
		 `+approvalEntityJoins+`
		 WHERE a.entity_kind = 'pr' AND a.state IN ('awaiting', 'changes_requested')
		   AND `+approvalPROpen+`
		 ORDER BY a.created_at, a.id`)
	if err != nil {
		return nil, fmt.Errorf("list inbox reviews: %w", err)
	}
	return collectRows(rows, "list inbox reviews", byValue(scanInboxReview))
}

// HasInboxItems answers 056 §4's indicator: does at least one inbox item
// exist for the actor. One statement of EXISTS branches, each commented
// with the §3.2 bucket it answers, so this reader and ListInboxReviews plus
// the pure assembly (internal/api's assembleInbox, over ListInboxReviews and
// ListProjectWorkFacts) cannot silently diverge. Postgres evaluates an
// EXISTS(... UNION ALL ...) lazily and stops at the first row produced, so
// this never runs the full ranked query. Every PR-kind branch excludes a
// closed PR's approval (WL-663), the same rule as ListInboxReviews: bucket 1
// left-joins pull_requests (so `pr.state IS NULL` still counts an
// approval not yet correlated to a PR row), buckets 2 and 3 already inner-join
// it, so a bare `pr.state = 'open'` is enough — there is no NULL case to
// spell there.
func (s *Store) HasInboxItems(ctx context.Context, actorID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (
			-- §3.2 bucket 1: reviews assigned to the actor
			SELECT 1 FROM approvals a
			 LEFT JOIN pull_requests pr ON a.entity_id = pr.repo || '#' || pr.number
			 WHERE a.entity_kind = 'pr' AND a.state IN ('awaiting', 'changes_requested')
			   AND a.required_actor = $1
			   AND (pr.state IS NULL OR pr.state = 'open')

			UNION ALL

			-- §3.2 bucket 2: unassigned reviews in a project the actor leads
			SELECT 1 FROM approvals a
			 JOIN pull_requests pr ON a.entity_id = pr.repo || '#' || pr.number
			 JOIN tasks t ON t.id = pr.task_id
			 JOIN project_participants pp
			   ON pp.project_id = t.project_id AND pp.actor_id = $1 AND pp.is_lead
			 WHERE a.entity_kind = 'pr' AND a.state IN ('awaiting', 'changes_requested')
			   AND a.required_actor IS NULL
			   AND pr.state = 'open'

			UNION ALL

			-- §3.2 bucket 3: reviews the actor owns -- they authored the PR
			-- and somebody else is the required reviewer
			SELECT 1 FROM approvals a
			 JOIN pull_requests pr ON a.entity_id = pr.repo || '#' || pr.number
			 JOIN actors act ON act.id = $1
			 WHERE a.entity_kind = 'pr' AND a.state IN ('awaiting', 'changes_requested')
			   AND pr.author IS NOT NULL AND act.expected_github_login IS NOT NULL
			   AND lower(pr.author) = lower(act.expected_github_login)
			   AND a.required_actor IS NOT NULL AND a.required_actor <> $1
			   AND pr.state = 'open'

			UNION ALL

			-- §3.2 buckets 4-5: active-state work assigned to or created by
			-- the actor
			SELECT 1 FROM tasks t
			 WHERE t.deleted_at IS NULL
			   AND t.state IN ('ready', 'in_progress', 'in_review')
			   AND (t.assignee = $1 OR t.created_by = $1)

			UNION ALL

			-- §3.2 bucket 6: other active-state work in a project the actor
			-- is a member of
			SELECT 1 FROM tasks t
			 JOIN project_participants pp
			   ON pp.project_id = t.project_id AND pp.actor_id = $1
			 WHERE t.deleted_at IS NULL
			   AND t.state IN ('ready', 'in_progress', 'in_review')
		 )`, actorID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has inbox items for %s: %w", actorID, err)
	}
	return exists, nil
}

// RevisionRef renders a revision-bound entity reference, "<id>@<revision>".
// The one spelling for governed-edge endpoints (InsertGovernedRefs et al.)
// and the CI gate's queries (029 §7.1).
func RevisionRef(entityID, revision string) string {
	return entityID + "@" + revision
}

// ImpactRevision renders an impact row's subject_revision:
// "<dependentRevision>+<upstreamID>@<upstreamRevision>" — the pair the
// impact decision reviews, unique per upstream move (029 §7.1).
func ImpactRevision(dependentRevision, upstreamID, upstreamRevision string) string {
	return dependentRevision + "+" + RevisionRef(upstreamID, upstreamRevision)
}

// ListApprovalsForEntity returns every row for (kind, id), newest first — the
// shared history for the detail page, the impact checks (PriorApprover), and
// the read API. No review_kind filter: an impact row is history too.
func ListApprovalsForEntity(tx *sql.Tx, entityKind, entityID string) ([]Approval, error) {
	rows, err := tx.Query(
		`SELECT `+approvalColumns+` FROM approvals
		 WHERE entity_kind = $1 AND entity_id = $2
		 ORDER BY id DESC`,
		entityKind, entityID)
	if err != nil {
		return nil, fmt.Errorf("approval history for %s %s: %w", entityKind, entityID, err)
	}
	return collectRows(rows, fmt.Sprintf("approval history for %s %s", entityKind, entityID),
		byValue(scanApproval))
}

// ListApprovalsForEntityCtx is ListApprovalsForEntity for the detail page
// (GET /approvals/{id}, 032 §7), which reads outside the caller's own
// transaction — the same shape docReviewersCtx gives docReviewers.
func (s *Store) ListApprovalsForEntityCtx(ctx context.Context, entityKind, entityID string) ([]Approval, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+approvalColumns+` FROM approvals
		 WHERE entity_kind = $1 AND entity_id = $2
		 ORDER BY id DESC`,
		entityKind, entityID)
	if err != nil {
		return nil, fmt.Errorf("approval history for %s %s: %w", entityKind, entityID, err)
	}
	return collectRows(rows, fmt.Sprintf("approval history for %s %s", entityKind, entityID),
		byValue(scanApproval))
}

// GovernedRefsForCtx is GovernedRefsFor for the detail page, which reads
// outside the caller's own transaction.
func (s *Store) GovernedRefsForCtx(ctx context.Context, kind, id, revision string) ([]GovernedRef, error) {
	from := RevisionRef(id, revision)
	rows, err := s.db.QueryContext(ctx,
		`SELECT to_kind, split_part(to_id, '@', 1), split_part(to_id, '@', 2)
		 FROM entity_edges
		 WHERE from_kind = $1 AND from_id = $2 AND rel = 'references_revision'
		 ORDER BY to_kind, to_id`,
		kind, from)
	if err != nil {
		return nil, fmt.Errorf("governed references from %s %s: %w", kind, from, err)
	}
	return collectRows(rows, fmt.Sprintf("governed references from %s %s", kind, from), scanGovernedRef)
}

// EntityTitleURL resolves the title and URL for one approval's governed
// entity, through the exact join and columns ListAwaitingApprovals's queue
// uses (approvalEntityJoins, approvalEntityTitle, approvalEntityURL) — so
// the detail page cannot name an entity, or link it, differently than the
// queue row a reviewer reached it from. Both come back "" when no join
// correlates (an entity kind with no row yet): an honest empty, not a
// fabricated identity.
func (s *Store) EntityTitleURL(ctx context.Context, approvalID int64) (title, url string, err error) {
	var t, u sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT `+approvalEntityTitle+`, `+approvalEntityURL+`
		 FROM approvals a
		 `+approvalEntityJoins+`
		 WHERE a.id = $1`,
		approvalID).Scan(&t, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("entity title/url for approval %d: %w", approvalID, err)
	}
	return t.String, u.String, nil
}

// designationScope narrows DesignateRevision's queries to the no-lane,
// 'review'-kind row: the single decision a PR-style entity carries. A
// document's several reviewer lanes (025 §7.3) are RequestDocApproval's
// concern, not DesignateRevision's — nothing here reruns that fan-out.
const designationScope = `lane = '' AND review_kind = 'review'`

// DesignateRevision applies OnNewRevision (approval_rules.go) inside the
// caller's event transaction (029 §7.1): rebinds the open review row's
// subject_revision, or inserts a candidate row copying required_role/
// required_actor from the newest decided review row. A designation that moved
// something (rebind or candidate) then fans the change out to the entities
// governed by it — see impactFanOut. Returns the outcome and how many impact
// rows opened, both for the caller's metric.
func DesignateRevision(tx *sql.Tx, now time.Time,
	entityKind, entityID, newRevision string) (RevisionOutcome, int, error) {
	open, err := scanApproval(tx.QueryRow(
		`SELECT `+approvalColumns+` FROM approvals
		 WHERE entity_kind = $1 AND entity_id = $2 AND `+designationScope+`
		   AND state IN ('awaiting', 'changes_requested')
		 ORDER BY id DESC LIMIT 1`,
		entityKind, entityID))
	if errors.Is(err, sql.ErrNoRows) {
		open = nil
	} else if err != nil {
		return 0, 0, fmt.Errorf("open review for %s %s: %w", entityKind, entityID, err)
	}

	var hasDecided, boundAlready bool
	if err := tx.QueryRow(
		`SELECT
		   EXISTS (SELECT 1 FROM approvals WHERE entity_kind = $1 AND entity_id = $2
		             AND `+designationScope+` AND state IN ('approved', 'rejected')),
		   EXISTS (SELECT 1 FROM approvals WHERE entity_kind = $1 AND entity_id = $2
		             AND `+designationScope+` AND subject_revision = $3)`,
		entityKind, entityID, newRevision).Scan(&hasDecided, &boundAlready); err != nil {
		return 0, 0, fmt.Errorf("revision history for %s %s: %w", entityKind, entityID, err)
	}

	outcome := OnNewRevision(open, hasDecided, boundAlready)
	switch outcome {
	case RevisionRebind:
		if _, err := tx.Exec(`UPDATE approvals SET subject_revision = $1 WHERE id = $2`,
			newRevision, open.ID); err != nil {
			return 0, 0, fmt.Errorf("rebind %s %s to %s: %w", entityKind, entityID, newRevision, err)
		}
	case RevisionCandidate:
		decided, err := scanApproval(tx.QueryRow(
			`SELECT `+approvalColumns+` FROM approvals
			 WHERE entity_kind = $1 AND entity_id = $2 AND `+designationScope+`
			   AND state IN ('approved', 'rejected')
			 ORDER BY id DESC LIMIT 1`,
			entityKind, entityID))
		if err != nil {
			return 0, 0, fmt.Errorf("newest decided review for %s %s: %w", entityKind, entityID, err)
		}
		if _, err := InsertAwaitingApproval(tx, now, entityKind, entityID, newRevision, "",
			decided.RequiredRole, decided.RequiredActor, nil); err != nil {
			return 0, 0, err
		}
	default:
		return outcome, 0, nil
	}
	opened, err := impactFanOut(tx, now, entityKind, entityID, newRevision)
	if err != nil {
		return 0, 0, err
	}
	return outcome, opened, nil
}

// approvedReviewFor returns an entity's newest approved review-kind row, or
// nil when it has none — the row an impact question is asked about, and the
// row a reopen puts back in review.
func approvedReviewFor(tx *sql.Tx, entityKind, entityID string) (*Approval, error) {
	a, err := scanApproval(tx.QueryRow(
		`SELECT `+approvalColumns+` FROM approvals
		 WHERE entity_kind = $1 AND entity_id = $2 AND review_kind = 'review'
		   AND state = 'approved'
		 ORDER BY id DESC LIMIT 1`,
		entityKind, entityID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("approved review for %s %s: %w", entityKind, entityID, err)
	}
	return a, nil
}

// impactFanOut opens 029 §7.1's explicit impact review: for every entity
// governed by (entityKind, entityID) that already approved something, one
// 'awaiting' impact row asking its prior approver whether that decision still
// holds at the upstream's new revision. The requirement is copied from the
// dependent's approved row; required_actor stays NULL, since the question is
// open to any qualified prior approver. Returns how many rows opened.
func impactFanOut(tx *sql.Tx, now time.Time,
	entityKind, entityID, newRevision string) (int, error) {
	deps, err := DependentsOf(tx, entityKind, entityID)
	if err != nil {
		return 0, err
	}
	opened := 0
	for _, d := range deps {
		approved, err := approvedReviewFor(tx, d.Kind, d.ID)
		if err != nil {
			return 0, err
		}
		if approved == nil {
			continue
		}
		// The NOT EXISTS is the absorption rule: a dependent's owner owes one
		// answer at a time, not one per upstream push. ON CONFLICT alone would
		// only absorb a redelivery of the same head, because subject_revision
		// varies with the upstream revision (ImpactRevision); it stays for
		// exactly that redelivery case.
		res, err := tx.Exec(
			`INSERT INTO approvals
			   (entity_kind, entity_id, subject_revision, required_role,
			    required_actor, lane, state, review_kind, created_at)
			 SELECT $1, $2, $3, $4, NULL, '', 'awaiting', 'impact', $5
			  WHERE NOT EXISTS (
			    SELECT 1 FROM approvals
			     WHERE entity_kind = $1 AND entity_id = $2 AND review_kind = 'impact'
			       AND state IN ('awaiting', 'changes_requested'))
			 ON CONFLICT (entity_kind, entity_id, subject_revision, lane, review_kind)
			 DO NOTHING`,
			d.Kind, d.ID, ImpactRevision(approved.SubjectRevision, entityID, newRevision),
			approved.RequiredRole, now.UTC())
		if err != nil {
			return 0, fmt.Errorf("open impact review on %s %s: %w", d.Kind, d.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("open impact review on %s %s: %w", d.Kind, d.ID, err)
		}
		opened += int(n)
	}
	return opened, nil
}

// SetImpactNote records the dependent owner's note on an open impact review
// (029 §7.1): what the upstream change means for this entity, written before
// a prior approver decides. ErrApprovalResolved once the row is decided,
// ErrInvalidInput on an ordinary review row or an empty note.
func SetImpactNote(tx *sql.Tx, id int64, note string) error {
	if strings.TrimSpace(note) == "" {
		return fmt.Errorf("%w: an impact note needs text", ErrInvalidInput)
	}
	var state, reviewKind string
	err := tx.QueryRow(
		`SELECT state, review_kind FROM approvals WHERE id = $1 FOR UPDATE`,
		id).Scan(&state, &reviewKind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load approval %d: %w", id, err)
	}
	if reviewKind != "impact" {
		return fmt.Errorf("%w: approval %d is not an impact review", ErrInvalidInput, id)
	}
	if state != "awaiting" && state != "changes_requested" {
		return ErrApprovalResolved
	}
	if _, err := tx.Exec(`UPDATE approvals SET note = $1 WHERE id = $2`, note, id); err != nil {
		return fmt.Errorf("set impact note on approval %d: %w", id, err)
	}
	return nil
}

// GovernedRef is one revision-bound reference a designation recorded (029
// §7.1): an entity_edges endpoint split back into its kind, id and revision.
type GovernedRef struct {
	Kind, ID, Revision string
}

// InsertGovernedRefs writes rel='references_revision' entity_edges rows from
// RevisionRef(fromID, fromRevision) to each RevisionRef(ref.ID, ref.Revision)
// (029 §7.1). Idempotent on the table's primary key (from_kind, from_id,
// to_kind, to_id, rel) — a re-designation that references the same set is a
// no-op, not a conflict.
func InsertGovernedRefs(tx *sql.Tx, now time.Time, createdBy *string,
	fromKind, fromID, fromRevision string, refs []GovernedRef) error {
	from := RevisionRef(fromID, fromRevision)
	ts := now.UTC()
	for _, ref := range refs {
		to := RevisionRef(ref.ID, ref.Revision)
		if _, err := tx.Exec(
			`INSERT INTO entity_edges (from_kind, from_id, to_kind, to_id, rel, created_at, created_by)
			 VALUES ($1, $2, $3, $4, 'references_revision', $5, $6)
			 ON CONFLICT (from_kind, from_id, to_kind, to_id, rel) DO NOTHING`,
			fromKind, from, ref.Kind, to, ts, createdBy,
		); err != nil {
			return fmt.Errorf("insert governed reference %s %s -> %s %s: %w",
				fromKind, from, ref.Kind, to, err)
		}
	}
	return nil
}

// scanGovernedRef reads one row selected as (kind, id, revision) — either
// GovernedRefsFor's to_-side split or DependentsOf's from_-side split.
func scanGovernedRef(row rowScanner) (GovernedRef, error) {
	var g GovernedRef
	err := row.Scan(&g.Kind, &g.ID, &g.Revision)
	return g, err
}

// GovernedRefsFor returns the references recorded from (kind, id) at
// revision — exactly what that revision's own designation wrote.
func GovernedRefsFor(tx *sql.Tx, kind, id, revision string) ([]GovernedRef, error) {
	from := RevisionRef(id, revision)
	rows, err := tx.Query(
		`SELECT to_kind, split_part(to_id, '@', 1), split_part(to_id, '@', 2)
		 FROM entity_edges
		 WHERE from_kind = $1 AND from_id = $2 AND rel = 'references_revision'
		 ORDER BY to_kind, to_id`,
		kind, from)
	if err != nil {
		return nil, fmt.Errorf("governed references from %s %s: %w", kind, from, err)
	}
	return collectRows(rows, fmt.Sprintf("governed references from %s %s", kind, from), scanGovernedRef)
}

// DependentsOf returns the distinct entities holding a references_revision
// edge pointing at (kind, id) at any revision of it — the impact fan-out set
// (029 §7.1). A referrer that recorded the reference at more than one of its
// own revisions is returned once, at its newest.
func DependentsOf(tx *sql.Tx, kind, id string) ([]GovernedRef, error) {
	rows, err := tx.Query(
		`SELECT DISTINCT ON (from_kind, split_part(from_id, '@', 1))
		        from_kind, split_part(from_id, '@', 1), split_part(from_id, '@', 2)
		 FROM entity_edges
		 WHERE to_kind = $1 AND split_part(to_id, '@', 1) = $2 AND rel = 'references_revision'
		 ORDER BY from_kind, split_part(from_id, '@', 1), created_at DESC`,
		kind, id)
	if err != nil {
		return nil, fmt.Errorf("dependents of %s %s: %w", kind, id, err)
	}
	return collectRows(rows, fmt.Sprintf("dependents of %s %s", kind, id), scanGovernedRef)
}
