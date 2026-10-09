package store

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// stampFlow puts a flow snapshot on the fixture project.
func stampFlow(t *testing.T, tx *sql.Tx, selfReview ...string) {
	t.Helper()
	if err := SetProjectApprovalFlow(tx, "horndb", model.ApprovalFlowSnapshot{
		Flow: model.ApprovalFlow{Name: "sunstone-story", Rev: "3", SelfReview: selfReview},
	}); err != nil {
		t.Fatalf("stamp flow: %v", err)
	}
}

// TestDecideSelfReview (WL-SPEC-75 §13.6): an author's own decision is refused
// unless the project's flow names the entity kind under self_review. An
// allowed self-decision stamps the author as exception_authorized_by.
func TestDecideSelfReview(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		selfReview []string
		want       error
	}{
		{"flow allows nothing", nil, ErrSelfApproval},
		{"flow allows another kind", []string{"task"}, ErrSelfApproval},
		{"flow allows docs", []string{"doc"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openTaskStore(t)                           // project "horndb", actor "stig"
			doc := docForApproval(t, s, "self-review", 295) // created_by "stig"
			tx := mustBegin(t, s)
			stampFlow(t, tx, tc.selfReview...)
			id := openApprovalID(t, tx, "doc", DocEntityID(doc.ID), "rev1", "review")

			got, err := DecideApproval(tx, DecideInput{
				ApprovalID: id, Decision: "approve", ActorID: "stig", Now: taskTestNow,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			var by *string
			if err := tx.QueryRow(
				`SELECT exception_authorized_by FROM approvals WHERE id = $1`, id).Scan(&by); err != nil {
				t.Fatal(err)
			}
			if tc.want != nil {
				if by != nil {
					t.Errorf("exception_authorized_by = %q after a refusal, want NULL", *by)
				}
				return
			}
			if got.State != "approved" {
				t.Errorf("state = %q, want approved", got.State)
			}
			if by == nil || *by != "stig" {
				t.Errorf("exception_authorized_by = %v, want stig", by)
			}
		})
	}
}

// TestDecideByAnotherActorRecordsNoSelfReview: a flow that allows self-review
// changes nothing for a reviewer who is not the author.
func TestDecideByAnotherActorRecordsNoSelfReview(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "ada", "human", "Ada", false); err != nil {
		t.Fatal(err)
	}
	doc := docForApproval(t, s, "self-review-other", 298)
	tx := mustBegin(t, s)
	stampFlow(t, tx, "doc")
	id := openApprovalID(t, tx, "doc", DocEntityID(doc.ID), "rev1", "review")

	got, err := DecideApproval(tx, DecideInput{
		ApprovalID: id, Decision: "approve", ActorID: "ada", Now: taskTestNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExceptionAuthorizedBy != nil {
		t.Errorf("exception_authorized_by = %q, want NULL", *got.ExceptionAuthorizedBy)
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
// returns the stamped flow and whether it allows self-review of the kind.
func TestSelfReviewPolicyReportsStampedFlow(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	doc := docForApproval(t, s, "029-exception-stamp", 297)

	tx := mustBegin(t, s)
	stampFlow(t, tx, "doc")
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
	if !allowed {
		t.Error("allowed = false, want true: the flow allows self-review of docs")
	}
}
