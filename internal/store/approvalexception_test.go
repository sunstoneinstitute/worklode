package store

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// These tests cover 029 §7.1's self-review exception. Read the dormancy note
// before reading the assertions: SelfReviewAllowed is false for every project
// (§7.1 requires the allowance, §7.2's flow shape does not carry it, and
// model.ApprovalFlow defines no field for it), so the exception can never be
// authorized through the real policy read and self-approval stays refused.
//
// What that leaves testable for real: every refusal AuthorizeSelfReviewException
// can reach, the plumbing the policy read is built on (projectForApproval, the
// stamped snapshot), and — the one that matters — that stamping
// exception_authorized_by by hand still does not buy a self-decision while the
// policy is off. The success path is unreachable by construction; the pure rule
// it turns on is covered with policyAllows == true in
// TestSelfReviewExceptionValid.

// stampFlow puts a flow snapshot on the fixture project, so the policy read
// gets past its "project has no flow" early return and actually reads a
// snapshot.
func stampFlow(t *testing.T, tx *sql.Tx) {
	t.Helper()
	if err := SetProjectApprovalFlow(tx, "horndb", model.ApprovalFlowSnapshot{
		Flow: model.ApprovalFlow{Name: "sunstone-story", Rev: "3"},
	}); err != nil {
		t.Fatalf("stamp flow: %v", err)
	}
}

// exceptionAuthorizedBy reads the column back.
func exceptionAuthorizedBy(t *testing.T, tx *sql.Tx, id int64) *string {
	t.Helper()
	var by *string
	if err := tx.QueryRow(
		`SELECT exception_authorized_by FROM approvals WHERE id = $1`, id).Scan(&by); err != nil {
		t.Fatalf("read exception_authorized_by: %v", err)
	}
	return by
}

// TestAuthorizeSelfReviewExceptionRefusedByPolicy is the live behaviour of the
// whole feature: a qualified third party asks to authorize the exception on a
// project that carries a flow, and the effective policy refuses, because no
// flow declares self-review permission. Nothing is stamped.
func TestAuthorizeSelfReviewExceptionRefusedByPolicy(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t) // project "horndb", actor "stig"
	if err := s.CreateActor(t.Context(), "ada", "human", "Ada", false); err != nil {
		t.Fatal(err)
	}
	doc := docForApproval(t, s, "029-exception-policy", 291) // created_by "stig"

	tx := mustBegin(t, s)
	stampFlow(t, tx)
	id := openApprovalID(t, tx, "doc", DocEntityID(doc.ID), "rev1", "review")

	err := AuthorizeSelfReviewException(tx, id, "ada")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if by := exceptionAuthorizedBy(t, tx, id); by != nil {
		t.Errorf("exception_authorized_by = %q after a refused authorization, want NULL", *by)
	}
}

// TestAuthorizeSelfReviewExceptionRefusesTheAuthor: authorizing your own
// exception is the self-approval the rule exists to prevent, so it is refused
// on the author's identity alone, whatever the policy would say.
func TestAuthorizeSelfReviewExceptionRefusesTheAuthor(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	doc := docForApproval(t, s, "029-exception-author", 292) // created_by "stig"

	tx := mustBegin(t, s)
	stampFlow(t, tx)
	id := openApprovalID(t, tx, "doc", DocEntityID(doc.ID), "rev1", "review")

	if err := AuthorizeSelfReviewException(tx, id, "stig"); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("err = %v, want ErrSelfApproval", err)
	}
}

// TestAuthorizeSelfReviewExceptionRefusesDecidedRow: an exception is approved
// "before review" (029 §7.1), so a decided row has nothing left to permit.
func TestAuthorizeSelfReviewExceptionRefusesDecidedRow(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "ada", "human", "Ada", false); err != nil {
		t.Fatal(err)
	}
	doc := docForApproval(t, s, "029-exception-decided", 293)

	tx := mustBegin(t, s)
	id := openApprovalID(t, tx, "doc", DocEntityID(doc.ID), "rev1", "review")
	ada := "ada"
	if err := ResolveApproval(tx, id, "approved", &ada, taskTestNow); err != nil {
		t.Fatal(err)
	}

	if err := AuthorizeSelfReviewException(tx, id, "ada"); !errors.Is(err, ErrApprovalResolved) {
		t.Fatalf("err = %v, want ErrApprovalResolved", err)
	}
}

