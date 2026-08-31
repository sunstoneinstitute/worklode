package store

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	got := PREntityID("sunstoneinstitute/worklode", 41)
	want := "sunstoneinstitute/worklode#41"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApprovalsAwaiting(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	if got[0].Title != "old pr" || got[1].Title != "new pr" {
		t.Errorf("got order %q, %q; want oldest first: old pr, new pr",
			got[0].Title, got[1].Title)
	}

	// Assert the rest of the SELECT list too: title/url/author,
	// task/project, and required-actor columns are same-typed strings, so a
	// transposition in the SELECT or scan would pass silently if only
	// Title were checked.
	oldRow := got[0]
	if oldRow.Task != task.ID {
		t.Errorf("got TaskID %q, want %q", oldRow.Task, task.ID)
	}
	if oldRow.Project != "horndb" {
		t.Errorf("got ProjectID %q, want horndb", oldRow.Project)
	}
	if oldRow.ProjectName != "HornDB" {
		t.Errorf("got ProjectName %q, want HornDB", oldRow.ProjectName)
	}
	if oldRow.URL != prOld.URL {
		t.Errorf("got URL %q, want %q", oldRow.URL, prOld.URL)
	}
	if oldRow.RequiredActorName == nil || *oldRow.RequiredActorName != "Stig" {
		t.Errorf("got RequiredActorName %v, want Stig", oldRow.RequiredActorName)
	}

	newRow := got[1]
	if newRow.Task != task.ID {
		t.Errorf("got TaskID %q, want %q", newRow.Task, task.ID)
	}
	if newRow.Project != "horndb" {
		t.Errorf("got ProjectID %q, want horndb", newRow.Project)
	}
	if newRow.ProjectName != "HornDB" {
		t.Errorf("got ProjectName %q, want HornDB", newRow.ProjectName)
	}
	if newRow.URL != prNew.URL {
		t.Errorf("got URL %q, want %q", newRow.URL, prNew.URL)
	}
	if newRow.RequiredActorName != nil {
		t.Errorf("got RequiredActorName %v, want nil", newRow.RequiredActorName)
	}
}

// docForApproval creates a doc in the horndb project openTaskStore seeds, for
// the two doc-approval tests below.
func docForApproval(t *testing.T, s *Store, slug string, number int) *model.Doc {
	t.Helper()
	return mustCreateDoc(t, s, DocInput{
		Project: "horndb", Kind: "spec", Number: number, Slug: slug,
		Body: specBody, CreatedBy: "stig",
	})
}

// assignDocReviewers runs SetDocReviewers through RecordDocEvent, the way
// the API will, as "stig" — docForApproval's CreatedBy, so the default owner
// (025 §7.3's authority for this call).
func assignDocReviewers(t *testing.T, s *Store, docID int64, reviewers []string) {
	t.Helper()
	_, _, err := s.RecordDocEvent(t.Context(), "set_reviewers", "cli",
		fmt.Sprintf("doc-reviewers-%d", docEventSeq.Add(1)), "doc.reviewers_changed", nil,
		func(tx *sql.Tx, eventID int64) error {
			return SetDocReviewers(tx, taskTestNow, docID, "stig", reviewers, eventID)
		})
	if err != nil {
		t.Fatalf("assign reviewers %v to doc %d: %v", reviewers, docID, err)
	}
}

// TestRequestDocApprovalOpensOneLanePerReviewer is 025 §7.3's reviewer set:
// every assigned reviewer gets an own awaiting row on the same revision, and
// stays assigned across a later revision — WL-359's durable set, read fresh
// each call rather than named by the caller. Migration 0038's key allowed
// only one row per revision, so this is also the case 0057 exists for.
// Re-running must stay a no-op, which is what proves the widened ON
// CONFLICT list still infers the index.
func TestRequestDocApprovalOpensOneLanePerReviewer(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "ada", "human", "Ada", false); err != nil {
		t.Fatal(err)
	}
	doc := docForApproval(t, s, "025-lanes", 25)
	assignDocReviewers(t, s, doc.ID, []string{"stig", "ada"})

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := RequestDocApproval(tx, taskTestNow, doc.ID, 1); err != nil {
		t.Fatal(err)
	}
	// Twice: the requirement is materialized on every doc event that reads
	// the reviewer set, so duplicate calls must not duplicate lanes.
	if err := RequestDocApproval(tx, taskTestNow, doc.ID, 1); err != nil {
		t.Fatal(err)
	}
	// A newer revision is a separate set of lanes, not a reuse of these —
	// but the same durable reviewer set as before, since nothing reassigned
	// it.
	if err := RequestDocApproval(tx, taskTestNow, doc.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListAwaitingApprovals(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		if r.RequiredActor == nil {
			t.Fatalf("row %d has no required_actor: %+v", r.ID, r)
		}
		got[*r.RequiredActor+"@"+r.SubjectRevision] = r.EntityID
	}
	want := map[string]string{
		"stig@1": DocEntityID(doc.ID),
		"ada@1":  DocEntityID(doc.ID),
		"stig@2": DocEntityID(doc.ID),
		"ada@2":  DocEntityID(doc.ID),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lanes = %v, want %v", got, want)
	}
}

