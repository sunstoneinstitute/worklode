package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestCreateAndGetProject(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ID != "horndb" || got.Name != "HornDB" {
		t.Fatalf("GetProject: got %+v, want id=horndb name=HornDB", got)
	}
	if len(got.Focus) != 0 {
		t.Fatalf("GetProject: got Focus=%v, want empty", got.Focus)
	}
}

func TestGetProjectNotFound(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	_, err := s.GetProject(ctx, "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject: want ErrNotFound, got %v", err)
	}
}

func TestListProjects(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject horndb: %v", err)
	}
	if err := s.CreateProject(ctx, "worklode", "Work Tracker", "WL"); err != nil {
		t.Fatalf("CreateProject worklode: %v", err)
	}

	got, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListProjects: got %d projects, want 2", len(got))
	}
	ids := []string{got[0].ID, got[1].ID}
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"horndb", "worklode"}) {
		t.Fatalf("ListProjects ids: got %v", ids)
	}
}

func TestSetProjectFocusRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	want := []string{"security", "completeness"}
	if err := s.SetProjectFocus(ctx, "horndb", want); err != nil {
		t.Fatalf("SetProjectFocus: %v", err)
	}

	got, err := s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if !reflect.DeepEqual(got.Focus, want) {
		t.Fatalf("GetProject Focus: got %v, want %v (order must be preserved)", got.Focus, want)
	}
}

func TestSetProjectFocusEmpty(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// New project already has empty focus.
	got, err := s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(got.Focus) != 0 {
		t.Fatalf("GetProject Focus on new project: got %v, want empty", got.Focus)
	}

	// Set to non-empty, then back to empty; must round-trip to empty.
	if err := s.SetProjectFocus(ctx, "horndb", []string{"usability"}); err != nil {
		t.Fatalf("SetProjectFocus non-empty: %v", err)
	}
	if err := s.SetProjectFocus(ctx, "horndb", nil); err != nil {
		t.Fatalf("SetProjectFocus nil: %v", err)
	}
	got, err = s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(got.Focus) != 0 {
		t.Fatalf("GetProject Focus after clearing: got %v, want empty", got.Focus)
	}
}

func TestSetProjectFocusMissingProject(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	err := s.SetProjectFocus(ctx, "nope", []string{"security"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetProjectFocus missing project: want ErrNotFound, got %v", err)
	}
}

func TestSetProjectFocusInvalidEntry(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	err := s.SetProjectFocus(ctx, "horndb", []string{"security", "not-a-concern"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("SetProjectFocus invalid entry: want ErrInvalidInput, got %v", err)
	}

	got, err := s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(got.Focus) != 0 {
		t.Fatalf("GetProject Focus after rejected SetProjectFocus: got %v, want unchanged (empty)", got.Focus)
	}
}

func TestPinProjectFocusRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// A new project has no pinned focus.
	got, err := s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.FocusNote != "" || got.FocusPinnedBy != "" || !got.FocusPinnedAt.IsZero() {
		t.Fatalf("new project focus: got note=%q by=%q at=%v, want all unset",
			got.FocusNote, got.FocusPinnedBy, got.FocusPinnedAt)
	}

	at := time.Date(2026, 8, 11, 15, 30, 0, 0, time.UTC)
	if err := s.PinProjectFocus(ctx, "horndb", "Ship the cockpit", "stig", at); err != nil {
		t.Fatalf("PinProjectFocus: %v", err)
	}

	got, err = s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.FocusNote != "Ship the cockpit" || got.FocusPinnedBy != "stig" || !got.FocusPinnedAt.Equal(at) {
		t.Fatalf("after pin: got note=%q by=%q at=%v, want note=%q by=%q at=%v",
			got.FocusNote, got.FocusPinnedBy, got.FocusPinnedAt, "Ship the cockpit", "stig", at)
	}

	// ListProjects reflects the same values.
	list, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListProjects: got %d, want 1", len(list))
	}
	if list[0].FocusNote != "Ship the cockpit" || list[0].FocusPinnedBy != "stig" || !list[0].FocusPinnedAt.Equal(at) {
		t.Fatalf("ListProjects focus: got note=%q by=%q at=%v",
			list[0].FocusNote, list[0].FocusPinnedBy, list[0].FocusPinnedAt)
	}

	// An empty note clears all three columns, regardless of the other args.
	if err := s.PinProjectFocus(ctx, "horndb", "", "ignored", at); err != nil {
		t.Fatalf("PinProjectFocus clear: %v", err)
	}
	got, err = s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.FocusNote != "" || got.FocusPinnedBy != "" || !got.FocusPinnedAt.IsZero() {
		t.Fatalf("after clear: got note=%q by=%q at=%v, want all unset",
			got.FocusNote, got.FocusPinnedBy, got.FocusPinnedAt)
	}
}

