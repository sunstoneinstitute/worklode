package store

import (
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// reviewFixture is openTaskStore plus reviewers a and b and the actor that
// opens reviews.
func reviewFixture(t *testing.T) *Store {
	t.Helper()
	s := openTaskStore(t)
	for _, id := range []string{"a", "b", "opener"} {
		if err := s.CreateActor(t.Context(), id, "human", id, false); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// openReview runs OpenReview in its own event, the way the API will.
func openReview(t *testing.T, s *Store, docID int64) (*model.Review, error) {
	t.Helper()
	var r *model.Review
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.open", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			r, err = OpenReview(tx, taskTestNow, model.OpenReviewInput{Doc: docID}, "opener", eventID)
			return err
		})
	return r, err
}

// TestOpenDocReview: opening a review over a draft spec reads the roster
// from doc_reviewers, opens an approvals lane per reviewer (WL-REQ-1324), and
// gives each reviewer one review task, adopting the unassigned one the
// doc-lifecycle subscriber minted before minting more (WL-REQ-1778).
func TestOpenDocReview(t *testing.T) {
	s := reviewFixture(t)
	doc := docForApproval(t, s, "reviewed", 1)
	assignDocReviewers(t, s, doc.ID, []string{"a", "b"})
	lifecycle := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "horndb", Title: "Review the spec", Priority: "medium",
		Kind: "review", CreatedBy: "stig", AboutDoc: doc.ID,
	})

	r, err := openReview(t, s, doc.ID)
	if err != nil {
		t.Fatalf("OpenReview: %v", err)
	}
	if r.State != "open" || r.Subject != "doc" || r.Doc != doc.ID || r.BaseRef != "1" || r.Round != 0 || r.OpenedBy != "opener" {
		t.Errorf("review = %+v, want open doc review at base_ref 1, round 0, opened by opener", r)
	}
	if !slices.Equal(r.Reviewers, []string{"a", "b"}) {
		t.Errorf("Reviewers = %v, want [a b]", r.Reviewers)
	}

	approvals, err := s.ListApprovalsForEntityCtx(t.Context(), "doc", DocEntityID(doc.ID))
	if err != nil {
		t.Fatal(err)
	}
	var lanes []string
	for _, a := range approvals {
		if a.State == "awaiting" && a.SubjectRevision == "1" {
			lanes = append(lanes, a.Lane)
		}
	}
	slices.Sort(lanes)
	if !slices.Equal(lanes, []string{"a", "b"}) {
		t.Errorf("awaiting lanes at revision 1 = %v, want [a b]", lanes)
	}

	tasks, err := s.ListTasks(t.Context(), TaskFilter{AboutDoc: doc.ID, Kind: "review"})
	if err != nil {
		t.Fatal(err)
	}
	assignees := map[string]string{}
	for _, task := range tasks {
		assignees[task.Assignee] = task.ID
	}
	if len(tasks) != 2 || assignees["a"] != lifecycle.ID || assignees["b"] == "" {
		t.Errorf("review tasks = %v, want the lifecycle task adopted by a and one minted for b", assignees)
	}

	if _, err := openReview(t, s, doc.ID); !errors.Is(err, ErrReviewOpen) {
		t.Errorf("second OpenReview err = %v, want ErrReviewOpen", err)
	}

	bare := docForApproval(t, s, "unreviewed", 2)
	if _, err := openReview(t, s, bare.ID); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("OpenReview with no reviewers err = %v, want ErrInvalidInput", err)
	}

	_, _, err = s.RecordEvent(t.Context(), "cli", nextExt(t), "review.open", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := OpenReview(tx, taskTestNow, model.OpenReviewInput{Task: lifecycle.ID}, "opener", eventID)
			return err
		})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("OpenReview over a task err = %v, want ErrInvalidInput", err)
	}
}

