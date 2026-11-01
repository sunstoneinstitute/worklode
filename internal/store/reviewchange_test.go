package store

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
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

const (
	changeSHA2 = "fedcba9876543210fedcba9876543210fedcba98"
	blobA      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	blobB      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func changeAnchor(path string, line int) model.ReviewAnchor {
	return model.ReviewAnchor{Repo: "r", Path: path, Side: "additions", Line: line, AnchorText: "x := 1", BlobSHA: blobA}
}

func submitChangeRound(t *testing.T, s *Store, id int64, in model.ReviewRoundInput, actor string) (*model.Review, error) {
	t.Helper()
	in.Client = "cli"
	var r *model.Review
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "review.round", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			r, err = SubmitAuthorRound(tx, taskTestNow, id, in, actor, eventID)
			return err
		})
	return r, err
}

// changeReviewWithThreads opens a change review over an in_progress task
// claimed by x, with threads at path.go:412 and util.go:7.
func changeReviewWithThreads(t *testing.T, s *Store) (r *model.Review, t1, t2 int64, task string) {
	t.Helper()
	if err := s.CreateActor(t.Context(), "x", "agent", "x", false); err != nil {
		t.Fatal(err)
	}
	seedParticipant(t, s, "horndb", "x", "member", false)
	in := defaultTaskInput()
	in.CreatedBy = "a"
	tk := createTask(t, s, taskTestNow, in)
	if err := setTaskReviewers(t, s, tk.ID, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(t.Context(), tk.ID, "x", "host:/wt", 0); err != nil {
		t.Fatal(err)
	}
	r, err := openChangeReview(t, s, model.OpenReviewInput{Task: tk.ID, BaseRef: changeSHA}, "x")
	if err != nil {
		t.Fatal(err)
	}
	var ids [2]int64
	for i, a := range []model.ReviewAnchor{changeAnchor("path.go", 412), changeAnchor("util.go", 7)} {
		th, err := openThread(t, s, r.ID, model.OpenThreadInput{Anchor: a, Intent: "change_request", Body: "fix"}, "a")
		if err != nil {
			t.Fatalf("open change thread: %v", err)
		}
		ids[i] = th.ID
	}
	return r, ids[0], ids[1], tk.ID
}

// TestChangeThread: a change thread stores its line anchor under the task
// subject; malformed and document-shaped anchors are refused (WL-REQ-1317).
func TestChangeThread(t *testing.T) {
	s := reviewFixture(t)
	r, t1, _, task := changeReviewWithThreads(t, s)
	th, err := s.GetThread(t.Context(), t1)
	if err != nil {
		t.Fatal(err)
	}
	if th.Anchor != changeAnchor("path.go", 412) || th.Review != r.ID {
		t.Errorf("thread = %+v", th)
	}
	var taskID string
	if err := s.db.QueryRowContext(t.Context(), `SELECT task_id FROM review_threads WHERE id = $1`, t1).Scan(&taskID); err != nil || taskID != task {
		t.Errorf("task_id = %q, %v, want %s", taskID, err, task)
	}
	bad := map[string]model.ReviewAnchor{
		"no text":  {Repo: "r", Path: "p", Side: "additions", Line: 1, BlobSHA: blobA},
		"bad side": {Repo: "r", Path: "p", Side: "left", Line: 1, AnchorText: "x", BlobSHA: blobA},
		"line 0":   {Repo: "r", Path: "p", Side: "additions", AnchorText: "x", BlobSHA: blobA},
		"doc":      {Doc: 1},
	}
	for name, a := range bad {
		if _, err := openThread(t, s, r.ID, model.OpenThreadInput{Anchor: a, Intent: "note", Body: "b"}, "a"); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if _, err := openThread(t, s, 0, model.OpenThreadInput{Anchor: changeAnchor("p", 1), Intent: "note", Body: "b"}, "a"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("change anchor on a bare note: err = %v, want ErrInvalidInput", err)
	}
}

// TestAuthorRoundReanchors: the author round of a change review moves or
// obsoletes the named threads (WL-REQ-1317).
func TestAuthorRoundReanchors(t *testing.T) {
	s := reviewFixture(t)
	r, t1, t2, task := changeReviewWithThreads(t, s)

	// A question open on T1 refuses the round and moves nothing.
	q, err := openThread(t, s, r.ID, model.OpenThreadInput{Anchor: changeAnchor("path.go", 412), Intent: "question", Body: "why?"}, "a")
	if err != nil {
		t.Fatal(err)
	}
	in := model.ReviewRoundInput{BaseRef: changeSHA2, Reanchors: []model.ThreadReanchor{
		{Thread: t1, Anchor: model.ReviewAnchor{Line: 415, BlobSHA: blobB}}, {Thread: t2, Status: "obsolete"}}}
	if _, err := submitChangeRound(t, s, r.ID, in, "x"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("round with open question err = %v, want ErrInvalidInput", err)
	}
	if got, _ := s.GetReview(t.Context(), r.ID); got.BaseRef != changeSHA || got.Round != 0 {
		t.Errorf("review moved: %+v", got)
	}
	_, _, err = s.RecordEvent(t.Context(), "cli", nextExt(t), "review.thread_resolved", nil, func(tx *sql.Tx, eventID int64) error {
		_, err := ResolveThread(tx, taskTestNow, q.ID, "a", "resolved", eventID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, bad := range map[string][]model.ThreadReanchor{
		"neither":    {{Thread: t1}},
		"bad status": {{Thread: t1, Status: "resolved"}},
		"foreign":    {{Thread: 999999, Status: "obsolete"}},
	} {
		if _, err := submitChangeRound(t, s, r.ID, model.ReviewRoundInput{BaseRef: changeSHA2, Reanchors: bad}, "x"); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if _, err := submitChangeRound(t, s, r.ID, model.ReviewRoundInput{BaseRef: changeSHA}, "x"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unchanged base_ref err = %v, want ErrInvalidInput", err)
	}

	got, err := submitChangeRound(t, s, r.ID, in, "x")
	if err != nil {
		t.Fatalf("SubmitAuthorRound: %v", err)
	}
	if got.BaseRef != changeSHA2 || got.Round != 1 || got.State != "open" {
		t.Errorf("review = %+v", got)
	}
	th1, _ := s.GetThread(t.Context(), t1)
	if th1.Status != "open" || th1.Anchor.Line != 415 || th1.Anchor.BlobSHA != blobB {
		t.Errorf("T1 = %+v", th1)
	}
	th2, _ := s.GetThread(t.Context(), t2)
	if th2.Status != "obsolete" || th2.ResolvedAt == nil {
		t.Errorf("T2 = %+v", th2)
	}
	for id, want := range map[int64]int{t1: 0, t2: 1} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM events WHERE type = 'wl:ReviewThreadResolved' AND payload->>'wl:subject' = $1 AND payload->>'wl:threadStatus' = 'obsolete'`,
			iri.CURIE(iri.ReviewThread(id))).Scan(&n); err != nil || n != want {
			t.Errorf("thread %d obsolete events = %d, %v, want %d", id, n, err, want)
		}
	}
	if st := changeTaskState(t, s, task); st != "in_review" {
		t.Errorf("task state = %s", st)
	}
	// T2 is closed now: naming it again is refused.
	again := model.ReviewRoundInput{BaseRef: changeSHA, Reanchors: []model.ThreadReanchor{{Thread: t2, Status: "obsolete"}}}
	if _, err := submitChangeRound(t, s, r.ID, again, "x"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("closed thread err = %v, want ErrInvalidInput", err)
	}
}

// loopReview opens a change review with roster {a} over a new task claimed by
// x, an actor the caller has created.
func loopReview(t *testing.T, s *Store) (*model.Review, string) {
	t.Helper()
	in := defaultTaskInput()
	in.CreatedBy = "a"
	task := createTask(t, s, taskTestNow, in)
	if err := setTaskReviewers(t, s, task.ID, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(t.Context(), task.ID, "x", "host:/wt-"+task.ID, 0); err != nil {
		t.Fatal(err)
	}
	r, err := openChangeReview(t, s, model.OpenReviewInput{Task: task.ID, BaseRef: changeSHA}, "x")
	if err != nil {
		t.Fatal(err)
	}
	return r, task.ID
}

func question(t *testing.T, s *Store, reviewID int64) *model.ReviewThread {
	t.Helper()
	th, err := openThread(t, s, reviewID, model.OpenThreadInput{Anchor: changeAnchor("path.go", 412), Intent: "question", Body: "why?"}, "a")
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func loopStore(t *testing.T) *Store {
	t.Helper()
	s := reviewFixture(t)
	if err := s.CreateActor(t.Context(), "x", "agent", "x", false); err != nil {
		t.Fatal(err)
	}
	seedParticipant(t, s, "horndb", "x", "member", false)
	return s
}

// TestChangeReviewLoop walks one change review from question to merge
// (WL-REQ-1324, WL-REQ-1774, WL-RULE-1321). The escalation path releases the
// lease, so it has a test of its own below.
func TestChangeReviewLoop(t *testing.T) {
	s := loopStore(t)
	ctx := t.Context()
	r, task := loopReview(t, s)

	q := question(t, s, r.ID)
	if _, _, _, err := replyThread(t, s, q.ID, "because", "x"); err != nil {
		t.Fatal(err)
	}
	if st := changeTaskState(t, s, task); st != "in_review" {
		t.Errorf("state after a reply = %s, want in_review", st)
	}
	if _, err := s.ActiveLease(ctx, task); err != nil {
		t.Errorf("lease after a reply: %v", err)
	}
	if _, err := resolveThread(t, s, q.ID, "a", "resolved"); err != nil {
		t.Fatal(err)
	}

	for _, v := range []string{"approved", "changes_requested"} {
		_, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: v}, "x")
		if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "self-approval: x holds the lease") {
			t.Errorf("%s from the lease holder: err = %v, want self-approval ErrForbidden", v, err)
		}
	}

	got, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "changes_requested"}, "a")
	if err != nil {
		t.Fatalf("changes_requested: %v", err)
	}
	if got.State != "changes_requested" {
		t.Errorf("review state = %s, want changes_requested", got.State)
	}
	if st := changeTaskState(t, s, task); st != "in_progress" {
		t.Errorf("state after changes_requested = %s, want in_progress", st)
	}
	if _, err := s.ActiveLease(ctx, task); err != nil {
		t.Errorf("lease after changes_requested: %v", err)
	}

	got, err = submitChangeRound(t, s, r.ID, model.ReviewRoundInput{BaseRef: changeSHA2}, "x")
	if err != nil {
		t.Fatalf("author round: %v", err)
	}
	if got.State != "open" {
		t.Errorf("review state after author round = %s, want open", got.State)
	}
	if st := changeTaskState(t, s, task); st != "in_review" {
		t.Errorf("state after author round = %s, want in_review", st)
	}

	if got, err = reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "approved"}, "a"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got.State != "approved" {
		t.Errorf("review state = %s, want approved", got.State)
	}
	if st := changeTaskState(t, s, task); st != "in_review" {
		t.Errorf("state after approval = %s, want in_review", st)
	}

	if err := transition(t, s, taskTestNow, task, "in_review", "merged"); err != nil {
		t.Fatal(err)
	}
	closed, err := s.GetReview(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.State != "closed" || closed.ClosedAt == nil {
		t.Errorf("review after merge = %+v", closed)
	}
	if n := reviewEvents(t, s, "wl:ReviewClosed", r.ID, "wl:outcome", "merged"); n != 1 {
		t.Errorf("ReviewClosed events = %d, want 1", n)
	}
}

// TestSelfApprovalCoversTheAssignee: the assignee is refused while another
// actor holds the lease (WL-RULE-1321).
func TestSelfApprovalCoversTheAssignee(t *testing.T) {
	s := loopStore(t)
	r, task := loopReview(t, s)
	if err := setTaskReviewers(t, s, task, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tasks SET assignee = 'b' WHERE id = $1`, task); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "approved"}, "b"); !errors.Is(err, ErrForbidden) {
		t.Errorf("verdict from the assignee: err = %v, want ErrForbidden", err)
	}
}

// TestEscalateCarriesToTheQuestion: an author who cannot answer escalates; the
// thread names the escalation, the review stays open, the task stays
// in_review and the lease goes (WL-REQ-1774).
func TestEscalateCarriesToTheQuestion(t *testing.T) {
	s := loopStore(t)
	ctx := t.Context()
	r, task := loopReview(t, s)
	q := question(t, s, r.ID)
	note, err := openThread(t, s, r.ID, model.OpenThreadInput{Anchor: changeAnchor("path.go", 412), Intent: "note", Body: "nice"}, "a")
	if err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{
		Project: "horndb", Kind: "plan", Number: 7, Slug: "007-plan",
		Body: "---\nstatus: accepted\n---\n\n# A plan\n", CreatedBy: "a",
	})

	// A caller without the lease changes nothing.
	if _, err := s.EscalateTask(ctx, EscalateInput{TaskID: task, To: "plan", DocID: plan.ID, Reason: "gap", ActorID: "a"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("escalate without the lease: err = %v, want ErrNotFound", err)
	}
	res, err := s.EscalateTask(ctx, EscalateInput{TaskID: task, To: "plan", DocID: plan.ID, Reason: "the plan is silent", ActorID: "x"})
	if err != nil {
		t.Fatalf("EscalateTask: %v", err)
	}
	got, err := s.GetReview(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "open" {
		t.Errorf("review state = %s, want open", got.State)
	}
	for _, th := range got.Threads {
		last := th.Messages[len(th.Messages)-1]
		switch th.ID {
		case q.ID:
			if len(th.Messages) != 2 || last.Role != "author" || last.Author != "x" || !strings.Contains(last.Body, res.Minted.ID) {
				t.Errorf("question messages = %+v, want an author reply naming %s", th.Messages, res.Minted.ID)
			}
			if th.Status != "open" {
				t.Errorf("question status = %s, want open", th.Status)
			}
		case note.ID:
			if len(th.Messages) != 1 {
				t.Errorf("note messages = %+v, want untouched", th.Messages)
			}
		}
	}
	if st := changeTaskState(t, s, task); st != "in_review" {
		t.Errorf("task state = %s, want in_review", st)
	}
	if _, err := s.ActiveLease(ctx, task); !errors.Is(err, ErrNotFound) {
		t.Errorf("lease after escalating: err = %v, want ErrNotFound", err)
	}
}

// TestTerminalTransitionClosesTheReview: every route into merged, abandoned or
// completed closes an open review, once.
func TestTerminalTransitionClosesTheReview(t *testing.T) {
	s := reviewFixture(t)
	for _, c := range []struct {
		path []string // from in_review
		to   string
	}{
		{nil, "merged"},
		{nil, "abandoned"},
		{nil, "completed"},
		{[]string{"in_progress"}, "merged"},
		{[]string{"in_progress"}, "abandoned"},
		{[]string{"in_progress"}, "completed"},
		{[]string{"in_progress", "ready"}, "merged"},
		{[]string{"in_progress", "ready"}, "abandoned"},
		{[]string{"in_progress", "ready"}, "completed"},
	} {
		task := createTask(t, s, taskTestNow, defaultTaskInput())
		walkTo(t, s, task.ID, "in_progress")
		r, err := openChangeReview(t, s, model.OpenReviewInput{Task: task.ID, BaseRef: changeSHA}, "opener")
		if err != nil {
			t.Fatal(err)
		}
		from := "in_review"
		for _, next := range c.path {
			if err := transition(t, s, taskTestNow, task.ID, from, next); err != nil {
				t.Fatal(err)
			}
			from = next
		}
		if got, _ := s.GetReview(t.Context(), r.ID); got.State == "closed" {
			t.Errorf("review closed on the way to %s via %v", c.to, c.path)
		}
		if err := transition(t, s, taskTestNow, task.ID, from, c.to); err != nil {
			t.Fatalf("%s -> %s: %v", from, c.to, err)
		}
		if got, _ := s.GetReview(t.Context(), r.ID); got.State != "closed" {
			t.Errorf("%s -> %s: review state = %s, want closed", from, c.to, got.State)
		}
		if n := reviewEvents(t, s, "wl:ReviewClosed", r.ID, "wl:outcome", c.to); n != 1 {
			t.Errorf("%s -> %s: ReviewClosed events = %d, want 1", from, c.to, n)
		}
		// Reopening and landing again finds no open review.
		if err := transition(t, s, taskTestNow, task.ID, c.to, "ready"); err != nil {
			t.Fatal(err)
		}
		if err := transition(t, s, taskTestNow, task.ID, "ready", c.to); err != nil {
			t.Fatal(err)
		}
		if n := reviewEvents(t, s, "wl:ReviewClosed", r.ID, "", ""); n != 1 {
			t.Errorf("%s: ReviewClosed events after a second landing = %d, want 1", c.to, n)
		}
	}
}

// TestRoundAndMergeShareALockOrder: a reviewer round and a merge take the task
// row before the review row. In the opposite order the two deadlock, because
// the merge's close hook wants the review while it holds the task.
func TestRoundAndMergeShareALockOrder(t *testing.T) {
	s := loopStore(t)
	r, task := loopReview(t, s)

	round := make(chan error, 1)
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.transition", nil, func(tx *sql.Tx, eventID int64) error {
		var state string
		if err := tx.QueryRow(`SELECT state FROM tasks WHERE id = $1 FOR UPDATE`, task).Scan(&state); err != nil {
			return err
		}
		go func() {
			_, err := reviewerRound(t, s, r.ID, model.ReviewRoundInput{Verdict: "changes_requested"}, "a")
			round <- err
		}()
		time.Sleep(300 * time.Millisecond) // lets the round reach its first lock
		return transitionKnown(tx, taskTestNow, task, state, "in_review", "merged", eventID)
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := <-round; !errors.Is(err, ErrInvalidInput) {
		t.Errorf("round after the merge: err = %v, want ErrInvalidInput (closed)", err)
	}
	if st := changeTaskState(t, s, task); st != "merged" {
		t.Errorf("task state = %s, want merged", st)
	}
}
