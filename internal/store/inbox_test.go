package store

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

var inboxTestNow = time.Date(2026, 7, 19, 11, 0, 0, 0, time.UTC)

// openInboxStore opens a store with the task fixtures (project "horndb",
// actor "stig").
func openInboxStore(t *testing.T) *Store {
	t.Helper()
	return openTaskStore(t)
}

// upsertIssue drives UpsertIssue through RecordEvent, source "github", the
// way a webhook handler will use it.
func upsertIssue(t *testing.T, s *Store, is Issue) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "github", nextExt(t), "issues.opened", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UpsertIssue(tx, is)
		})
	return err
}

// promoteIssue drives PromoteIssue through RecordEvent, source "cli".
func promoteIssue(t *testing.T, s *Store, now time.Time, repo string, number int64, in TaskInput, versions []string) (*model.Task, error) {
	t.Helper()
	var task *model.Task
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "issue.promote", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			task, err = PromoteIssue(tx, now, repo, number, in, versions)
			return err
		})
	if err != nil {
		return nil, err
	}
	return task, nil
}

// dismissIssue drives DismissIssue through RecordEvent, source "cli".
func dismissIssue(t *testing.T, s *Store, repo string, number int64) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "issue.dismiss", nil,
		func(tx *sql.Tx, eventID int64) error {
			return DismissIssue(tx, repo, number)
		})
	return err
}

// linkIssue drives LinkIssue through RecordEvent, source "cli".
func linkIssue(t *testing.T, s *Store, repo string, number int64, taskID string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "issue.link", nil,
		func(tx *sql.Tx, eventID int64) error {
			return LinkIssue(tx, repo, number, taskID)
		})
	return err
}

func defaultIssue() Issue {
	return Issue{
		Repo:   "sunstoneinstitute/demo",
		Number: 1,
		Title:  "something is broken",
		State:  "open",
		URL:    "https://github.com/sunstoneinstitute/demo/issues/1",
	}
}

func TestUpsertIssueInsertAndUpdate(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue (insert): %v", err)
	}

	list, err := s.ListIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListIssues: got %d issues, want 1", len(list))
	}
	got := list[0]
	if got.Repo != is.Repo || got.Number != is.Number || got.Title != is.Title ||
		got.State != is.State || got.URL != is.URL {
		t.Fatalf("inserted issue: got %+v, want fields matching %+v", got, is)
	}
	if got.TriageState != "new" {
		t.Fatalf("inserted issue triage_state: got %q, want new", got.TriageState)
	}
	if got.TaskID != nil {
		t.Fatalf("inserted issue task_id: got %v, want nil", got.TaskID)
	}
	if got.AppliesToVersions != nil {
		t.Fatalf("inserted issue applies_to_versions: got %v, want nil", got.AppliesToVersions)
	}

	// Redelivery: title/state/url update, triage_state untouched.
	is2 := is
	is2.Title = "actually it's worse"
	is2.State = "closed"
	is2.URL = "https://github.com/sunstoneinstitute/demo/issues/1#closed"
	if err := upsertIssue(t, s, is2); err != nil {
		t.Fatalf("upsert issue (update): %v", err)
	}
	list, err = s.ListIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("ListIssues after update: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListIssues after update: got %d issues, want 1", len(list))
	}
	got = list[0]
	if got.Title != is2.Title || got.State != is2.State || got.URL != is2.URL {
		t.Fatalf("updated issue: got %+v, want fields matching %+v", got, is2)
	}
	if got.TriageState != "new" {
		t.Fatalf("updated issue triage_state: got %q, want new", got.TriageState)
	}
}

func TestUpsertIssueDoesNotClobberAfterPromote(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	task, err := promoteIssue(t, s, inboxTestNow, is.Repo, is.Number, defaultTaskInput(), []string{"v1.2.0"})
	if err != nil {
		t.Fatalf("PromoteIssue: %v", err)
	}

	// Redelivery of the original webhook must not clobber triage_state,
	// task_id, or applies_to_versions.
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue (redelivery after promote): %v", err)
	}

	list, err := s.ListIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListIssues: got %d issues, want 1", len(list))
	}
	got := list[0]
	if got.TriageState != "promoted" {
		t.Fatalf("triage_state after redelivery: got %q, want promoted", got.TriageState)
	}
	if got.TaskID == nil || *got.TaskID != task.ID {
		t.Fatalf("task_id after redelivery: got %v, want %s", got.TaskID, task.ID)
	}
	if !reflect.DeepEqual(got.AppliesToVersions, []string{"v1.2.0"}) {
		t.Fatalf("applies_to_versions after redelivery: got %v, want [v1.2.0]", got.AppliesToVersions)
	}
}

