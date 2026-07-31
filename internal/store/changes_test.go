package store

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

var changesTestNow = time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)

func openChangesStore(t *testing.T) *Store {
	t.Helper()
	return openTaskStore(t)
}

// upsertPR drives UpsertPR through RecordEvent, source "github".
func upsertPR(t *testing.T, s *Store, pr PullRequest, body string) (*PullRequest, error) {
	t.Helper()
	var out *PullRequest
	_, _, err := s.RecordEvent(t.Context(), "github", nextExt(t), "pull_request", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, err = UpsertPR(tx, pr, body)
			return err
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func upsertCIRun(t *testing.T, s *Store, r CIRun) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "github", nextExt(t), "workflow_run", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UpsertCIRun(tx, r)
		})
	return err
}

func upsertReview(t *testing.T, s *Store, rv Review) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "github", nextExt(t), "pull_request_review", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UpsertReview(tx, rv)
		})
	return err
}

func defaultPR(taskID string) PullRequest {
	return PullRequest{
		Repo:     "sunstoneinstitute/demo",
		Number:   1,
		Title:    "fix the thing",
		State:    "open",
		HeadRef:  "wl/" + taskID + "-fix-the-thing",
		HeadSHA:  "abc123",
		URL:      "https://github.com/sunstoneinstitute/demo/pull/1",
		OpenedAt: changesTestNow,
	}
}