// TestListAwaitingApprovalsIncludesDocs: the queue is per entity_kind, and a
// doc reaches its project directly, with no task between. The inner joins the
// PR-only query used would have dropped the doc row entirely.
func TestListAwaitingApprovalsIncludesDocs(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()
	task := createTask(t, s, taskTestNow, TaskInput{
		ProjectID: "horndb", Title: "t", Body: "b", Priority: "medium",
		Kind: "feature", CreatedBy: "stig",
	})
	pr := PullRequest{
		Repo: "sunstoneinstitute/q", Number: 1, Title: "a pr", State: "open",
		HeadRef: task.ID + "-pr", HeadSHA: "sha1",
		URL: "https://github.com/sunstoneinstitute/q/pull/1", OpenedAt: taskTestNow,
	}
	if _, err := upsertPR(t, s, pr, ""); err != nil {
		t.Fatal(err)
	}
	doc := docForApproval(t, s, "026-queue", 26)
	assignDocReviewers(t, s, doc.ID, []string{"stig"})

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := InsertAwaitingApproval(tx, taskTestNow.Add(-time.Hour), "pr",
		PREntityID(pr.Repo, pr.Number), "sha1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := RequestDocApproval(tx, taskTestNow, doc.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListAwaitingApprovals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want the pr row and the doc row: %+v", len(rows), rows)
	}
	prRow, docRow := rows[0], rows[1]
	if prRow.EntityKind != "pr" || docRow.EntityKind != "doc" {
		t.Fatalf("got kinds %q, %q; want pr then doc (oldest first)",
			prRow.EntityKind, docRow.EntityKind)
	}
	if prRow.Title != "a pr" || prRow.URL != pr.URL || prRow.Task != task.ID {
		t.Errorf("pr row = %+v, want the PR's own title/url/task", prRow)
	}
	if docRow.Title != doc.Title {
		t.Errorf("doc row Title = %q, want %q", docRow.Title, doc.Title)
	}
	if want := "/docs/" + strconv.FormatInt(doc.ID, 10); docRow.URL != want {
		t.Errorf("doc row URL = %q, want %q", docRow.URL, want)
	}
	if docRow.Task != "" {
		t.Errorf("doc row TaskID = %q, want empty: a doc hangs off no task", docRow.Task)
	}
	if docRow.Project != "horndb" || docRow.ProjectName != "HornDB" {
		t.Errorf("doc row project = %q/%q, want horndb/HornDB",
			docRow.Project, docRow.ProjectName)
	}

	// The per-project badge counts both kinds through the same joins.
	counts, err := s.ApprovalsAwaiting(ctx, "stig", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[0].ProjectID != "horndb" || counts[0].Count != 1 {
		t.Errorf("ApprovalsAwaiting = %+v, want horndb:1 (the doc lane)", counts)
	}
}