func TestPromoteIssue(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	in := defaultTaskInput()
	in.Title = "fix the broken thing"
	task, err := promoteIssue(t, s, inboxTestNow, is.Repo, is.Number, in, []string{"v1.0.0", "v1.1.0"})
	if err != nil {
		t.Fatalf("PromoteIssue: %v", err)
	}
	if task.ID != "HDB-1" || task.Title != "fix the broken thing" {
		t.Fatalf("created task: got %+v", task)
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.ID != task.ID {
		t.Fatalf("GetTask round trip: got %+v", got)
	}

	list, err := s.ListIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	gotIssue := list[0]
	if gotIssue.TriageState != "promoted" {
		t.Fatalf("issue triage_state: got %q, want promoted", gotIssue.TriageState)
	}
	if gotIssue.TaskID == nil || *gotIssue.TaskID != task.ID {
		t.Fatalf("issue task_id: got %v, want %s", gotIssue.TaskID, task.ID)
	}
	if !reflect.DeepEqual(gotIssue.AppliesToVersions, []string{"v1.0.0", "v1.1.0"}) {
		t.Fatalf("issue applies_to_versions: got %v, want [v1.0.0 v1.1.0]", gotIssue.AppliesToVersions)
	}
}

func TestPromoteIssueRequiresNew(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	if _, err := promoteIssue(t, s, inboxTestNow, is.Repo, is.Number, defaultTaskInput(), nil); err != nil {
		t.Fatalf("first PromoteIssue: %v", err)
	}
	// Already promoted: second promote fails.
	if _, err := promoteIssue(t, s, inboxTestNow, is.Repo, is.Number, defaultTaskInput(), nil); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("promote already-promoted issue: want ErrBadTransition, got %v", err)
	}

	is2 := defaultIssue()
	is2.Number = 2
	if err := upsertIssue(t, s, is2); err != nil {
		t.Fatalf("upsert issue 2: %v", err)
	}
	if err := dismissIssue(t, s, is2.Repo, is2.Number); err != nil {
		t.Fatalf("dismiss issue 2: %v", err)
	}
	if _, err := promoteIssue(t, s, inboxTestNow, is2.Repo, is2.Number, defaultTaskInput(), nil); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("promote dismissed issue: want ErrBadTransition, got %v", err)
	}
}

func TestPromoteIssueNotFound(t *testing.T) {
	s := openInboxStore(t)
	if _, err := promoteIssue(t, s, inboxTestNow, "sunstoneinstitute/demo", 999, defaultTaskInput(), nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("promote unknown issue: want ErrNotFound, got %v", err)
	}
}

func TestDismissIssue(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	if err := dismissIssue(t, s, is.Repo, is.Number); err != nil {
		t.Fatalf("DismissIssue: %v", err)
	}
	list, err := s.ListIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if list[0].TriageState != "dismissed" {
		t.Fatalf("triage_state: got %q, want dismissed", list[0].TriageState)
	}
}

func TestDismissIssueNotFound(t *testing.T) {
	s := openInboxStore(t)
	if err := dismissIssue(t, s, "sunstoneinstitute/demo", 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dismiss unknown issue: want ErrNotFound, got %v", err)
	}
}

func TestDismissIssueRequiresNew(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	if err := dismissIssue(t, s, is.Repo, is.Number); err != nil {
		t.Fatalf("first dismiss: %v", err)
	}
	if err := dismissIssue(t, s, is.Repo, is.Number); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("second dismiss: want ErrBadTransition, got %v", err)
	}
}

func TestLinkIssueAttachesExistingTask(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	var taskID string
	if err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		task, err := CreateTask(tx, inboxTestNow, defaultTaskInput())
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := linkIssue(t, s, is.Repo, is.Number, taskID); err != nil {
		t.Fatalf("LinkIssue: %v", err)
	}

	list, err := s.ListIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	got := list[0]
	if got.TriageState != "promoted" {
		t.Fatalf("triage_state = %q, want promoted", got.TriageState)
	}
	if got.TaskID == nil || *got.TaskID != taskID {
		t.Fatalf("task_id = %v, want %s", got.TaskID, taskID)
	}
}

func TestLinkIssueRejectsAlreadyTriaged(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	if err := dismissIssue(t, s, is.Repo, is.Number); err != nil {
		t.Fatalf("dismiss issue: %v", err)
	}

	var taskID string
	if err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		task, err := CreateTask(tx, inboxTestNow, defaultTaskInput())
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := linkIssue(t, s, is.Repo, is.Number, taskID); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("link dismissed issue: want ErrBadTransition, got %v", err)
	}
}

func TestLinkIssueRejectsAlreadyPromoted(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}
	if _, err := promoteIssue(t, s, inboxTestNow, is.Repo, is.Number, defaultTaskInput(), nil); err != nil {
		t.Fatalf("promote issue: %v", err)
	}

	var taskID string
	if err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		task, err := CreateTask(tx, inboxTestNow, defaultTaskInput())
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// The issue already has a task from the earlier promote; a second link
	// must not silently overwrite it.
	if err := linkIssue(t, s, is.Repo, is.Number, taskID); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("link already-promoted issue: want ErrBadTransition, got %v", err)
	}
}

func TestLinkIssueRejectsMissingTask(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	if err := linkIssue(t, s, is.Repo, is.Number, "HDB-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("link to missing task: want ErrNotFound, got %v", err)
	}
}

