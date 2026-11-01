package store

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

const changeSHA = "0123456789abcdef0123456789abcdef01234567"

func openChangeReview(t *testing.T, s *Store, in model.OpenReviewInput, actor string) (*model.Review, error) {
	t.Helper()
	var r *model.Review
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.open", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			r, err = OpenReview(tx, taskTestNow, in, actor, eventID)
			return err
		})
	return r, err
}

func changeTaskState(t *testing.T, s *Store, id string) string {
	t.Helper()
	task, err := s.GetTask(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return task.State
}

// TestOpenChangeReview: a review over an in_progress task moves it to
// in_review, keeps the lease, mints one assigned review task per reviewer that
// the reviewed task dependsOn, and stores the reason (WL-REQ-1324,
// WL-REQ-1778, WL-REQ-1780).
func TestOpenChangeReview(t *testing.T) {
	s := reviewFixture(t)
	if err := s.CreateActor(t.Context(), "h", "human", "h", false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateActor(t.Context(), "x", "agent", "x", false); err != nil {
		t.Fatal(err)
	}
	seedParticipant(t, s, "horndb", "x", "member", false)
	in := defaultTaskInput()
	in.CreatedBy = "h"
	task := createTask(t, s, taskTestNow, in)
	if err := setTaskReviewers(t, s, task.ID, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(t.Context(), task.ID, "x", "host:/wt", 0); err != nil {
		t.Fatal(err)
	}
	pr := defaultPR(task.ID)
	if _, _, err := upsertPRWritten(t, s, pr, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := openChangeReview(t, s, model.OpenReviewInput{Task: task.ID, BaseRef: "main"}, "x"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bad sha err = %v, want ErrInvalidInput", err)
	}
	r, err := openChangeReview(t, s, model.OpenReviewInput{Task: task.ID, BaseRef: changeSHA, Reason: "touches the route guard table"}, "x")
	if err != nil {
		t.Fatalf("OpenReview: %v", err)
	}
	if r.Subject != "change" || r.Task != task.ID || r.BaseRef != changeSHA || r.Reason != "touches the route guard table" {
		t.Errorf("review = %+v", r)
	}
	if got := changeTaskState(t, s, task.ID); got != "in_review" {
		t.Errorf("task state = %s, want in_review", got)
	}
	if _, err := s.ActiveLease(t.Context(), task.ID); err != nil {
		t.Errorf("lease not active: %v", err)
	}
	out, _, err := s.ListEdges(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	assignees := map[string]bool{}
	for _, e := range out {
		if e.Type != "dependsOn" {
			continue
		}
		rt, err := s.GetTask(t.Context(), e.ToTask)
		if err != nil {
			t.Fatal(err)
		}
		if rt.Kind != "review" || !strings.Contains(string(rt.Body), pr.URL) {
			t.Errorf("review task %s kind %s body %q, want review with PR URL", rt.ID, rt.Kind, rt.Body)
		}
		assignees[rt.Assignee] = true
	}
	if len(assignees) != 2 || !assignees["a"] || !assignees["b"] {
		t.Errorf("review task assignees = %v, want a and b", assignees)
	}
	got, err := s.ReviewForTask(t.Context(), task.ID)
	if err != nil || got.Reason != "touches the route guard table" {
		t.Errorf("ReviewForTask = %+v, %v", got, err)
	}
	if _, err := openChangeReview(t, s, model.OpenReviewInput{Task: task.ID, BaseRef: changeSHA}, "x"); !errors.Is(err, ErrReviewOpen) {
		t.Errorf("second open err = %v, want ErrReviewOpen", err)
	}
	var author string
	_, _, err = s.RecordEvent(t.Context(), "cli", nextExt(t), "t", nil, func(tx *sql.Tx, _ int64) error {
		var err error
		author, err = reviewAuthor(tx, r)
		return err
	})
	if err != nil || author != "x" {
		t.Errorf("reviewAuthor = %q, %v, want x", author, err)
	}
}

// TestOpenChangeReviewDefaultRoster: no reviewers means the creator when
// human, else the project lead (WL-REQ-1779).
func TestOpenChangeReviewDefaultRoster(t *testing.T) {
	s := reviewFixture(t)
	if err := s.CreateActor(t.Context(), "bot", "agent", "bot", false); err != nil {
		t.Fatal(err)
	}
	walk := func(createdBy string) *model.Task {
		in := defaultTaskInput()
		in.CreatedBy = createdBy
		task := createTask(t, s, taskTestNow, in)
		walkTo(t, s, task.ID, "in_progress")
		return task
	}
	for createdBy, want := range map[string]string{"a": "a", "bot": "stig"} {
		task := walk(createdBy)
		r, err := openChangeReview(t, s, model.OpenReviewInput{Task: task.ID, BaseRef: changeSHA}, "opener")
		if err != nil {
			t.Fatalf("created by %s: %v", createdBy, err)
		}
		if !slices.Equal(r.Reviewers, []string{want}) {
			t.Errorf("created by %s: roster = %v, want [%s]", createdBy, r.Reviewers, want)
		}
	}
}

// TestOpenChangeReviewStates: the WL-REQ-1324 state rule.
func TestOpenChangeReviewStates(t *testing.T) {
	s := reviewFixture(t)
	cases := []struct {
		state      string
		allowReady bool
		wantErr    bool
		wantState  string
	}{
		{"in_progress", false, false, "in_review"},
		{"in_review", false, false, "in_review"},
		{"ready", false, true, "ready"},
		{"ready", true, false, "in_review"},
		{"draft", false, true, "draft"},
		{"merged", false, true, "merged"},
		{"abandoned", false, true, "abandoned"},
		{"completed", false, true, "completed"},
	}
	for _, c := range cases {
		in := defaultTaskInput()
		in.Draft = c.state == "draft"
		task := createTask(t, s, taskTestNow, in)
		if c.state != "draft" && c.state != "ready" {
			walkTo(t, s, task.ID, c.state)
		}
		_, err := openChangeReview(t, s, model.OpenReviewInput{Task: task.ID, BaseRef: changeSHA, AllowReady: c.allowReady}, "opener")
		if c.wantErr != errors.Is(err, ErrInvalidInput) || (!c.wantErr && err != nil) {
			t.Errorf("%s allowReady=%v: err = %v, wantErr %v", c.state, c.allowReady, err, c.wantErr)
		}
		if got := changeTaskState(t, s, task.ID); got != c.wantState {
			t.Errorf("%s allowReady=%v: state = %s, want %s", c.state, c.allowReady, got, c.wantState)
		}
	}
}