func TestPinProjectFocusMissingProject(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	err := s.PinProjectFocus(ctx, "nope", "note", "stig", time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("PinProjectFocus missing project: want ErrNotFound, got %v", err)
	}
}

func TestSetProjectNextDecisionRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// A new project has no next decision.
	got, err := s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.DecisionTitle != "" || got.DecisionAccountable != "" || got.DecisionReadiness != "" {
		t.Fatalf("new project decision: got title=%q accountable=%q readiness=%q, want all unset",
			got.DecisionTitle, got.DecisionAccountable, got.DecisionReadiness)
	}

	if err := s.SetProjectNextDecision(ctx, "horndb", "Pick a datastore", "stig", "blocked on benchmark"); err != nil {
		t.Fatalf("SetProjectNextDecision: %v", err)
	}

	got, err = s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.DecisionTitle != "Pick a datastore" || got.DecisionAccountable != "stig" || got.DecisionReadiness != "blocked on benchmark" {
		t.Fatalf("after set: got title=%q accountable=%q readiness=%q",
			got.DecisionTitle, got.DecisionAccountable, got.DecisionReadiness)
	}

	// ListProjects reflects the same values.
	list, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListProjects: got %d, want 1", len(list))
	}
	if list[0].DecisionTitle != "Pick a datastore" || list[0].DecisionAccountable != "stig" || list[0].DecisionReadiness != "blocked on benchmark" {
		t.Fatalf("ListProjects decision: got title=%q accountable=%q readiness=%q",
			list[0].DecisionTitle, list[0].DecisionAccountable, list[0].DecisionReadiness)
	}

	// An empty title clears all three columns, regardless of the other args.
	if err := s.SetProjectNextDecision(ctx, "horndb", "", "ignored", "ignored"); err != nil {
		t.Fatalf("SetProjectNextDecision clear: %v", err)
	}
	got, err = s.GetProject(ctx, "horndb")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.DecisionTitle != "" || got.DecisionAccountable != "" || got.DecisionReadiness != "" {
		t.Fatalf("after clear: got title=%q accountable=%q readiness=%q, want all unset",
			got.DecisionTitle, got.DecisionAccountable, got.DecisionReadiness)
	}
}

func TestSetProjectNextDecisionMissingProject(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	err := s.SetProjectNextDecision(ctx, "nope", "title", "stig", "ready")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetProjectNextDecision missing project: want ErrNotFound, got %v", err)
	}
}

func TestAddRepoAndProjectForRepo(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb"); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	got, err := s.ProjectForRepo(ctx, "sunstoneinstitute/horndb")
	if err != nil {
		t.Fatalf("ProjectForRepo: %v", err)
	}
	if got.ID != "horndb" {
		t.Fatalf("ProjectForRepo: got project %q, want horndb", got.ID)
	}
}

func TestProjectForRepoUnmapped(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	_, err := s.ProjectForRepo(ctx, "sunstoneinstitute/nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ProjectForRepo: want ErrNotFound, got %v", err)
	}
}

func TestAddRepoDuplicateSameProject(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb"); err != nil {
		t.Fatalf("AddRepo first: %v", err)
	}

	err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb")
	if !errors.Is(err, ErrRepoTaken) {
		t.Fatalf("AddRepo duplicate (same project): want ErrRepoTaken, got %v", err)
	}
}

func TestAddRepoDuplicateDifferentProject(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject horndb: %v", err)
	}
	if err := s.CreateProject(ctx, "other", "Other", "OTH"); err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb"); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	err := s.AddRepo(ctx, "other", "sunstoneinstitute/horndb")
	if !errors.Is(err, ErrRepoTaken) {
		t.Fatalf("AddRepo duplicate (different project): want ErrRepoTaken, got %v", err)
	}
}