func TestTaskIDFromRef(t *testing.T) {
	cases := []struct {
		ref  string
		want string
	}{
		{"wl/HDB-123-some-slug", "HDB-123"},
		{"wl/HDB-1-x", "HDB-1"},
		{"wl/HDB-12", "HDB-12"},
		{"main", ""},
		{"feature/foo", ""},
		{"wl/wl-12-x", ""}, // lowercase wl- prefix in the id part: no match
		{"", ""},
		{"wl/-x", ""},
		{"wl/HDB-abc-x", ""}, // no digits after HDB-
	}
	for _, c := range cases {
		if got := TaskIDFromRef(c.ref); got != c.want {
			t.Errorf("TaskIDFromRef(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestTaskIDFromRefPrefixes(t *testing.T) {
	SetBranchPrefix("lode/")
	t.Cleanup(func() { SetBranchPrefix("lode/") })
	cases := map[string]string{
		"lode/WL-7-fix-thing": "WL-7",
		"lode/WL-7":           "WL-7",
		"wl/WL-7-fix-thing":   "WL-7", // legacy prefix still recognized
		"main":                "",
		"lode/wl-7-lower":     "",
	}
	for ref, want := range cases {
		if got := TaskIDFromRef(ref); got != want {
			t.Errorf("TaskIDFromRef(%q) = %q, want %q", ref, got, want)
		}
	}
	SetBranchPrefix("team/")
	if got := TaskIDFromRef("team/AB-3-x"); got != "AB-3" {
		t.Errorf("custom prefix: got %q, want AB-3", got)
	}
	if got := TaskIDFromRef("wl/AB-3-x"); got != "AB-3" {
		t.Errorf("legacy prefix under custom prefix: got %q, want AB-3", got)
	}
	if got := BranchFor(&Task{ID: "AB-3", Title: "Fix the thing"}); got != "team/AB-3-fix-the-thing" {
		t.Errorf("BranchFor = %q, want team/AB-3-fix-the-thing", got)
	}
}

// TestSetBranchPrefixNormalizes covers the separator guard: a prefix with no
// trailing "/" or "-" would otherwise yield branches like "lodeWL-7-slug".
func TestSetBranchPrefixNormalizes(t *testing.T) {
	t.Cleanup(func() { SetBranchPrefix("") })
	cases := map[string]string{
		"":      "lode/",
		"lode":  "lode/",
		"team/": "team/",
		"team-": "team-",
	}
	for in, want := range cases {
		SetBranchPrefix(in)
		if got := BranchPrefix(); got != want {
			t.Errorf("SetBranchPrefix(%q) -> BranchPrefix() = %q, want %q", in, got, want)
		}
		if got := TaskIDFromRef(want + "WL-7-slug"); got != "WL-7" {
			t.Errorf("SetBranchPrefix(%q): TaskIDFromRef(%q) = %q, want WL-7", in, want+"WL-7-slug", got)
		}
	}
}

func TestTaskIDFromBody(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"WL-Task: HDB-42", "HDB-42"},
		{"some text\nWL-Task: HDB-7\nmore text", "HDB-7"},
		{"no marker here", ""},
		{"", ""},
		{"wl-task: HDB-7", ""},          // case-sensitive prefix
		{"  WL-Task: HDB-7  ", "HDB-7"}, // surrounding whitespace on the line is trimmed
		{"WL-Task: HDB-7 trailing text", "HDB-7"},
		{"prefix WL-Task: HDB-7", ""}, // marker must start the line, not be embedded mid-line
	}
	for _, c := range cases {
		if got := TaskIDFromBody(c.body); got != c.want {
			t.Errorf("TaskIDFromBody(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

func TestTaskIDFromRefGeneralPrefix(t *testing.T) {
	if got := TaskIDFromRef("wl/SW-3-slug"); got != "SW-3" {
		t.Errorf("TaskIDFromRef = %q, want SW-3", got)
	}
	if got := TaskIDFromRef("wl/SW-3"); got != "SW-3" {
		t.Errorf("TaskIDFromRef = %q, want SW-3", got)
	}
}

func TestTaskIDFromBodyGeneralPrefix(t *testing.T) {
	if got := TaskIDFromBody("WL-Task: SW-12\nother"); got != "SW-12" {
		t.Errorf("TaskIDFromBody = %q, want SW-12", got)
	}
}

func TestUpsertPRCorrelatesViaRef(t *testing.T) {
	s := openChangesStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	pr := defaultPR(task.ID)
	got, err := upsertPR(t, s, pr, "no marker here")
	if err != nil {
		t.Fatalf("UpsertPR: %v", err)
	}
	if got.TaskID == nil || *got.TaskID != task.ID {
		t.Fatalf("PR task_id: got %v, want %s", got.TaskID, task.ID)
	}

	stored, err := s.GetPR(t.Context(), pr.Repo, pr.Number)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	if stored.TaskID == nil || *stored.TaskID != task.ID {
		t.Fatalf("stored PR task_id: got %v, want %s", stored.TaskID, task.ID)
	}
}

func TestUpsertPRCorrelatesViaBody(t *testing.T) {
	s := openChangesStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	pr := defaultPR("ignored")
	pr.HeadRef = "some-branch"
	body := "Description.\n\nWL-Task: " + task.ID + "\n"
	got, err := upsertPR(t, s, pr, body)
	if err != nil {
		t.Fatalf("UpsertPR: %v", err)
	}
	if got.TaskID == nil || *got.TaskID != task.ID {
		t.Fatalf("PR task_id via body: got %v, want %s", got.TaskID, task.ID)
	}
}

func TestUpsertPRNoMatch(t *testing.T) {
	s := openChangesStore(t)

	pr := defaultPR("nope")
	pr.HeadRef = "some-branch"
	got, err := upsertPR(t, s, pr, "no marker here")
	if err != nil {
		t.Fatalf("UpsertPR: %v", err)
	}
	if got.TaskID != nil {
		t.Fatalf("PR task_id with no correlation: got %v, want nil", got.TaskID)
	}
}

func TestUpsertPRReferencesNonexistentTaskStaysNil(t *testing.T) {
	s := openChangesStore(t)

	pr := defaultPR("HDB-999")
	got, err := upsertPR(t, s, pr, "")
	if err != nil {
		t.Fatalf("UpsertPR: %v", err)
	}
	if got.TaskID != nil {
		t.Fatalf("PR task_id referencing nonexistent task: got %v, want nil", got.TaskID)
	}
}

func TestUpsertPRUpdateKeepsTaskID(t *testing.T) {
	s := openChangesStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	pr := defaultPR(task.ID)
	if _, err := upsertPR(t, s, pr, ""); err != nil {
		t.Fatalf("first UpsertPR: %v", err)
	}

	// Redelivery with a different head_ref (no correlation) must not clear
	// the task_id already set.
	pr2 := pr
	pr2.State = "merged"
	pr2.HeadSHA = "def456"
	mergeSHA := "def456"
	pr2.MergeSHA = &mergeSHA
	pr2.Title = "fix the thing (updated)"
	merged := changesTestNow.Add(time.Hour)
	pr2.MergedAt = &merged
	pr2.HeadRef = "some-other-branch"

	got, err := upsertPR(t, s, pr2, "")
	if err != nil {
		t.Fatalf("second UpsertPR: %v", err)
	}
	if got.TaskID == nil || *got.TaskID != task.ID {
		t.Fatalf("PR task_id after update: got %v, want %s (must be kept)", got.TaskID, task.ID)
	}
	if got.State != "merged" || got.Title != "fix the thing (updated)" || got.HeadSHA != "def456" {
		t.Fatalf("PR fields after update: got %+v", got)
	}
	if got.MergeSHA == nil || *got.MergeSHA != "def456" {
		t.Fatalf("PR merge_sha after update: got %v, want def456", got.MergeSHA)
	}
	if got.MergedAt == nil || !got.MergedAt.Equal(merged) {
		t.Fatalf("PR merged_at after update: got %v, want %v", got.MergedAt, merged)
	}
}

// TestExistingPRNumbers covers the read import uses before upserting, so it
// can report new rows separately from updated ones.
func TestExistingPRNumbers(t *testing.T) {
	s := openChangesStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	pr := defaultPR(task.ID)
	if _, err := upsertPR(t, s, pr, ""); err != nil {
		t.Fatalf("upsert PR #%d: %v", pr.Number, err)
	}
	pr2 := defaultPR(task.ID)
	pr2.Number = 5
	if _, err := upsertPR(t, s, pr2, ""); err != nil {
		t.Fatalf("upsert PR #%d: %v", pr2.Number, err)
	}

	if err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		got, err := ExistingPRNumbers(tx, pr.Repo)
		if err != nil {
			return err
		}
		if len(got) != 2 || !got[1] || !got[5] {
			t.Fatalf("got %v, want {1,5}", got)
		}

		other, err := ExistingPRNumbers(tx, "acme/other")
		if err != nil {
			return err
		}
		if len(other) != 0 {
			t.Fatalf("other repo got %v, want empty", other)
		}
		return nil
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}
}

func TestGetPRNotFound(t *testing.T) {
	s := openChangesStore(t)
	if _, err := s.GetPR(t.Context(), "sunstoneinstitute/demo", 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPR unknown: want ErrNotFound, got %v", err)
	}
}

func TestPRsForTask(t *testing.T) {
	s := openChangesStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	otherTask := createTask(t, s, taskTestNow, defaultTaskInput())

	pr1 := defaultPR(task.ID)
	pr2 := defaultPR(task.ID)
	pr2.Number = 2
	pr3 := defaultPR(otherTask.ID)
	pr3.Number = 3

	for _, pr := range []PullRequest{pr1, pr2, pr3} {
		if _, err := upsertPR(t, s, pr, ""); err != nil {
			t.Fatalf("upsert PR #%d: %v", pr.Number, err)
		}
	}

	got, err := s.PRsForTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("PRsForTask: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("PRsForTask: got %d PRs, want 2", len(got))
	}
	var numbers []int64
	for _, pr := range got {
		numbers = append(numbers, pr.Number)
	}
	if !reflect.DeepEqual(numbers, []int64{1, 2}) {
		t.Fatalf("PRsForTask numbers: got %v, want [1 2]", numbers)
	}
}

func TestUpsertCIRunIdempotent(t *testing.T) {
	s := openChangesStore(t)
	r := CIRun{
		Repo:      "sunstoneinstitute/demo",
		HeadSHA:   "abc123",
		Workflow:  "ci.yml",
		Status:    "completed",
		URL:       "https://github.com/sunstoneinstitute/demo/actions/runs/1",
		StartedAt: changesTestNow,
	}
	if err := upsertCIRun(t, s, r); err != nil {
		t.Fatalf("first UpsertCIRun: %v", err)
	}
	if err := upsertCIRun(t, s, r); err != nil {
		t.Fatalf("second UpsertCIRun (redelivery): %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ci_runs`).Scan(&count); err != nil {
		t.Fatalf("count ci_runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("ci_runs count: got %d, want 1", count)
	}

	// A later delivery with the same natural key but a new conclusion
	// updates the row in place.
	conclusion := "success"
	r2 := r
	r2.Conclusion = &conclusion
	completed := changesTestNow.Add(5 * time.Minute)
	r2.CompletedAt = &completed
	if err := upsertCIRun(t, s, r2); err != nil {
		t.Fatalf("third UpsertCIRun (conclusion update): %v", err)
	}
	var gotConclusion sql.NullString
	if err := s.db.QueryRow(`SELECT conclusion FROM ci_runs`).Scan(&gotConclusion); err != nil {
		t.Fatalf("read conclusion: %v", err)
	}
	if !gotConclusion.Valid || gotConclusion.String != "success" {
		t.Fatalf("conclusion after update: got %v, want success", gotConclusion)
	}
}

func TestUpsertReviewIdempotent(t *testing.T) {
	s := openChangesStore(t)
	rv := Review{
		Repo:        "sunstoneinstitute/demo",
		PRNumber:    1,
		Reviewer:    "alice",
		State:       "approved",
		SubmittedAt: changesTestNow,
	}
	if err := upsertReview(t, s, rv); err != nil {
		t.Fatalf("first UpsertReview: %v", err)
	}
	if err := upsertReview(t, s, rv); err != nil {
		t.Fatalf("second UpsertReview (redelivery): %v", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM reviews`).Scan(&count); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if count != 1 {
		t.Fatalf("reviews count: got %d, want 1", count)
	}

	// Different reviewer/time is a distinct review.
	rv2 := rv
	rv2.Reviewer = "bob"
	rv2.State = "changes_requested"
	if err := upsertReview(t, s, rv2); err != nil {
		t.Fatalf("UpsertReview second reviewer: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM reviews`).Scan(&count); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if count != 2 {
		t.Fatalf("reviews count after second reviewer: got %d, want 2", count)
	}
}
