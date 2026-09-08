package store

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/progress"
)

// progressSpecBody is a spec with three anchored sections, the ones
// progressPlanBody covers at each of the three levels.
const progressSpecBody = `---
status: draft
---

# Progress

## 0. Why {#sec-0}

Why body.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// progressPlanBody covers the three sections at none/full/partial and mints
// two tasks.
const progressPlanBody = `---
status: draft
covers:
  - spec: 066-progress.md#sec-0
    coverage: none
  - spec: 066-progress.md#sec-1
    coverage: full
  - spec: 066-progress.md#sec-2
    coverage: partial
---

# Progress plan

## Tasks

### Task 1 — First task

` + "```yaml" + `
kind: feature
priority: high
blockedBy: []
` + "```" + `

Do the first thing.

### Task 2 — Second task

` + "```yaml" + `
kind: feature
priority: medium
blockedBy: []
` + "```" + `

Do the second thing.
`

// seedProgressCorpus creates the spec and the plan, accepts the plan so it
// mints its two tasks, and returns both documents plus the minted task ids in
// definition order.
func seedProgressCorpus(t *testing.T, s *Store) (specID, planID int64, taskIDs []string) {
	t.Helper()
	spec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 66, Slug: "066-progress",
		Body: progressSpecBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "066-progress-plan",
		Body: progressPlanBody, CreatedBy: "stig",
	})
	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatalf("AcceptDoc(plan): %v", err)
	}
	if len(minted) != 2 {
		t.Fatalf("minted %d tasks, want 2", len(minted))
	}
	for _, m := range minted {
		taskIDs = append(taskIDs, m.ID)
	}
	return spec.ID, plan.ID, taskIDs
}

// TestProjectProgress: one read returns the project's specs with their
// sections in document order, its plans with their covers levels, and the
// tasks each plan minted with the position each one sits at.
func TestProjectProgress(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	specID, planID, taskIDs := seedProgressCorpus(t, s)
	walkTo(t, s, taskIDs[0], "merged")

	in, err := s.ProjectProgress(t.Context(), "p1", nil)
	if err != nil {
		t.Fatalf("ProjectProgress: %v", err)
	}

	if in.Project != "p1" {
		t.Errorf("Project = %q, want p1", in.Project)
	}
	if len(in.Specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(in.Specs))
	}
	spec := in.Specs[0]
	if spec.Doc != specID {
		t.Errorf("spec Doc = %d, want %d", spec.Doc, specID)
	}
	if spec.Ref != "P1-SPEC-66" {
		t.Errorf("spec Ref = %q, want P1-SPEC-66", spec.Ref)
	}
	// Status and owner ride along for 066 §3.2's Accept button, which is
	// offered on a draft document and enabled only for its owner.
	if spec.Status != "draft" || spec.Owner != "stig" {
		t.Errorf("spec Status/Owner = %q/%q, want draft/stig", spec.Status, spec.Owner)
	}
	wantAnchors := []string{"sec-0", "sec-1", "sec-2"}
	if len(spec.Sections) != len(wantAnchors) {
		t.Fatalf("got %d sections, want %d", len(spec.Sections), len(wantAnchors))
	}
	for i, want := range wantAnchors {
		if spec.Sections[i].Anchor != want {
			t.Errorf("section %d anchor = %q, want %q", i, spec.Sections[i].Anchor, want)
		}
	}

	if len(in.Plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(in.Plans))
	}
	plan := in.Plans[0]
	if plan.Doc != planID {
		t.Errorf("plan Doc = %d, want %d", plan.Doc, planID)
	}
	if plan.Status != "accepted" {
		t.Errorf("plan Status = %q, want accepted", plan.Status)
	}
	if plan.Owner != "stig" {
		t.Errorf("plan Owner = %q, want stig", plan.Owner)
	}
	wantLevels := map[string]string{"sec-0": "none", "sec-1": "full", "sec-2": "partial"}
	if len(plan.Covers) != len(wantLevels) {
		t.Fatalf("got %d covers, want %d", len(plan.Covers), len(wantLevels))
	}
	for _, c := range plan.Covers {
		if c.Spec != specID {
			t.Errorf("cover %s Spec = %d, want %d", c.Anchor, c.Spec, specID)
		}
		if want := wantLevels[c.Anchor]; c.Level != want {
			t.Errorf("cover %s Level = %q, want %q", c.Anchor, c.Level, want)
		}
	}

	if len(plan.Tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(plan.Tasks))
	}
	byID := map[string]progress.Task{}
	for _, task := range plan.Tasks {
		byID[task.ID] = task
	}
	if got := byID[taskIDs[0]]; got.State != "merged" || got.Position != "merged" {
		t.Errorf("task %s = state %q position %q, want merged/merged",
			taskIDs[0], got.State, got.Position)
	}
	if got := byID[taskIDs[1]]; got.State != "ready" || got.Position != "ready" {
		t.Errorf("task %s = state %q position %q, want ready/ready",
			taskIDs[1], got.State, got.Position)
	}

	if in.Rally != nil {
		t.Errorf("Rally = %+v, want nil", in.Rally)
	}
}

// TestProjectProgressPositionFromPRAndCI: an open task carrying a PR whose
// head SHA has a running CI run reports the checks rung of §2.4's ladder.
func TestProjectProgressPositionFromPRAndCI(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	_, _, taskIDs := seedProgressCorpus(t, s)

	// UpsertPR ignores the TaskID field and correlates on the head ref, so
	// the branch is what binds this PR to the task.
	taskID := taskIDs[1]
	if _, err := upsertPR(t, s, PullRequest{
		Repo: "org/repo", Number: 7, Title: "Second task", State: "open",
		HeadRef: taskID + "-second-task", HeadSHA: "deadbeef",
		URL: "https://example.test/pr/7", OpenedAt: s.Now(), UpdatedAt: s.Now(),
	}, "body"); err != nil {
		t.Fatalf("UpsertPR: %v", err)
	}
	if err := upsertCIRun(t, s, CIRun{
		Repo: "org/repo", HeadSHA: "deadbeef", Workflow: "ci",
		Status: "in_progress", StartedAt: s.Now(), UpdatedAt: s.Now(),
	}); err != nil {
		t.Fatalf("UpsertCIRun: %v", err)
	}

	in, err := s.ProjectProgress(t.Context(), "p1", nil)
	if err != nil {
		t.Fatalf("ProjectProgress: %v", err)
	}
	if len(in.Plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(in.Plans))
	}
	var got string
	for _, task := range in.Plans[0].Tasks {
		if task.ID == taskID {
			got = task.Position
		}
	}
	if want := "checks running on PR #7"; got != want {
		t.Errorf("position = %q, want %q", got, want)
	}
}

// TestProjectProgressPositionQueued covers WL-SPEC-66 §2.4, §8 criterion 5:
// a task whose PR has entered the merge queue reads as "queued for merge"
// ahead of any PR/CI rung, and a task whose PR has not is unaffected.
func TestProjectProgressPositionQueued(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	_, _, taskIDs := seedProgressCorpus(t, s)

	queuedTask, openTask := taskIDs[0], taskIDs[1]
	if _, err := upsertPR(t, s, PullRequest{
		Repo: "org/repo", Number: 7, Title: "First task", State: "open",
		HeadRef: queuedTask + "-first-task", HeadSHA: "aaaaaaa",
		URL: "https://example.test/pr/7", OpenedAt: s.Now(), UpdatedAt: s.Now(),
	}, "body"); err != nil {
		t.Fatalf("UpsertPR(queued): %v", err)
	}
	if _, err := upsertPR(t, s, PullRequest{
		Repo: "org/repo", Number: 8, Title: "Second task", State: "open",
		HeadRef: openTask + "-second-task", HeadSHA: "bbbbbbb",
		URL: "https://example.test/pr/8", OpenedAt: s.Now(), UpdatedAt: s.Now(),
	}, "body"); err != nil {
		t.Fatalf("UpsertPR(open): %v", err)
	}

	queuedAt := s.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := SetPRQueued(tx, "org/repo", 7, &queuedAt); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	in, err := s.ProjectProgress(t.Context(), "p1", nil)
	if err != nil {
		t.Fatalf("ProjectProgress: %v", err)
	}
	if len(in.Plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(in.Plans))
	}
	positions := map[string]string{}
	for _, task := range in.Plans[0].Tasks {
		positions[task.ID] = task.Position
	}
	if got, want := positions[queuedTask], "queued for merge"; got != want {
		t.Errorf("queued task position = %q, want %q", got, want)
	}
	if got, want := positions[openTask], "PR #8 open"; got != want {
		t.Errorf("open task position = %q, want %q", got, want)
	}
}

// TestProjectProgressPlanningTask: the open design task about a spec rides
// along on the read, so the page can draw it as a link instead of 066 §3.4's
// Plan button. A closed one is not carried — that spec owes planning again.
func TestProjectProgressPlanningTask(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	specID, _, _ := seedProgressCorpus(t, s)

	if in, err := s.ProjectProgress(t.Context(), "p1", nil); err != nil {
		t.Fatalf("ProjectProgress: %v", err)
	} else if in.Specs[0].PlanningTask != "" {
		t.Fatalf("PlanningTask = %q before any mint, want empty", in.Specs[0].PlanningTask)
	}

	planning := createTask(t, s, s.Now(), TaskInput{
		ProjectID: "p1", Title: "Plan it", Body: "body", Priority: "medium",
		Kind: "design", AboutDoc: specID, CreatedBy: "stig",
	})
	in, err := s.ProjectProgress(t.Context(), "p1", nil)
	if err != nil {
		t.Fatalf("ProjectProgress: %v", err)
	}
	if got := in.Specs[0].PlanningTask; got != planning.ID {
		t.Fatalf("PlanningTask = %q, want %s", got, planning.ID)
	}

	// The same rule OpenTaskForDoc holds: an abandoned task is not open.
	walkTo(t, s, planning.ID, "abandoned")
	if in, err := s.ProjectProgress(t.Context(), "p1", nil); err != nil {
		t.Fatalf("ProjectProgress: %v", err)
	} else if in.Specs[0].PlanningTask != "" {
		t.Errorf("PlanningTask = %q after abandoning it, want empty", in.Specs[0].PlanningTask)
	}
}

// TestProjectProgressSpecsFilter: a non-empty specs filter narrows the read
// to those spec ids and the plans that cover at least one of them (WL-SPEC-66
// §5.2) — a second, unrelated spec and its own plan are absent from the
// result entirely, while the requested spec's own sections, covering plan
// and derived group/next act come back exactly as an unfiltered read
// produces them.
func TestProjectProgressSpecsFilter(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	specID, planID, _ := seedProgressCorpus(t, s)

	otherSpec := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 67, Slug: "067-other",
		Body: progressSpecBody, CreatedBy: "stig",
	})
	otherPlan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "067-other-plan",
		Body:      strings.ReplaceAll(progressPlanBody, "066-progress.md", "067-other.md"),
		CreatedBy: "stig",
	})
	if _, _, err := acceptDoc(t, s, otherPlan.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc(otherPlan): %v", err)
	}

	full, err := s.ProjectProgress(t.Context(), "p1", nil)
	if err != nil {
		t.Fatalf("ProjectProgress (unfiltered): %v", err)
	}
	if len(full.Specs) != 2 || len(full.Plans) != 2 {
		t.Fatalf("unfiltered read: got %d specs, %d plans, want 2, 2", len(full.Specs), len(full.Plans))
	}

	filtered, err := s.ProjectProgress(t.Context(), "p1", []int64{specID})
	if err != nil {
		t.Fatalf("ProjectProgress (filtered): %v", err)
	}
	if len(filtered.Specs) != 1 || filtered.Specs[0].Doc != specID {
		t.Fatalf("filtered specs = %+v, want just spec %d", filtered.Specs, specID)
	}
	if len(filtered.Plans) != 1 || filtered.Plans[0].Doc != planID {
		t.Fatalf("filtered plans = %+v, want just plan %d (not otherSpec's %d)",
			filtered.Plans, planID, otherSpec.ID)
	}
	if len(filtered.Plans[0].Tasks) != 2 {
		t.Fatalf("filtered plan carries %d tasks, want 2", len(filtered.Plans[0].Tasks))
	}

	fullSpec, ok := findProgressSpec(progress.Derive(full), specID)
	if !ok {
		t.Fatalf("unfiltered derivation has no spec %d", specID)
	}
	filteredSpec, ok := findProgressSpec(progress.Derive(filtered), specID)
	if !ok {
		t.Fatalf("filtered derivation has no spec %d", specID)
	}
	if filteredSpec.Group != fullSpec.Group {
		t.Errorf("filtered Group = %q, want %q (same as unfiltered)", filteredSpec.Group, fullSpec.Group)
	}
	if !reflect.DeepEqual(filteredSpec.Next, fullSpec.Next) {
		t.Errorf("filtered Next = %+v, want %+v (same as unfiltered)", filteredSpec.Next, fullSpec.Next)
	}
}

// findProgressSpec finds one spec in a derived model.ProjectProgress by its
// document id.
func findProgressSpec(p model.ProjectProgress, doc int64) (model.ProgressSpec, bool) {
	for _, g := range p.Groups {
		for _, sp := range g.Specs {
			if sp.Doc == doc {
				return sp, true
			}
		}
	}
	return model.ProgressSpec{}, false
}

// TestProgressRefs: a minted task resolves through plan_doc to the specs its
// plan covers, a planning task resolves through about_doc straight to the
// spec it is about, and a task in the project's draft rally carries that
// rally's band while a sibling task does not.
func TestProgressRefs(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	specID, planID, taskIDs := seedProgressCorpus(t, s)

	planning := createTask(t, s, s.Now(), TaskInput{
		ProjectID: "p1", Title: "Plan it", Body: "body", Priority: "medium",
		Kind: "design", AboutDoc: specID, CreatedBy: "stig",
	})

	var rally *model.Task
	if err := rallyTx(t, s, func(tx *sql.Tx, eventID int64) error {
		var err error
		rally, err = EnsureDraftRally(tx, s.Now(), "p1", "stig", eventID)
		return err
	}); err != nil {
		t.Fatalf("EnsureDraftRally: %v", err)
	}
	if err := rallyTx(t, s, func(tx *sql.Tx, eventID int64) error {
		_, err := AddRallyMembers(tx, s.Now(), rally.ID, []string{taskIDs[0]}, eventID)
		return err
	}); err != nil {
		t.Fatalf("AddRallyMembers: %v", err)
	}

	refs, err := s.ProgressRefs(t.Context(),
		[]string{taskIDs[0], taskIDs[1], planning.ID}, nil)
	if err != nil {
		t.Fatalf("ProgressRefs: %v", err)
	}
	byTask := map[string]ProgressRef{}
	for _, r := range refs {
		byTask[r.Task] = r
	}

	for _, id := range taskIDs {
		ref, ok := byTask[id]
		if !ok {
			t.Fatalf("no ref for minted task %s", id)
		}
		if ref.Plan != planID {
			t.Errorf("task %s Plan = %d, want %d", id, ref.Plan, planID)
		}
		if len(ref.Specs) != 1 || ref.Specs[0] != specID {
			t.Errorf("task %s Specs = %v, want [%d]", id, ref.Specs, specID)
		}
	}

	planningRef, ok := byTask[planning.ID]
	if !ok {
		t.Fatalf("no ref for planning task %s", planning.ID)
	}
	if planningRef.Plan != 0 {
		t.Errorf("planning task Plan = %d, want 0 (about_doc names the spec directly)", planningRef.Plan)
	}
	if len(planningRef.Specs) != 1 || planningRef.Specs[0] != specID {
		t.Errorf("planning task Specs = %v, want [%d]", planningRef.Specs, specID)
	}

	member := byTask[taskIDs[0]]
	if member.Rally == nil {
		t.Fatalf("rally member %s: Rally = nil, want set", taskIDs[0])
	}
	if member.Rally.ID != rally.ID {
		t.Errorf("rally member %s: Rally.ID = %s, want %s", taskIDs[0], member.Rally.ID, rally.ID)
	}
	if other := byTask[taskIDs[1]]; other.Rally != nil {
		t.Errorf("non-member %s: Rally = %+v, want nil", taskIDs[1], other.Rally)
	}
}