func TestListRepos(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb"); err != nil {
		t.Fatalf("AddRepo horndb: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "sunstoneinstitute/horndb-docs"); err != nil {
		t.Fatalf("AddRepo horndb-docs: %v", err)
	}

	if err := s.SetRepoDoneState(ctx, "sunstoneinstitute/horndb", "released"); err != nil {
		t.Fatalf("SetRepoDoneState: %v", err)
	}

	got, err := s.ListRepos(ctx, "horndb")
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Repo < got[j].Repo })
	want := []RepoMapping{
		{Repo: "sunstoneinstitute/horndb", DoneState: "released"},
		{Repo: "sunstoneinstitute/horndb-docs", DoneState: "merged"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListRepos: got %v, want %v", got, want)
	}
}

func TestSetRepoDoneState(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "p1", "P1", "P1"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.AddRepo(ctx, "p1", "acme/app"); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	// A new mapping defaults to "merged".
	if got := repoDoneState(t, s, "acme/app"); got != "merged" {
		t.Fatalf("default done_state = %q, want merged", got)
	}

	// Driven from validDoneStates, not a hardcoded list, so a state added to
	// the Go validator but not to the migration's CHECK fails here rather than
	// 500ing in production.
	for state := range validDoneStates {
		if err := s.SetRepoDoneState(ctx, "acme/app", state); err != nil {
			t.Fatalf("SetRepoDoneState %q: %v", state, err)
		}
		if got := repoDoneState(t, s, "acme/app"); got != state {
			t.Fatalf("done_state after set %q = %q", state, got)
		}
	}
	// Map iteration left an arbitrary state; reset so the check below is exact.
	if err := s.SetRepoDoneState(ctx, "acme/app", DefaultDoneState); err != nil {
		t.Fatalf("SetRepoDoneState %q: %v", DefaultDoneState, err)
	}

	if err := s.SetRepoDoneState(ctx, "acme/app", "bogus"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("SetRepoDoneState bogus: want ErrInvalidInput, got %v", err)
	}
	if got := repoDoneState(t, s, "acme/app"); got != DefaultDoneState {
		t.Fatalf("done_state after rejected set = %q, want %s (unchanged)", got, DefaultDoneState)
	}

	if err := s.SetRepoDoneState(ctx, "acme/nope", "released"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetRepoDoneState unmapped repo: want ErrNotFound, got %v", err)
	}
}

// repoDoneState reads project_repos.done_state directly, so the assertions
// above do not depend on the reader under test.
func repoDoneState(t *testing.T, s *Store, repo string) string {
	t.Helper()
	var state string
	if err := s.db.QueryRow(
		`SELECT done_state FROM project_repos WHERE repo = $1`, repo).Scan(&state); err != nil {
		t.Fatalf("read done_state for %s: %v", repo, err)
	}
	return state
}

func TestPerProjectTaskNumbering(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if err := s.CreateProject(ctx, "worklode", "Worklode", "WL"); err != nil {
		t.Fatalf("create worklode: %v", err)
	}
	if err := s.CreateProject(ctx, "web", "Web", "SW"); err != nil {
		t.Fatalf("create web: %v", err)
	}

	mk := func(project string) string {
		var task *Task
		_, _, err := s.RecordEvent(ctx, "cli", mustExtID(t), "task.created", []byte(`{}`),
			func(tx *sql.Tx, eventID int64) error {
				var e error
				task, e = CreateTask(tx, s.Now(), TaskInput{
					ProjectID: project, Title: "t", Priority: "medium", Kind: "feature",
				})
				return e
			})
		if err != nil {
			t.Fatalf("create task in %s: %v", project, err)
		}
		return task.ID
	}

	if got := mk("worklode"); got != "WL-1" {
		t.Fatalf("first worklode task = %q, want WL-1", got)
	}
	if got := mk("web"); got != "SW-1" {
		t.Fatalf("first web task = %q, want SW-1", got)
	}
	if got := mk("worklode"); got != "WL-2" {
		t.Fatalf("second worklode task = %q, want WL-2", got)
	}
}

func TestCreateProjectDuplicateKey(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "a", "A", "WL"); err != nil {
		t.Fatalf("create a: %v", err)
	}
	err := s.CreateProject(ctx, "b", "B", "WL")
	if !errors.Is(err, ErrKeyTaken) {
		t.Fatalf("duplicate key err = %v, want ErrKeyTaken", err)
	}
}

// mustExtID returns a random external id for test events.
func mustExtID(t *testing.T) string {
	t.Helper()
	id, err := randomExternalID()
	if err != nil {
		t.Fatalf("ext id: %v", err)
	}
	return id
}
