package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// mustBegin starts a tx against s and registers a rollback cleanup, matching
// the pattern the other tx-scoped function tests in this package use.
func mustBegin(t *testing.T, s *Store) *sql.Tx {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestInsertAwaitingApprovalIsIdempotent(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	for range 2 { // second insert must be a silent no-op
		if err := InsertAwaitingApproval(tx, now,
			"pr", "acme/site#7", "abc123", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#7")
	if err != nil {
		t.Fatal(err)
	}
	if a.State != "awaiting" || a.SubjectRevision != "abc123" {
		t.Errorf("got state %q revision %q", a.State, a.SubjectRevision)
	}

	var count int
	if err := tx.QueryRow(
		`SELECT count(*) FROM approvals WHERE entity_kind = 'pr' AND entity_id = 'acme/site#7'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("got %d rows, want 1 (second insert must not duplicate)", count)
	}
}

func TestApprovalResolveApprovedClosesOpenLookup(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#8", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#8")
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveApproval(tx, a.ID, "approved", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenApprovalForEntity(tx, "pr", "acme/site#8"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got err %v, want ErrNotFound", err)
	}
}

func TestApprovalResolveChangesRequestedStaysOpen(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#9", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#9")
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveApproval(tx, a.ID, "changes_requested", nil, now); err != nil {
		t.Fatal(err)
	}
	got, err := OpenApprovalForEntity(tx, "pr", "acme/site#9")
	if err != nil {
		t.Fatalf("still-open lookup: %v", err)
	}
	if got.State != "changes_requested" {
		t.Errorf("got state %q, want changes_requested", got.State)
	}
}

func TestReopenApprovalClearsResolutionAndNoOpsOnApproved(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	if err := s.CreateActor(t.Context(), "reviewer-1", "human", "Reviewer", false); err != nil {
		t.Fatal(err)
	}
	reviewer := "reviewer-1"

	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#10", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#10")
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveApproval(tx, a.ID, "changes_requested", &reviewer, now); err != nil {
		t.Fatal(err)
	}
	resolved, err := OpenApprovalForEntity(tx, "pr", "acme/site#10")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResolvingActor == nil || *resolved.ResolvingActor != reviewer || resolved.ResolvedAt == nil {
		t.Fatalf("got resolvingActor %v resolvedAt %v, want %q/non-nil (ResolveApproval must stamp both)",
			resolved.ResolvingActor, resolved.ResolvedAt, reviewer)
	}
	if err := ReopenApproval(tx, a.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenApprovalForEntity(tx, "pr", "acme/site#10")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State != "awaiting" || reopened.ResolvingActor != nil || reopened.ResolvedAt != nil {
		t.Errorf("got state %q resolvingActor %v resolvedAt %v, want awaiting/nil/nil",
			reopened.State, reopened.ResolvingActor, reopened.ResolvedAt)
	}

	// Approved is not reopenable: ReopenApproval must leave it untouched.
	if err := ResolveApproval(tx, a.ID, "approved", &reviewer, now); err != nil {
		t.Fatal(err)
	}
	if err := ReopenApproval(tx, a.ID); err != nil {
		t.Fatal(err)
	}
	var stillState string
	if err := tx.QueryRow(`SELECT state FROM approvals WHERE id = $1`, a.ID).Scan(&stillState); err != nil {
		t.Fatal(err)
	}
	if stillState != "approved" {
		t.Errorf("got state %q, want approved (no-op)", stillState)
	}
}

func TestGetApproval(t *testing.T) {
	s := OpenTestStore(t)
	now := time.Now().UTC()

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#11", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#11")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetApproval(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EntityID != "acme/site#11" || got.State != "awaiting" {
		t.Errorf("got entityID %q state %q", got.EntityID, got.State)
	}

	if _, err := s.GetApproval(t.Context(), a.ID+1_000_000); !errors.Is(err, ErrNotFound) {
		t.Errorf("got err %v, want ErrNotFound", err)
	}
}

func TestSetRequiredActor(t *testing.T) {
	s := OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	if err := s.CreateActor(t.Context(), "actor-a", "human", "A", false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateActor(t.Context(), "actor-b", "human", "B", false); err != nil {
		t.Fatal(err)
	}

	if err := InsertAwaitingApproval(tx, now, "pr", "acme/site#12", "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	a, err := OpenApprovalForEntity(tx, "pr", "acme/site#12")
	if err != nil {
		t.Fatal(err)
	}

	// required_actor starts NULL: fills.
	if err := SetRequiredActor(tx, a.ID, "actor-a"); err != nil {
		t.Fatal(err)
	}
	got, err := OpenApprovalForEntity(tx, "pr", "acme/site#12")
	if err != nil {
		t.Fatal(err)
	}
	if got.RequiredActor == nil || *got.RequiredActor != "actor-a" {
		t.Fatalf("got requiredActor %v, want actor-a", got.RequiredActor)
	}

	// Already set: must not overwrite.
	if err := SetRequiredActor(tx, a.ID, "actor-b"); err != nil {
		t.Fatal(err)
	}
	got2, err := OpenApprovalForEntity(tx, "pr", "acme/site#12")
	if err != nil {
		t.Fatal(err)
	}
	if got2.RequiredActor == nil || *got2.RequiredActor != "actor-a" {
		t.Errorf("got requiredActor %v, want unchanged actor-a", got2.RequiredActor)
	}
}

func TestActorIDForGitHubLogin(t *testing.T) {
	s := OpenTestStore(t)
	if err := s.UpsertHumanActor(t.Context(), "octo-1", "Octocat", false, "Octocat", "", nil); err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, s)

	got, err := ActorIDForGitHubLogin(tx, "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if got != "octo-1" {
		t.Errorf("got %q, want octo-1 (case-insensitive match)", got)
	}

	miss, err := ActorIDForGitHubLogin(tx, "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if miss != "" {
		t.Errorf("got %q, want empty string for unknown login", miss)
	}
}

func TestPREntityID(t *testing.T) {
	got := PREntityID("sunstoneinstitute/worklode", 41)
	want := "sunstoneinstitute/worklode#41"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApprovalsAwaiting(t *testing.T) {
	s := openTaskStore(t) // project "horndb" (key HDB), actor "stig"
	ctx := t.Context()
	if err := s.CreateProject(ctx, "acme", "Acme", "ACM"); err != nil {
		t.Fatal(err)
	}

	taskH := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "horndb", Title: "h", Body: "b", Priority: "medium",
		Kind: "feature", CreatedBy: "stig",
	})
	taskA := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "acme", Title: "a", Body: "b", Priority: "medium",
		Kind: "feature", CreatedBy: "stig",
	})

	prH := PullRequest{
		Repo: "sunstoneinstitute/h", Number: 1, Title: "h pr", State: "open",
		HeadRef: taskH.ID + "-fix", HeadSHA: "sha1",
		URL: "https://github.com/sunstoneinstitute/h/pull/1", OpenedAt: taskTestNow,
	}
	if _, err := upsertPR(t, s, prH, ""); err != nil {
		t.Fatal(err)
	}
	prA := PullRequest{
		Repo: "sunstoneinstitute/a", Number: 1, Title: "a pr", State: "open",
		HeadRef: taskA.ID + "-fix", HeadSHA: "sha2",
		URL: "https://github.com/sunstoneinstitute/a/pull/1", OpenedAt: taskTestNow,
	}
	if _, err := upsertPR(t, s, prA, ""); err != nil {
		t.Fatal(err)
	}
	prNobody := PullRequest{
		Repo: "sunstoneinstitute/h", Number: 2, Title: "nobody pr", State: "open",
		HeadRef: taskH.ID + "-nobody", HeadSHA: "sha3",
		URL: "https://github.com/sunstoneinstitute/h/pull/2", OpenedAt: taskTestNow,
	}
	if _, err := upsertPR(t, s, prNobody, ""); err != nil {
		t.Fatal(err)
	}

	role := "science-leads"
	actorStig := "stig"
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := InsertAwaitingApproval(tx, taskTestNow, "pr",
		PREntityID(prH.Repo, prH.Number), "sha1", nil, &actorStig); err != nil {
		t.Fatal(err)
	}
	if err := InsertAwaitingApproval(tx, taskTestNow, "pr",
		PREntityID(prA.Repo, prA.Number), "sha2", &role, nil); err != nil {
		t.Fatal(err)
	}
	if err := InsertAwaitingApproval(tx, taskTestNow, "pr",
		PREntityID(prNobody.Repo, prNobody.Number), "sha3", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Named actor and named group both count: 2 total, one per project.
	got, err := s.ApprovalsAwaiting(ctx, "stig", []string{"science-leads"})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	byProject := map[string]int{}
	for _, c := range got {
		total += c.Count
		byProject[c.ProjectID] = c.Count
	}
	if total != 2 {
		t.Fatalf("got total %d, want 2: %+v", total, got)
	}
	if byProject["horndb"] != 1 || byProject["acme"] != 1 {
		t.Errorf("got counts %+v, want horndb=1 acme=1", byProject)
	}

	// Actor only, no groups: only the named-actor row counts.
	got, err = s.ApprovalsAwaiting(ctx, "stig", nil)
	if err != nil {
		t.Fatal(err)
	}
	total = 0
	for _, c := range got {
		total += c.Count
	}
	if total != 1 {
		t.Fatalf("got total %d, want 1: %+v", total, got)
	}

	// The open-instance subject (no actor, no groups) matches nothing.
	got, err = s.ApprovalsAwaiting(ctx, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want no rows for actorID=\"\" and no groups", got)
	}
}

func TestListAwaitingApprovals(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	task := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "horndb", Title: "t", Body: "b", Priority: "medium",
		Kind: "feature", CreatedBy: "stig",
	})

	prOld := PullRequest{
		Repo: "sunstoneinstitute/q", Number: 1, Title: "old pr", State: "open",
		HeadRef: task.ID + "-old", HeadSHA: "sha-old",
		URL: "https://github.com/sunstoneinstitute/q/pull/1", OpenedAt: taskTestNow,
	}
	if _, err := upsertPR(t, s, prOld, ""); err != nil {
		t.Fatal(err)
	}
	prNew := PullRequest{
		Repo: "sunstoneinstitute/q", Number: 2, Title: "new pr", State: "open",
		HeadRef: task.ID + "-new", HeadSHA: "sha-new",
		URL: "https://github.com/sunstoneinstitute/q/pull/2", OpenedAt: taskTestNow,
	}
	if _, err := upsertPR(t, s, prNew, ""); err != nil {
		t.Fatal(err)
	}
	prResolved := PullRequest{
		Repo: "sunstoneinstitute/q", Number: 3, Title: "resolved pr", State: "open",
		HeadRef: task.ID + "-resolved", HeadSHA: "sha-resolved",
		URL: "https://github.com/sunstoneinstitute/q/pull/3", OpenedAt: taskTestNow,
	}
	if _, err := upsertPR(t, s, prResolved, ""); err != nil {
		t.Fatal(err)
	}

	older := taskTestNow.Add(-2 * time.Hour)
	newer := taskTestNow.Add(-1 * time.Hour)
	actorStig := "stig"

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := InsertAwaitingApproval(tx, newer, "pr",
		PREntityID(prNew.Repo, prNew.Number), "sha-new", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := InsertAwaitingApproval(tx, older, "pr",
		PREntityID(prOld.Repo, prOld.Number), "sha-old", nil, &actorStig); err != nil {
		t.Fatal(err)
	}
	if err := InsertAwaitingApproval(tx, older.Add(-time.Hour), "pr",
		PREntityID(prResolved.Repo, prResolved.Number), "sha-resolved", nil, nil); err != nil {
		t.Fatal(err)
	}
	resolved, err := OpenApprovalForEntity(tx, "pr", PREntityID(prResolved.Repo, prResolved.Number))
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveApproval(tx, resolved.ID, "approved", nil, taskTestNow); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListAwaitingApprovals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (resolved row must be excluded): %+v", len(got), got)
	}
	if got[0].PRTitle != "old pr" || got[1].PRTitle != "new pr" {
		t.Errorf("got order %q, %q; want oldest first: old pr, new pr",
			got[0].PRTitle, got[1].PRTitle)
	}

	// Assert the rest of the SELECT list too: title/url/author,
	// task/project, and required-actor columns are same-typed strings, so a
	// transposition in the SELECT or scan would pass silently if only
	// PRTitle were checked.
	oldRow := got[0]
	if oldRow.TaskID != task.ID {
		t.Errorf("got TaskID %q, want %q", oldRow.TaskID, task.ID)
	}
	if oldRow.ProjectID != "horndb" {
		t.Errorf("got ProjectID %q, want horndb", oldRow.ProjectID)
	}
	if oldRow.ProjectName != "HornDB" {
		t.Errorf("got ProjectName %q, want HornDB", oldRow.ProjectName)
	}
	if oldRow.PRURL != prOld.URL {
		t.Errorf("got PRURL %q, want %q", oldRow.PRURL, prOld.URL)
	}
	if oldRow.RequiredActorName == nil || *oldRow.RequiredActorName != "Stig" {
		t.Errorf("got RequiredActorName %v, want Stig", oldRow.RequiredActorName)
	}

	newRow := got[1]
	if newRow.TaskID != task.ID {
		t.Errorf("got TaskID %q, want %q", newRow.TaskID, task.ID)
	}
	if newRow.ProjectID != "horndb" {
		t.Errorf("got ProjectID %q, want horndb", newRow.ProjectID)
	}
	if newRow.ProjectName != "HornDB" {
		t.Errorf("got ProjectName %q, want HornDB", newRow.ProjectName)
	}
	if newRow.PRURL != prNew.URL {
		t.Errorf("got PRURL %q, want %q", newRow.PRURL, prNew.URL)
	}
	if newRow.RequiredActorName != nil {
		t.Errorf("got RequiredActorName %v, want nil", newRow.RequiredActorName)
	}
}
