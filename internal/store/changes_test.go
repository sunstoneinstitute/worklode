package store

import (
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

var changesTestNow = time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)

func openChangesStore(t *testing.T) *Store {
	t.Helper()
	return openTaskStore(t)
}

// upsertPR drives UpsertPR through RecordEvent, source "github".
func upsertPR(t *testing.T, s *Store, pr PullRequest, body string) (*PullRequest, error) {
	t.Helper()
	out, _, err := upsertPRWritten(t, s, pr, body)
	return out, err
}

// upsertPRWritten is upsertPR plus UpsertPR's own write-vs-guard-rejected
// verdict, for tests that assert on it (the non-regressing guard).
func upsertPRWritten(t *testing.T, s *Store, pr PullRequest, body string) (*PullRequest, bool, error) {
	t.Helper()
	var out *PullRequest
	var written bool
	_, _, err := s.RecordEvent(t.Context(), "github", nextExt(t), "pull_request", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			out, written, err = UpsertPR(tx, pr, body)
			return err
		})
	if err != nil {
		return nil, false, err
	}
	return out, written, nil
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
		HeadRef:  taskID + "-fix-the-thing",
		HeadSHA:  "abc123",
		URL:      "https://github.com/sunstoneinstitute/demo/pull/1",
		OpenedAt: changesTestNow,
	}
}

