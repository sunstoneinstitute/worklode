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
	"time"
)

// Approval is one row of the approvals table.
type Approval struct {
	ID              int64
	EntityKind      string
	EntityID        string
	SubjectRevision string
	RequiredRole    *string
	RequiredActor   *string
	ResolvingActor  *string
	State           string
	CreatedAt       time.Time
	ResolvedAt      *time.Time
}

// PREntityID renders the approvals entity_id for a pull request: the webhook
// ingest and the queue reader must share this one spelling.
func PREntityID(repo string, number int64) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

// InsertAwaitingApproval materializes the requirement as an 'awaiting' row.
// ON CONFLICT (entity_kind, entity_id, subject_revision) DO NOTHING: a
// redelivered or reopened PR never duplicates the requirement.
func InsertAwaitingApproval(tx *sql.Tx, now time.Time,
	entityKind, entityID, subjectRevision string,
	requiredRole, requiredActor *string) error {
	_, err := tx.Exec(
		`INSERT INTO approvals
		   (entity_kind, entity_id, subject_revision, required_role,
		    required_actor, state, created_at)
		 VALUES ($1, $2, $3, $4, $5, 'awaiting', $6)
		 ON CONFLICT (entity_kind, entity_id, subject_revision) DO NOTHING`,
		entityKind, entityID, subjectRevision, requiredRole, requiredActor,
		now.UTC())
	if err != nil {
		return fmt.Errorf("insert awaiting approval %s %s@%s: %w",
			entityKind, entityID, subjectRevision, err)
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
// ErrNotFound otherwise. A draft PR can legally carry two open rows under
// different subject_revision (opened at head A, then ready_for_review at
// head B before A resolves): ORDER BY id DESC picks the newest revision,
// matching 029 §7.1's "each decision binds the exact governed revision".
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

// prEntityIDSQL renders a pull_requests row's approvals entity_id in SQL, the
// spelling PREntityID and prEntityJoin both use.
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

// prEntityJoin correlates an approvals row to its pull request the same way
// the ingest wrote it: entity_id spelled as PREntityID renders it. Kept in
// one place so ListAwaitingApprovals and ApprovalsAwaiting cannot drift onto
// a hand-rolled spelling.
const prEntityJoin = `JOIN pull_requests pr
	ON a.entity_kind = 'pr' AND a.entity_id = pr.repo || '#' || pr.number`

// AwaitingApproval is one queue row: the approval plus what a person needs
// to act on it. PR-kind only until another entity kind joins the queue.
type AwaitingApproval struct {
	Approval
	PRTitle, PRURL    string
	PRAuthor          string // "" when unknown (pre-column row)
	TaskID, ProjectID string
	ProjectName       string
	RequiredActorName *string // display_name, for "awaiting <who>"
}

// scanAwaitingApproval reads one row selected with the SELECT list
// ListAwaitingApprovals builds: approvalColumns qualified under "a", then
// the PR/task/project/actor columns in the order below.
func scanAwaitingApproval(row rowScanner) (*AwaitingApproval, error) {
	var aa AwaitingApproval
	var title, url, author, actorName sql.NullString
	err := row.Scan(&aa.ID, &aa.EntityKind, &aa.EntityID, &aa.SubjectRevision,
		&aa.RequiredRole, &aa.RequiredActor, &aa.ResolvingActor, &aa.State,
		&aa.CreatedAt, &aa.ResolvedAt,
		&title, &url, &author, &aa.TaskID, &aa.ProjectID, &aa.ProjectName, &actorName)
	if err != nil {
		return nil, err
	}
	aa.PRTitle = title.String
	aa.PRURL = url.String
	aa.PRAuthor = author.String
	if actorName.Valid {
		aa.RequiredActorName = &actorName.String
	}
	return &aa, nil
}

// ListAwaitingApprovals returns every awaiting approval joined to its PR,
// task, and project, oldest first. The join is per entity_kind; today that
// is one branch ('pr' via pull_requests -> tasks -> projects).
func (s *Store) ListAwaitingApprovals(ctx context.Context) ([]AwaitingApproval, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+qualifyColumns(approvalColumns, "a")+`,
		        coalesce(pr.title, ''), coalesce(pr.url, ''), coalesce(pr.author, ''),
		        t.id, t.project_id, p.name, ra.display_name
		 FROM approvals a
		 `+prEntityJoin+`
		 JOIN tasks t ON t.id = pr.task_id
		 JOIN projects p ON p.id = t.project_id
		 LEFT JOIN actors ra ON ra.id = a.required_actor
		 WHERE a.state = 'awaiting'
		 ORDER BY a.created_at, a.id`)
	if err != nil {
		return nil, fmt.Errorf("list awaiting approvals: %w", err)
	}
	return collectRows(rows, "list awaiting approvals", byValue(scanAwaitingApproval))
}

// ApprovalCount is one project's tally of awaiting approvals that name an
// actor, for the Home page's per-project badge.
type ApprovalCount struct {
	ProjectID string
	Count     int
}

// ApprovalsAwaiting counts awaiting approvals whose required_actor is
// actorID or whose required_role names a group actorID belongs to, grouped
// by project. actorID == "" with no groups is the open-instance subject: it
// matches nothing, by design — this is a security-relevant default refusal,
// not an oversight, so it never falls through to "count everything".
func (s *Store) ApprovalsAwaiting(ctx context.Context,
	actorID string, groups []string) ([]ApprovalCount, error) {
	if actorID == "" && len(groups) == 0 {
		return nil, nil
	}
	if groups == nil {
		groups = []string{}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.project_id, count(*)
		 FROM approvals a
		 `+prEntityJoin+`
		 JOIN tasks t ON t.id = pr.task_id
		 WHERE a.state = 'awaiting'
		   AND (a.required_actor = $1 OR a.required_role = ANY($2))
		 GROUP BY t.project_id`,
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
