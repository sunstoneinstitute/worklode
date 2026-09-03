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
// correlate a row to its docs row (and its project) in SQL.
func DocEntityID(docID int64) string {
	return fmt.Sprintf("doc:%d", docID)
}

// InsertAwaitingApproval materializes the requirement as an 'awaiting' row.
// The ON CONFLICT list is migration 0057's whole unique key, lane columns
// included, so two reviewer lanes on one document revision coexist while a
// redelivered or reopened PR — which writes the same NULL/NULL lane — still
// does not duplicate the requirement. That last part holds only because the
// key is NULLS NOT DISTINCT.
func InsertAwaitingApproval(tx *sql.Tx, now time.Time,
	entityKind, entityID, subjectRevision string,
	requiredRole, requiredActor *string) error {
	_, err := tx.Exec(
		`INSERT INTO approvals
		   (entity_kind, entity_id, subject_revision, required_role,
		    required_actor, state, created_at)
		 VALUES ($1, $2, $3, $4, $5, 'awaiting', $6)
		 ON CONFLICT (entity_kind, entity_id, subject_revision, required_role,
		              required_actor) DO NOTHING`,
		entityKind, entityID, subjectRevision, requiredRole, requiredActor,
		now.UTC())
	if err != nil {
		return fmt.Errorf("insert awaiting approval %s %s@%s: %w",
			entityKind, entityID, subjectRevision, err)
	}
	return nil
}

// RequestDocApproval materializes 025 §7.3's durable reviewer set (WL-359:
// doc_reviewers, assigned separately via SetDocReviewers) for one document
// revision: one 'awaiting' row per assigned reviewer, all on the same
// subject_revision (the docs.version the reviewers see), which is exactly the
// shape migration 0057's per-lane unique key permits. Re-running it for the
// same version is a no-op, and a reviewer assigned later gets only the new
// lane the next time this runs.
//
// 029 §7.2's role-scoped lanes (a flow requiring "someone in this group")
// call InsertAwaitingApproval directly with a required_role: that assignment
// is project policy, not a document's own reviewer set, and the two lane
// kinds coexist on one revision under 0057's key without conflict.
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
		if err := InsertAwaitingApproval(tx, now, "doc", entityID, revision,
			nil, &r); err != nil {
			return err
		}
	}
	return nil
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
// several open lanes on purpose, unlike OpenApprovalForEntity's single-row
// PR case, so this reads every open one rather than the newest.
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
const approvalColumns = `id, entity_kind, entity_id, subject_revision,
	required_role, required_actor, resolving_actor, state, created_at, resolved_at`