// TestTaskIDFromRef, TestTaskIDFromRefCustomTemplate, and
// TestTaskIDFromRefGeneralPrefix are not t.Parallel(): SetBranchTemplate
// mutates a process-global (branchname_test.go's tests follow the same
// rule, for the same reason).
func TestTaskIDFromRef(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate(""); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ref  string
		want string
	}{
		{"HDB-123-some-slug", "HDB-123"},
		{"HDB-1-x", "HDB-1"},
		{"HDB-12", ""}, // no slug separator under the default template
		{"main", ""},
		{"feature/foo", ""},
		{"wl-12-x", ""}, // lowercase id: no match
		{"", ""},
		{"-x", ""},
		{"HDB-abc-x", ""}, // no digits after HDB-
	}
	for _, c := range cases {
		if got := TaskIDFromRef(c.ref); got != c.want {
			t.Errorf("TaskIDFromRef(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestTaskIDFromRefCustomTemplate(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate("lode/{{ .id }}-{{ .slug }}"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"lode/WL-7-fix-thing": "WL-7",
		"WL-7-fix-thing":      "", // no longer matches without the configured prefix
		"wl/WL-7-fix-thing":   "", // legacy prefix is not recognized (spec 008 §7)
		"main":                "",
		"lode/wl-7-lower":     "",
	}
	for ref, want := range cases {
		if got := TaskIDFromRef(ref); got != want {
			t.Errorf("TaskIDFromRef(%q) = %q, want %q", ref, got, want)
		}
	}
	if err := SetBranchTemplate("team/{{ .id }}-{{ .slug }}"); err != nil {
		t.Fatal(err)
	}
	if got := TaskIDFromRef("team/AB-3-x"); got != "AB-3" {
		t.Errorf("custom template: got %q, want AB-3", got)
	}
	if got := TaskIDFromRef("wl/AB-3-x"); got != "" {
		t.Errorf("legacy prefix must not be recognized under a custom template: got %q, want \"\"", got)
	}
	if got := BranchFor(&model.Task{ID: "AB-3", Title: "Fix the thing"}); got != "team/AB-3-fix-the-thing" {
		t.Errorf("BranchFor = %q, want team/AB-3-fix-the-thing", got)
	}
}

func TestTaskIDFromBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		body string
		want string
	}{
		{"Worklode-Task: HDB-42", "HDB-42"},
		{"some text\nWorklode-Task: HDB-7\nmore text", "HDB-7"},
		{"no marker here", ""},
		{"", ""},
		{"worklode-task: HDB-7", ""},          // case-sensitive prefix
		{"  Worklode-Task: HDB-7  ", "HDB-7"}, // surrounding whitespace on the line is trimmed
		{"Worklode-Task: HDB-7 trailing text", "HDB-7"},
		{"prefix Worklode-Task: HDB-7", ""}, // trailer must start the line, not be embedded mid-line

		// The pre-rename spelling is not recognized. Nothing ever wrote one, so
		// there is no history to be compatible with.
		{"WL-Task: HDB-42", ""},

		// No match on a bare word that merely starts the same way.
		{"Worklode-Tasks: HDB-7", ""},
		{"Worklode: HDB-7", ""},
	}
	for _, c := range cases {
		if got := TaskIDFromBody(c.body); got != c.want {
			t.Errorf("TaskIDFromBody(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

func TestTaskIDFromRefGeneralPrefix(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate(""); err != nil {
		t.Fatal(err)
	}
	if got := TaskIDFromRef("SW-3-slug"); got != "SW-3" {
		t.Errorf("TaskIDFromRef = %q, want SW-3", got)
	}
	if got := TaskIDFromRef("SW-3-"); got != "SW-3" {
		t.Errorf("TaskIDFromRef = %q, want SW-3", got)
	}
}

func TestTaskIDFromBodyGeneralPrefix(t *testing.T) {
	t.Parallel()
	if got := TaskIDFromBody("Worklode-Task: SW-12\nother"); got != "SW-12" {
		t.Errorf("TaskIDFromBody = %q, want SW-12", got)
	}
}

func TestTaskIDsFromBody(t *testing.T) {
	cases := []struct {
		body string
		want []string
	}{
		{"Worklode-Task: HDB-42", []string{"HDB-42"}},
		{"Worklode-Task: WL-1\nWorklode-Task: WL-2\nWorklode-Task: WL-3", []string{"WL-1", "WL-2", "WL-3"}},
		{"some text\nWorklode-Task: HDB-7\nmore text", []string{"HDB-7"}},
		{"no marker here", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := TaskIDsFromBody(c.body)
		if !slices.Equal(got, c.want) {
			t.Errorf("TaskIDsFromBody(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}

func TestUpsertPRCorrelatesViaRef(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	s := openChangesStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	pr := defaultPR("ignored")
	pr.HeadRef = "some-branch"
	body := "Description.\n\nWorklode-Task: " + task.ID + "\n"
	got, err := upsertPR(t, s, pr, body)
	if err != nil {
		t.Fatalf("UpsertPR: %v", err)
	}
	if got.TaskID == nil || *got.TaskID != task.ID {
		t.Fatalf("PR task_id via body: got %v, want %s", got.TaskID, task.ID)
	}
}

func TestUpsertPRNoMatch(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// TestUpsertPRNonRegressing covers the guard on pull_requests.updated_at: a
// delivery carrying an older updated_at must not overwrite newer facts, which
// is how a replayed pull_request.opened used to regress a merged PR (WL-198).
func TestUpsertPRNonRegressing(t *testing.T) {
	t.Parallel()
	s := openChangesStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	t1 := changesTestNow
	t2 := changesTestNow.Add(time.Hour)
	t3 := changesTestNow.Add(2 * time.Hour)

	mergeSHA := "def456"
	merged := defaultPR(task.ID)
	merged.State = "merged"
	merged.HeadSHA = "def456"
	merged.MergeSHA = &mergeSHA
	merged.MergedAt = &t2
	merged.UpdatedAt = t2
	if _, err := upsertPR(t, s, merged, ""); err != nil {
		t.Fatalf("seed merged PR: %v", err)
	}

	// Stale replay: an "opened" payload from before the merge.
	stale := defaultPR(task.ID)
	stale.Title = "fix the thing"
	stale.UpdatedAt = t1
	got, written, err := upsertPRWritten(t, s, stale, "")
	if err != nil {
		t.Fatalf("stale UpsertPR: %v", err)
	}
	if got.State != "merged" {
		t.Fatalf("state after stale upsert: got %q, want merged", got.State)
	}
	if got.MergeSHA == nil || *got.MergeSHA != mergeSHA {
		t.Fatalf("merge_sha after stale upsert: got %v, want %s", got.MergeSHA, mergeSHA)
	}
	if got.MergedAt == nil || !got.MergedAt.Equal(t2) {
		t.Fatalf("merged_at after stale upsert: got %v, want %v", got.MergedAt, t2)
	}
	// The guard rejected this write (WL-250): a caller that reports
	// "this PR was updated" off the returned bool, rather than off having
	// merely called UpsertPR, must see false here.
	if written {
		t.Fatalf("written after stale (guard-rejected) upsert: got true, want false")
	}

	// Equal timestamps: an ordinary redelivery of the stored payload writes.
	redelivered := merged
	redelivered.Title = "fix the thing (retitled)"
	if got, written, err = upsertPRWritten(t, s, redelivered, ""); err != nil {
		t.Fatalf("redelivery UpsertPR: %v", err)
	}
	if got.Title != "fix the thing (retitled)" {
		t.Fatalf("title after equal-timestamp redelivery: got %q, want the new title", got.Title)
	}
	if !written {
		t.Fatalf("written after equal-timestamp redelivery: got false, want true")
	}

	// Newer: applies.
	newer := defaultPR(task.ID)
	newer.State = "closed"
	newer.Title = "fix the thing (reopened then closed)"
	newer.UpdatedAt = t3
	if got, written, err = upsertPRWritten(t, s, newer, ""); err != nil {
		t.Fatalf("newer UpsertPR: %v", err)
	}
	if got.State != "closed" || got.MergeSHA != nil || got.MergedAt != nil {
		t.Fatalf("row after newer upsert: got %+v, want closed with no merge data", got)
	}
	if !written {
		t.Fatalf("written after newer upsert: got false, want true")
	}
}

// TestUpsertPRLegacyNullTimestampYields covers a row written before the
// updated_at column existed: unknown sorts as -infinity, so the first
// timestamped event applies rather than the row freezing forever.
func TestUpsertPRLegacyNullTimestampYields(t *testing.T) {
	t.Parallel()
	s := openChangesStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	legacy := defaultPR(task.ID) // zero UpdatedAt -> NULL
	if _, err := upsertPR(t, s, legacy, ""); err != nil {
		t.Fatalf("seed legacy PR: %v", err)
	}

	next := legacy
	next.State = "closed"
	next.UpdatedAt = changesTestNow.Add(time.Hour)
	got, err := upsertPR(t, s, next, "")
	if err != nil {
		t.Fatalf("timestamped UpsertPR: %v", err)
	}
	if got.State != "closed" {
		t.Fatalf("state after timestamped upsert over a NULL row: got %q, want closed", got.State)
	}
	if !got.UpdatedAt.Equal(next.UpdatedAt) {
		t.Fatalf("updated_at round trip: got %v, want %v", got.UpdatedAt, next.UpdatedAt)
	}
}

// TestUpsertCIRunNonRegressing covers the guard on ci_runs.updated_at: a
// stale in_progress delivery must not regress a completed run.
func TestUpsertCIRunNonRegressing(t *testing.T) {
	t.Parallel()
	s := openChangesStore(t)

	t1 := changesTestNow
	t2 := changesTestNow.Add(time.Hour)
	success := "success"

	done := CIRun{
		Repo:        "sunstoneinstitute/demo",
		HeadSHA:     "abc123",
		Workflow:    "ci",
		Status:      "completed",
		Conclusion:  &success,
		URL:         "https://github.com/sunstoneinstitute/demo/actions/runs/2",
		StartedAt:   t1,
		CompletedAt: &t2,
		UpdatedAt:   t2,
	}
	if err := upsertCIRun(t, s, done); err != nil {
		t.Fatalf("seed completed run: %v", err)
	}

	stale := done
	stale.Status = "in_progress"
	stale.Conclusion = nil
	stale.CompletedAt = nil
	stale.UpdatedAt = t1
	if err := upsertCIRun(t, s, stale); err != nil {
		t.Fatalf("stale UpsertCIRun: %v", err)
	}

	runs, err := s.CIRunsForSHA(t.Context(), done.Repo, done.HeadSHA)
	if err != nil {
		t.Fatalf("CIRunsForSHA: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("CIRunsForSHA: got %d runs, want 1", len(runs))
	}
	got := runs[0]
	if got.Status != "completed" {
		t.Fatalf("status after stale upsert: got %q, want completed", got.Status)
	}
	if got.Conclusion == nil || *got.Conclusion != success {
		t.Fatalf("conclusion after stale upsert: got %v, want success", got.Conclusion)
	}
	if got.CompletedAt == nil || !got.CompletedAt.Equal(t2) {
		t.Fatalf("completed_at after stale upsert: got %v, want %v", got.CompletedAt, t2)
	}
	if !got.UpdatedAt.Equal(t2) {
		t.Fatalf("updated_at round trip: got %v, want %v", got.UpdatedAt, t2)
	}
}

// TestExistingPRNumbers covers the read import uses before upserting, so it
// can report new rows separately from updated ones.
func TestExistingPRNumbers(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	s := openChangesStore(t)
	if _, err := s.GetPR(t.Context(), "sunstoneinstitute/demo", 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPR unknown: want ErrNotFound, got %v", err)
	}
}

func TestPRsForTask(t *testing.T) {
	t.Parallel()
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

// TestOpenPRsForProject covers the bulk project reader: one open PR and one
// merged PR on tasks in the project, newest UpdatedAt first; a
// closed-unmerged PR on a project task and an open PR on another project's
// task are both excluded.
func TestOpenPRsForProject(t *testing.T) {
	t.Parallel()
	s := openChangesStore(t)
	ctx := t.Context()
	taskA := createTask(t, s, taskTestNow, defaultTaskInput())
	taskB := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := s.CreateProject(ctx, "other", "Other", "OTH"); err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	otherIn := defaultTaskInput()
	otherIn.ProjectID = "other"
	otherTask := createTask(t, s, taskTestNow, otherIn)

	openA := defaultPR(taskA.ID)
	openA.Number = 1
	openA.UpdatedAt = changesTestNow

	mergedB := defaultPR(taskB.ID)
	mergedB.Number = 2
	mergedB.State = "merged"
	mergedB.UpdatedAt = changesTestNow.Add(2 * time.Hour)

	closedA := defaultPR(taskA.ID)
	closedA.Number = 3
	closedA.State = "closed"
	closedA.UpdatedAt = changesTestNow.Add(time.Hour)

	openOther := defaultPR(otherTask.ID)
	openOther.Number = 4
	openOther.UpdatedAt = changesTestNow.Add(3 * time.Hour)

	for _, pr := range []PullRequest{openA, mergedB, closedA, openOther} {
		if _, err := upsertPR(t, s, pr, ""); err != nil {
			t.Fatalf("upsert PR #%d: %v", pr.Number, err)
		}
	}

	got, err := s.OpenPRsForProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("OpenPRsForProject: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("OpenPRsForProject: got %d PRs, want 2: %+v", len(got), got)
	}
	if got[0].Number != 2 || got[1].Number != 1 {
		t.Fatalf("OpenPRsForProject order: got PR numbers [%d %d], want [2 1] (newest UpdatedAt first)", got[0].Number, got[1].Number)
	}
	if got[0].TaskID == nil || *got[0].TaskID != taskB.ID {
		t.Fatalf("OpenPRsForProject[0].TaskID: got %v, want %s", got[0].TaskID, taskB.ID)
	}
	if got[1].TaskID == nil || *got[1].TaskID != taskA.ID {
		t.Fatalf("OpenPRsForProject[1].TaskID: got %v, want %s", got[1].TaskID, taskA.ID)
	}

	if err := s.CreateProject(ctx, "empty-project", "Empty", "EMP"); err != nil {
		t.Fatalf("CreateProject empty-project: %v", err)
	}
	noPRs, err := s.OpenPRsForProject(ctx, "empty-project")
	if err != nil {
		t.Fatalf("OpenPRsForProject(empty-project): %v", err)
	}
	if noPRs == nil || len(noPRs) != 0 {
		t.Fatalf("OpenPRsForProject(empty-project): got %v, want empty non-nil slice", noPRs)
	}
}

func TestUpsertCIRunIdempotent(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// TestCIRunsForSHAs covers the bulk reader: one query answers every
// (repo, head_sha) key, and each group matches what CIRunsForSHA returns.
func TestCIRunsForSHAs(t *testing.T) {
	t.Parallel()
	s := openChangesStore(t)

	run := func(repo, sha, workflow string, offset time.Duration) CIRun {
		return CIRun{
			Repo: repo, HeadSHA: sha, Workflow: workflow, Status: "completed",
			URL:       "https://ci/" + workflow,
			StartedAt: changesTestNow.Add(offset), UpdatedAt: changesTestNow.Add(offset),
		}
	}
	seeded := []CIRun{
		run("sunstoneinstitute/demo", "sha-a", "build", 2*time.Minute),
		run("sunstoneinstitute/demo", "sha-a", "lint", time.Minute),
		run("sunstoneinstitute/demo", "sha-b", "build", time.Minute),
		run("sunstoneinstitute/other", "sha-a", "build", time.Minute),
	}
	for _, r := range seeded {
		if err := upsertCIRun(t, s, r); err != nil {
			t.Fatalf("upsertCIRun %s %s %s: %v", r.Repo, r.HeadSHA, r.Workflow, err)
		}
	}

	keys := []RepoSHA{
		{Repo: "sunstoneinstitute/demo", SHA: "sha-a"},
		{Repo: "sunstoneinstitute/demo", SHA: "sha-b"},
		{Repo: "sunstoneinstitute/demo", SHA: "sha-absent"},
	}
	got, err := s.CIRunsForSHAs(t.Context(), keys)
	if err != nil {
		t.Fatalf("CIRunsForSHAs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("CIRunsForSHAs: got %d keys, want 2 (sha-absent must be absent)", len(got))
	}
	// The other repo's run shares sha-a and must not leak into the demo group.
	for _, k := range keys {
		want, err := s.CIRunsForSHA(t.Context(), k.Repo, k.SHA)
		if err != nil {
			t.Fatalf("CIRunsForSHA %s %s: %v", k.Repo, k.SHA, err)
		}
		if !reflect.DeepEqual(got[k], want) {
			t.Fatalf("CIRunsForSHAs[%v] = %v, want %v", k, got[k], want)
		}
	}

	empty, err := s.CIRunsForSHAs(t.Context(), nil)
	if err != nil {
		t.Fatalf("CIRunsForSHAs(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("CIRunsForSHAs(nil): got %v, want empty non-nil map", empty)
	}
}

// TestReviewsForPRs covers the bulk reader: one query answers every
// (repo, number) key, and each group matches what ReviewsForPR returns.
func TestReviewsForPRs(t *testing.T) {
	t.Parallel()
	s := openChangesStore(t)

	review := func(repo string, number int64, reviewer string, offset time.Duration) Review {
		return Review{
			Repo: repo, PRNumber: number, Reviewer: reviewer, State: "approved",
			SubmittedAt: changesTestNow.Add(offset),
		}
	}
	seeded := []Review{
		review("sunstoneinstitute/demo", 1, "bob", 2*time.Minute),
		review("sunstoneinstitute/demo", 1, "alice", time.Minute),
		review("sunstoneinstitute/demo", 2, "bob", time.Minute),
		review("sunstoneinstitute/other", 1, "bob", time.Minute),
	}
	for _, rv := range seeded {
		if err := upsertReview(t, s, rv); err != nil {
			t.Fatalf("upsertReview %s#%d %s: %v", rv.Repo, rv.PRNumber, rv.Reviewer, err)
		}
	}

	keys := []RepoPR{
		{Repo: "sunstoneinstitute/demo", Number: 1},
		{Repo: "sunstoneinstitute/demo", Number: 2},
		{Repo: "sunstoneinstitute/demo", Number: 99},
	}
	got, err := s.ReviewsForPRs(t.Context(), keys)
	if err != nil {
		t.Fatalf("ReviewsForPRs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ReviewsForPRs: got %d keys, want 2 (PR 99 must be absent)", len(got))
	}
	for _, k := range keys {
		want, err := s.ReviewsForPR(t.Context(), k.Repo, k.Number)
		if err != nil {
			t.Fatalf("ReviewsForPR %s#%d: %v", k.Repo, k.Number, err)
		}
		if !reflect.DeepEqual(got[k], want) {
			t.Fatalf("ReviewsForPRs[%v] = %v, want %v", k, got[k], want)
		}
	}

	empty, err := s.ReviewsForPRs(t.Context(), nil)
	if err != nil {
		t.Fatalf("ReviewsForPRs(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("ReviewsForPRs(nil): got %v, want empty non-nil map", empty)
	}
}