// TestOpenDocReviewNoSecondTask: a reviewer who already holds an open review
// task about the document gets no second one.
func TestOpenDocReviewNoSecondTask(t *testing.T) {
	s := reviewFixture(t)
	doc := docForApproval(t, s, "held", 1)
	assignDocReviewers(t, s, doc.ID, []string{"a"})
	held := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "horndb", Title: "Review it", Priority: "medium",
		Kind: "review", CreatedBy: "stig", AboutDoc: doc.ID,
	})
	if _, err := s.db.Exec(`UPDATE tasks SET assignee = 'a' WHERE id = $1`, held.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := openReview(t, s, doc.ID); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ListTasks(t.Context(), TaskFilter{AboutDoc: doc.ID, Kind: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Errorf("review tasks = %d, want 1", len(tasks))
	}
}

// TestGetReview: the read carries the roster, each thread with its messages,
// and the rounds; reviewAuthor of a document review is the document's owner.
func TestGetReview(t *testing.T) {
	s := reviewFixture(t)
	doc := docForApproval(t, s, "read", 1)
	assignDocReviewers(t, s, doc.ID, []string{"a", "b"})
	r, err := openReview(t, s, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	var thread int64
	if err := s.db.QueryRow(
		`INSERT INTO review_threads (review_id, doc_id, version, byte_start, byte_end, quote, doc_anchor, intent, author_id, created_at)
		 VALUES ($1, $2, 1, 0, 3, '---', 'sec-1', 'question', 'a', $3) RETURNING id`,
		r.ID, doc.ID, taskTestNow).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO review_messages (thread_id, review_id, seq, actor_id, role, body, created_at)
		 VALUES ($1, $2, 1, 'a', 'reviewer', 'why?', $3), ($1, $2, 2, 'stig', 'author', 'because', $3)`,
		thread, r.ID, taskTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO review_rounds (review_id, round, actor_id, client, base_ref, verdict, submitted_at)
		 VALUES ($1, 1, 'a', 'cli', '1', 'approved', $2)`, r.ID, taskTestNow); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetReview(t.Context(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Reviewers, []string{"a", "b"}) {
		t.Errorf("Reviewers = %v", got.Reviewers)
	}
	if len(got.Threads) != 1 {
		t.Fatalf("Threads = %+v, want 1", got.Threads)
	}
	th := got.Threads[0]
	if th.Intent != "question" || th.Status != "open" || th.Author != "a" || th.Anchor.Doc != doc.ID ||
		th.Anchor.Quote != "---" || th.Anchor.ByteEnd != 3 || th.Anchor.DocAnchor != "sec-1" {
		t.Errorf("thread = %+v", th)
	}
	if len(th.Messages) != 2 || th.Messages[0].Body != "why?" || th.Messages[1].Role != "author" || th.Messages[1].Seq != 2 {
		t.Errorf("messages = %+v", th.Messages)
	}
	if len(got.Rounds) != 1 || got.Rounds[0].Verdict != "approved" || got.Rounds[0].Actor != "a" || got.Rounds[0].Client != "cli" {
		t.Errorf("rounds = %+v", got.Rounds)
	}

	if _, err := s.GetReview(t.Context(), r.ID+1000); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetReview(missing) err = %v, want ErrNotFound", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	author, err := reviewAuthor(tx, got)
	if err != nil || author != "stig" {
		t.Errorf("reviewAuthor = %q, %v, want stig", author, err)
	}
}

// TestListReviews: subject, reviewer (in the roster), state and open filter.
func TestListReviews(t *testing.T) {
	s := reviewFixture(t)
	d1 := docForApproval(t, s, "one", 1)
	d2 := docForApproval(t, s, "two", 2)
	assignDocReviewers(t, s, d1.ID, []string{"a"})
	assignDocReviewers(t, s, d2.ID, []string{"b"})
	r1, err := openReview(t, s, d1.ID)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := openReview(t, s, d2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE reviews SET state = 'closed', closed_at = $2 WHERE id = $1`, r2.ID, taskTestNow); err != nil {
		t.Fatal(err)
	}

	ids := func(p model.ReviewListParams) []int64 {
		t.Helper()
		got, err := s.ListReviews(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, r := range got {
			out = append(out, r.ID)
		}
		return out
	}
	for name, tc := range map[string]struct {
		p    model.ReviewListParams
		want []int64
	}{
		"all":      {model.ReviewListParams{}, []int64{r1.ID, r2.ID}},
		"subject":  {model.ReviewListParams{Subject: "doc"}, []int64{r1.ID, r2.ID}},
		"change":   {model.ReviewListParams{Subject: "change"}, nil},
		"reviewer": {model.ReviewListParams{Reviewer: "b"}, []int64{r2.ID}},
		"state":    {model.ReviewListParams{State: "open"}, []int64{r1.ID}},
		"open":     {model.ReviewListParams{Open: true}, []int64{r1.ID}},
	} {
		if got := ids(tc.p); !slices.Equal(got, tc.want) {
			t.Errorf("%s: ids = %v, want %v", name, got, tc.want)
		}
	}
}