func TestListIssuesFilter(t *testing.T) {
	s := openInboxStore(t)

	newIssue := defaultIssue()
	newIssue.Number = 1
	promotedIssue := defaultIssue()
	promotedIssue.Number = 2
	dismissedIssue := defaultIssue()
	dismissedIssue.Number = 3

	for _, is := range []Issue{newIssue, promotedIssue, dismissedIssue} {
		if err := upsertIssue(t, s, is); err != nil {
			t.Fatalf("upsert issue #%d: %v", is.Number, err)
		}
	}
	if _, err := promoteIssue(t, s, inboxTestNow, promotedIssue.Repo, promotedIssue.Number, defaultTaskInput(), nil); err != nil {
		t.Fatalf("promote #%d: %v", promotedIssue.Number, err)
	}
	if err := dismissIssue(t, s, dismissedIssue.Repo, dismissedIssue.Number); err != nil {
		t.Fatalf("dismiss #%d: %v", dismissedIssue.Number, err)
	}

	all, err := s.ListIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("ListIssues all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListIssues all: got %d, want 3", len(all))
	}

	newOnly, err := s.ListIssues(t.Context(), "new", "")
	if err != nil {
		t.Fatalf("ListIssues new: %v", err)
	}
	if len(newOnly) != 1 || newOnly[0].Number != 1 {
		t.Fatalf("ListIssues new: got %+v, want just #1", newOnly)
	}

	promotedOnly, err := s.ListIssues(t.Context(), "promoted", "")
	if err != nil {
		t.Fatalf("ListIssues promoted: %v", err)
	}
	if len(promotedOnly) != 1 || promotedOnly[0].Number != 2 {
		t.Fatalf("ListIssues promoted: got %+v, want just #2", promotedOnly)
	}

	dismissedOnly, err := s.ListIssues(t.Context(), "dismissed", "")
	if err != nil {
		t.Fatalf("ListIssues dismissed: %v", err)
	}
	if len(dismissedOnly) != 1 || dismissedOnly[0].Number != 3 {
		t.Fatalf("ListIssues dismissed: got %+v, want just #3", dismissedOnly)
	}
}

func TestListIssuesProjectFilter(t *testing.T) {
	s := openInboxStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "alpha", "Alpha", "AL"); err != nil {
		t.Fatalf("create project alpha: %v", err)
	}
	if err := s.CreateProject(ctx, "beta", "Beta", "BE"); err != nil {
		t.Fatalf("create project beta: %v", err)
	}
	if err := s.AddRepo(ctx, "alpha", "acme/alpha-app"); err != nil {
		t.Fatalf("map alpha repo: %v", err)
	}
	if err := s.AddRepo(ctx, "beta", "acme/beta-app"); err != nil {
		t.Fatalf("map beta repo: %v", err)
	}

	for _, is := range []Issue{
		{Repo: "acme/alpha-app", Number: 1, Title: "alpha", State: "open", URL: "https://example.test/1"},
		{Repo: "acme/beta-app", Number: 2, Title: "beta", State: "open", URL: "https://example.test/2"},
		{Repo: "acme/unmapped", Number: 3, Title: "unmapped", State: "open", URL: "https://example.test/3"},
	} {
		if err := upsertIssue(t, s, is); err != nil {
			t.Fatalf("upsert %s#%d: %v", is.Repo, is.Number, err)
		}
	}

	got := issueKeys(t, s, "", "alpha")
	if len(got) != 1 || got[0] != "acme/alpha-app#1" {
		t.Fatalf("project alpha = %v; want [acme/alpha-app#1]", got)
	}

	got = issueKeys(t, s, "", "")
	if len(got) != 3 {
		t.Fatalf("no project filter = %v; want all 3 issues", got)
	}

	got = issueKeys(t, s, "", "nosuchproject")
	if len(got) != 0 {
		t.Fatalf("unknown project = %v; want none", got)
	}
}

// TestExistingIssueNumbers covers the read import uses before upserting, so
// it can report new rows separately from updated ones.
func TestExistingIssueNumbers(t *testing.T) {
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue #%d: %v", is.Number, err)
	}
	is2 := defaultIssue()
	is2.Number = 5
	if err := upsertIssue(t, s, is2); err != nil {
		t.Fatalf("upsert issue #%d: %v", is2.Number, err)
	}

	if err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		got, err := ExistingIssueNumbers(tx, is.Repo)
		if err != nil {
			return err
		}
		if len(got) != 2 || !got[1] || !got[5] {
			t.Fatalf("got %v, want {1,5}", got)
		}

		other, err := ExistingIssueNumbers(tx, "acme/other")
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

// issueKeys lists issues and returns "repo#number" for each.
func issueKeys(t *testing.T, s *Store, triageState, project string) []string {
	t.Helper()
	issues, err := s.ListIssues(t.Context(), triageState, project)
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	out := make([]string, 0, len(issues))
	for _, is := range issues {
		out = append(out, fmt.Sprintf("%s#%d", is.Repo, is.Number))
	}
	return out
}