// seedApprovalRow inserts one approvals row directly, committed against s.db
// rather than an open tx -- ListInboxReviews and HasInboxItems both read
// through s.db, and these fixtures need states (changes_requested, resolved)
// and created_at values InsertAwaitingApproval's always-'awaiting'/now shape
// cannot produce.
func seedApprovalRow(t *testing.T, s *Store, entityKind, entityID, subjectRevision string,
	requiredRole, requiredActor *string, state string, createdAt time.Time) int64 {
	t.Helper()
	var id int64
	err := s.db.QueryRowContext(t.Context(),
		`INSERT INTO approvals
		   (entity_kind, entity_id, subject_revision, required_role, required_actor, state, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		entityKind, entityID, subjectRevision, requiredRole, requiredActor, state, createdAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed approval %s %s: %v", entityKind, entityID, err)
	}
	return id
}

// TestListInboxReviews: 056 §3.1's org-wide read, oldest first, no
// membership predicate (that lives in the pure assembly per §3.3).
func TestListInboxReviews(t *testing.T) {
	t.Parallel()
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
		Author: "h-login",
	}
	if _, err := upsertPR(t, s, prH, ""); err != nil {
		t.Fatal(err)
	}
	prA := PullRequest{
		Repo: "sunstoneinstitute/a", Number: 1, Title: "a pr", State: "open",
		HeadRef: taskA.ID + "-fix", HeadSHA: "sha2",
		URL: "https://github.com/sunstoneinstitute/a/pull/1", OpenedAt: taskTestNow,
		Author: "a-login",
	}
	if _, err := upsertPR(t, s, prA, ""); err != nil {
		t.Fatal(err)
	}
	prClosed := PullRequest{
		Repo: "sunstoneinstitute/h", Number: 2, Title: "closed pr", State: "open",
		HeadRef: taskH.ID + "-old", HeadSHA: "sha3",
		URL: "https://github.com/sunstoneinstitute/h/pull/2", OpenedAt: taskTestNow,
	}
	if _, err := upsertPR(t, s, prClosed, ""); err != nil {
		t.Fatal(err)
	}

	role := "science-leads"
	// prH's approval is the oldest (an hour before prA's) and carries a
	// required_role lane instead of a required_actor.
	seedApprovalRow(t, s, "pr", PREntityID(prH.Repo, prH.Number), "sha1",
		&role, nil, "changes_requested", taskTestNow.Add(-time.Hour))
	seedApprovalRow(t, s, "pr", PREntityID(prA.Repo, prA.Number), "sha2",
		nil, nil, "awaiting", taskTestNow)
	// Resolved -- must not appear.
	seedApprovalRow(t, s, "pr", PREntityID(prClosed.Repo, prClosed.Number), "sha3",
		nil, nil, "approved", taskTestNow)
	// A doc-kind approval -- must not appear (entity_kind filter).
	seedApprovalRow(t, s, "doc", "doc:999", "1", nil, nil, "awaiting", taskTestNow)

	got, err := s.ListInboxReviews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (only the two open pr approvals): %+v", len(got), got)
	}
	if got[0].EntityID != PREntityID(prH.Repo, prH.Number) || got[1].EntityID != PREntityID(prA.Repo, prA.Number) {
		t.Fatalf("got entity ids %q, %q, want prH then prA (oldest first)",
			got[0].EntityID, got[1].EntityID)
	}
	if got[0].Project != "horndb" || got[1].Project != "acme" {
		t.Errorf("got projects %q, %q, want horndb, acme", got[0].Project, got[1].Project)
	}
	if got[0].AuthorLogin != "h-login" || got[1].AuthorLogin != "a-login" {
		t.Errorf("got author logins %q, %q, want h-login, a-login", got[0].AuthorLogin, got[1].AuthorLogin)
	}
	if got[0].Title != "h pr" || got[0].URL != prH.URL {
		t.Errorf("got title/url %q/%q, want %q/%q", got[0].Title, got[0].URL, "h pr", prH.URL)
	}
	if got[0].RequiredRole == nil || *got[0].RequiredRole != role {
		t.Errorf("got[0].RequiredRole = %v, want %q", got[0].RequiredRole, role)
	}
	if got[0].RequiredActor != nil {
		t.Errorf("got[0].RequiredActor = %v, want nil", got[0].RequiredActor)
	}
	if got[1].RequiredRole != nil || got[1].RequiredActor != nil {
		t.Errorf("got[1] role/actor = %v/%v, want nil/nil", got[1].RequiredRole, got[1].RequiredActor)
	}
}

// TestHasInboxItems: 056 §4's existence check, one subtest per branch the
// brief names, each proving that branch alone (no other qualifying item for
// the actor under test) rather than just an incidental true/false.
func TestHasInboxItems(t *testing.T) {
	t.Parallel()

	t.Run("empty database", func(t *testing.T) {
		t.Parallel()
		s := OpenTestStore(t)
		got, err := s.HasInboxItems(t.Context(), "nobody")
		if err != nil {
			t.Fatal(err)
		}
		if got {
			t.Error("got true, want false: nothing exists")
		}
	})

	t.Run("assigned review", func(t *testing.T) {
		t.Parallel()
		s := openTaskStore(t) // project "horndb", actor "stig"
		ctx := t.Context()
		if err := s.CreateActor(ctx, "reviewer1", "human", "Reviewer One", false); err != nil {
			t.Fatal(err)
		}
		task := createTask(t, s, taskTestNow, TaskInput{
			ProjectID: "horndb", Title: "t", Body: "b", Priority: "medium",
			Kind: "feature", CreatedBy: "stig",
		})
		pr := PullRequest{
			Repo: "sunstoneinstitute/h", Number: 1, Title: "pr", State: "open",
			HeadRef: task.ID + "-fix", HeadSHA: "sha1",
			URL: "https://github.com/sunstoneinstitute/h/pull/1", OpenedAt: taskTestNow,
		}
		if _, err := upsertPR(t, s, pr, ""); err != nil {
			t.Fatal(err)
		}
		reviewer := "reviewer1"
		seedApprovalRow(t, s, "pr", PREntityID(pr.Repo, pr.Number), "sha1",
			nil, &reviewer, "awaiting", taskTestNow)

		// reviewer1 holds no project membership -- assignment alone must
		// answer the check.
		got, err := s.HasInboxItems(ctx, "reviewer1")
		if err != nil {
			t.Fatal(err)
		}
		if !got {
			t.Error("got false, want true: reviewer1 is the review's required_actor")
		}
	})

	t.Run("unassigned review in a led project", func(t *testing.T) {
		t.Parallel()
		s := openTaskStore(t) // project "horndb", actor "stig" leads it
		ctx := t.Context()
		if err := s.CreateActor(ctx, "creator2", "human", "Creator Two", false); err != nil {
			t.Fatal(err)
		}
		task := createTask(t, s, taskTestNow, TaskInput{
			ProjectID: "horndb", Title: "t", Body: "b", Priority: "medium",
			Kind: "feature", CreatedBy: "creator2",
		})
		pr := PullRequest{
			Repo: "sunstoneinstitute/h", Number: 1, Title: "pr", State: "open",
			HeadRef: task.ID + "-fix", HeadSHA: "sha1",
			URL: "https://github.com/sunstoneinstitute/h/pull/1", OpenedAt: taskTestNow,
		}
		if _, err := upsertPR(t, s, pr, ""); err != nil {
			t.Fatal(err)
		}
		seedApprovalRow(t, s, "pr", PREntityID(pr.Repo, pr.Number), "sha1",
			nil, nil, "awaiting", taskTestNow)

		// stig neither created nor is assigned this task, and the review
		// names no required_actor -- only the led-project bucket applies.
		got, err := s.HasInboxItems(ctx, "stig")
		if err != nil {
			t.Fatal(err)
		}
		if !got {
			t.Error("got false, want true: stig leads horndb and the review is unassigned")
		}
	})

	t.Run("non-member with nothing", func(t *testing.T) {
		t.Parallel()
		s := openTaskStore(t) // project "horndb", actor "stig" leads it
		ctx := t.Context()
		if err := s.CreateActor(ctx, "outsider", "human", "Outsider", false); err != nil {
			t.Fatal(err)
		}
		// Noise: an active task and an assigned review, both stig's --
		// outsider must still see nothing.
		task := createTask(t, s, taskTestNow, TaskInput{
			ProjectID: "horndb", Title: "t", Body: "b", Priority: "medium",
			Kind: "feature", CreatedBy: "stig",
		})
		pr := PullRequest{
			Repo: "sunstoneinstitute/h", Number: 1, Title: "pr", State: "open",
			HeadRef: task.ID + "-fix", HeadSHA: "sha1",
			URL: "https://github.com/sunstoneinstitute/h/pull/1", OpenedAt: taskTestNow,
		}
		if _, err := upsertPR(t, s, pr, ""); err != nil {
			t.Fatal(err)
		}
		stig := "stig"
		seedApprovalRow(t, s, "pr", PREntityID(pr.Repo, pr.Number), "sha1",
			nil, &stig, "awaiting", taskTestNow)

		got, err := s.HasInboxItems(ctx, "outsider")
		if err != nil {
			t.Fatal(err)
		}
		if got {
			t.Error("got true, want false: outsider is not a member, lead, assignee, creator, or reviewer of anything")
		}
	})

	t.Run("active task the actor created", func(t *testing.T) {
		t.Parallel()
		s := openTaskStore(t) // project "horndb", actor "stig"
		ctx := t.Context()
		if err := s.CreateActor(ctx, "creator3", "human", "Creator Three", false); err != nil {
			t.Fatal(err)
		}
		createTask(t, s, taskTestNow, TaskInput{
			ProjectID: "horndb", Title: "t", Body: "b", Priority: "medium",
			Kind: "feature", CreatedBy: "creator3",
		})

		// creator3 holds no project membership -- authorship of the active
		// task alone must answer the check.
		got, err := s.HasInboxItems(ctx, "creator3")
		if err != nil {
			t.Fatal(err)
		}
		if !got {
			t.Error("got false, want true: creator3 created an active task")
		}
	})
}