// TestAuthorizeSelfReviewExceptionRefusesSecondAuthorization: the column names
// one authorizer, and a second call must not overwrite who permitted this.
// The first authorization is stamped by hand — the policy gate would refuse a
// real one, and the refusal being tested sits ahead of that gate.
func TestAuthorizeSelfReviewExceptionRefusesSecondAuthorization(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	for _, id := range []string{"ada", "bob"} {
		if err := s.CreateActor(t.Context(), id, "human", id, false); err != nil {
			t.Fatal(err)
		}
	}
	doc := docForApproval(t, s, "029-exception-twice", 294)

	tx := mustBegin(t, s)
	id := openApprovalID(t, tx, "doc", DocEntityID(doc.ID), "rev1", "review")
	if _, err := tx.Exec(
		`UPDATE approvals SET exception_authorized_by = 'ada' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	err := AuthorizeSelfReviewException(tx, id, "bob")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if by := exceptionAuthorizedBy(t, tx, id); by == nil || *by != "ada" {
		t.Errorf("exception_authorized_by = %v, want it left at ada", by)
	}
}

// TestDecideSelfApprovalStandsUnderStampedException is the dormancy, asserted
// rather than assumed. exception_authorized_by names a different actor — the
// exact column state a live policy would produce — and the author's own
// decision is still refused, because SelfReviewExceptionValid's first
// condition, the policy allowance, is false. When a flow field lands, this
// test flips to the brief's "authorize then self-decide succeeds".
func TestDecideSelfApprovalStandsUnderStampedException(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "ada", "human", "Ada", false); err != nil {
		t.Fatal(err)
	}
	doc := docForApproval(t, s, "029-exception-decide", 295) // created_by "stig"

	tx := mustBegin(t, s)
	stampFlow(t, tx)
	id := openApprovalID(t, tx, "doc", DocEntityID(doc.ID), "rev1", "review")
	if _, err := tx.Exec(
		`UPDATE approvals SET exception_authorized_by = 'ada' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	allowed, err := SelfReviewAllowed(tx, "doc", DocEntityID(doc.ID))
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("SelfReviewAllowed is true: a flow now declares self-review permission, " +
			"so this test and its comment are out of date")
	}
	if _, err := DecideApproval(tx, DecideInput{
		ApprovalID: id, Decision: "approve", ActorID: "stig", Now: taskTestNow,
	}); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("err = %v, want ErrSelfApproval (the policy allowance is off)", err)
	}
}

// TestProjectForApproval covers the resolution SelfReviewAllowed is built on,
// which is the half of the policy read that is real: every governed kind
// reaches its project, a PR through the task its head ref correlates to, and
// an uncorrelated entity resolves to "" rather than erroring.
func TestProjectForApproval(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	doc := docForApproval(t, s, "029-exception-project", 296)
	task := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "horndb", Title: "reviewed", Body: "b", Priority: "medium",
		Kind: "feature", CreatedBy: "stig",
	})
	del, err := createDeliverable(s, DeliverableInput{
		ProjectID: "horndb", Name: "the report", CreatedBy: "stig",
	})
	if err != nil {
		t.Fatal(err)
	}

	tx := mustBegin(t, s)
	if _, _, err := UpsertPR(tx, PullRequest{
		Repo: "acme/site", Number: 42, Title: "t", State: "open",
		HeadRef: task.ID + "-fix", HeadSHA: "sha1", OpenedAt: taskTestNow,
	}, ""); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, kind, entityID, want string }{
		{"doc", "doc", DocEntityID(doc.ID), "horndb"},
		{"task", "task", task.ID, "horndb"},
		{"deliverable", "deliverable", del.ID, "horndb"},
		{"pr through its task", "pr", PREntityID("acme/site", 42), "horndb"},
		{"pr nothing correlates", "pr", "acme/site#999", ""},
		{"unknown kind", "widget", "x", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := projectForApproval(tx, tc.kind, tc.entityID)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("projectForApproval(%q, %q) = %q, want %q",
					tc.kind, tc.entityID, got, tc.want)
			}
		})
	}
}

// TestSelfReviewPolicyReportsStampedFlow: the approval detail page's read
// returns the flow it can name beside a decision, and the allowance it cannot
// grant.
func TestSelfReviewPolicyReportsStampedFlow(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	doc := docForApproval(t, s, "029-exception-stamp", 297)

	tx := mustBegin(t, s)
	stampFlow(t, tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	name, rev, allowed, err := s.SelfReviewPolicy(t.Context(), "doc", DocEntityID(doc.ID))
	if err != nil {
		t.Fatal(err)
	}
	if name != "sunstone-story" || rev != "3" {
		t.Errorf("flow = %q@%q, want sunstone-story@3", name, rev)
	}
	if allowed {
		t.Error("allowed = true, want false: no flow declares self-review permission")
	}
}