func scanApproval(row rowScanner) (*Approval, error) {
	var a Approval
	err := row.Scan(&a.ID, &a.EntityKind, &a.EntityID, &a.SubjectRevision,
		&a.RequiredRole, &a.RequiredActor, &a.ResolvingActor, &a.State,
		&a.CreatedAt, &a.ResolvedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// OpenApprovalForEntity returns the open row for (entityKind, entityID) —
// state 'awaiting' or 'changes_requested', both counting as open —
// ErrNotFound otherwise. The PR ingest keeps at most one open row per entity,
// so ORDER BY id DESC is a deterministic tiebreak rather than a lane
// selector. A document's reviewer set is several open rows on purpose (§7.3),
// so this is the wrong reader for one: address a doc lane by id instead.
func OpenApprovalForEntity(tx *sql.Tx, entityKind, entityID string) (*Approval, error) {
	a, err := scanApproval(tx.QueryRow(
		`SELECT `+approvalColumns+` FROM approvals
		 WHERE entity_kind = $1 AND entity_id = $2
		   AND state IN ('awaiting', 'changes_requested')
		 ORDER BY id DESC LIMIT 1`,
		entityKind, entityID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open approval for %s %s: %w", entityKind, entityID, err)
	}
	return a, nil
}

// ResolveApproval stamps state, resolving_actor, resolved_at. Shared by the
// review ingest and the web act, so the two resolution paths cannot drift.
// There is no state guard here: callers reach the row through
// OpenApprovalForEntity (or their own open-state check) first.
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
// (ErrApprovalResolved); the decider holds the group required_role names
// (ErrNotQualified); and, for entity_kind 'pr', the decider did not author
// the pull request (ErrSelfApproval). Then it resolves the row.
//
// Self-approval is refused by default and unconditionally (029 §7.1); the
// policy-permitted exception flow is not implemented. An unknown login on
// either side proves nothing, so it does not refuse — see IsSelfApproval.
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
	if !QualifiedForRole(a.RequiredRole, in.Groups) {
		return nil, ErrNotQualified
	}
	if a.EntityKind == "pr" {
		author, err := prAuthorForEntity(tx, a.EntityID)
		if err != nil {
			return nil, err
		}
		decider, err := gitHubLoginForActor(tx, in.ActorID)
		if err != nil {
			return nil, err
		}
		if IsSelfApproval(author, decider) {
			return nil, ErrSelfApproval
		}
	}

	if err := ResolveApproval(tx, a.ID, state, &in.ActorID, in.Now); err != nil {
		return nil, err
	}
	a.State = state
	a.ResolvingActor = &in.ActorID
	resolvedAt := in.Now.UTC()
	a.ResolvedAt = &resolvedAt
	return a, nil
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
// kind nothing correlates still lists, with empty columns.
const approvalEntityJoins = `LEFT JOIN pull_requests pr
		ON a.entity_kind = 'pr' AND a.entity_id = pr.repo || '#' || pr.number
	LEFT JOIN tasks t ON t.id = pr.task_id
	LEFT JOIN docs d ON a.entity_kind = 'doc' AND a.entity_id = 'doc:' || d.id`

// approvalProjectID is the project an approvals row belongs to under those
// joins: through its task for a PR, directly for a doc.
const approvalProjectID = `coalesce(t.project_id, d.project_id)`

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
		&aa.RequiredRole, &aa.RequiredActor, &aa.ResolvingActor, &aa.State,
		&aa.CreatedAt, &aa.ResolvedAt,
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
// governs, oldest first. Title/URL/Author come from whichever entity join
// matched: a PR jumps out to GitHub, a doc links to its cockpit page (its
// SubjectRevision names the version reviewed).
func (s *Store) ListAwaitingApprovals(ctx context.Context) ([]AwaitingApproval, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+qualifyColumns(approvalColumns, "a")+`,
		        coalesce(pr.title, d.title), coalesce(pr.url, '/docs/' || d.id),
		        pr.author, t.id, `+approvalProjectID+`, p.name, ra.display_name
		 FROM approvals a
		 `+approvalEntityJoins+`
		 LEFT JOIN projects p ON p.id = `+approvalProjectID+`
		 LEFT JOIN actors ra ON ra.id = a.required_actor
		 WHERE a.state = 'awaiting'
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
// by project. An empty actorID with no groups (the open-instance subject)
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
// pr-kind approval across all projects, oldest first, id tiebreak — the
// membership scoping happens in the pure assembly (056 §3.3's
// score-wide-filter-late rule applied uniformly). Join shape is
// approvalEntityJoins/approvalProjectID, the same as ListAwaitingApprovals,
// plus the PR's author column.
func (s *Store) ListInboxReviews(ctx context.Context) ([]InboxReview, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, `+approvalProjectID+`, a.entity_id,
		        coalesce(pr.title, ''), coalesce(pr.url, ''), coalesce(pr.author, ''),
		        a.required_actor, a.required_role, a.created_at
		 FROM approvals a
		 `+approvalEntityJoins+`
		 WHERE a.entity_kind = 'pr' AND a.state IN ('awaiting', 'changes_requested')
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
// this never runs the full ranked query.
func (s *Store) HasInboxItems(ctx context.Context, actorID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (
			-- §3.2 bucket 1: reviews assigned to the actor
			SELECT 1 FROM approvals a
			 WHERE a.entity_kind = 'pr' AND a.state IN ('awaiting', 'changes_requested')
			   AND a.required_actor = $1

			UNION ALL

			-- §3.2 bucket 2: unassigned reviews in a project the actor leads
			SELECT 1 FROM approvals a
			 JOIN pull_requests pr ON a.entity_id = pr.repo || '#' || pr.number
			 JOIN tasks t ON t.id = pr.task_id
			 JOIN project_participants pp
			   ON pp.project_id = t.project_id AND pp.actor_id = $1 AND pp.is_lead
			 WHERE a.entity_kind = 'pr' AND a.state IN ('awaiting', 'changes_requested')
			   AND a.required_actor IS NULL

			UNION ALL

			-- §3.2 bucket 3: reviews the actor owns -- they authored the PR
			-- and somebody else is the required reviewer
			SELECT 1 FROM approvals a
			 JOIN pull_requests pr ON a.entity_id = pr.repo || '#' || pr.number
			 JOIN actors act ON act.id = $1
			 WHERE a.entity_kind = 'pr' AND a.state IN ('awaiting', 'changes_requested')
			   AND pr.author IS NOT NULL AND act.expected_github_login IS NOT NULL
			   AND lower(pr.author) = lower(act.expected_github_login)
			   AND (a.required_actor IS NULL OR a.required_actor <> $1)

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
