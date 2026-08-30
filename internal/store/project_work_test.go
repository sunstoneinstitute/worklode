package store

import (
	"database/sql"
	"testing"
	"time"
)

// openProjectWorkStore opens a test store with two projects ("project-a",
// "project-b") and the actors ListProjectWorkFacts tests need: "stig" (the
// default CreatedBy in defaultTaskInput) and "agent-x" (an agent that claims
// tasks).
func openProjectWorkStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "project-a", "Project A", "PA"); err != nil {
		t.Fatalf("CreateProject project-a: %v", err)
	}
	if err := s.CreateProject(ctx, "project-b", "Project B", "PB"); err != nil {
		t.Fatalf("CreateProject project-b: %v", err)
	}
	if err := s.CreateActor(ctx, "stig", "human", "Stig", false); err != nil {
		t.Fatalf("CreateActor stig: %v", err)
	}
	if err := s.CreateActor(ctx, "agent-x", "agent", "Agent X", false); err != nil {
		t.Fatalf("CreateActor agent-x: %v", err)
	}
	return s
}

// projectWorkTaskInput builds a TaskInput for the given project, priority,
// kind, and title, otherwise matching defaultTaskInput.
func projectWorkTaskInput(project, priority, kind, title string) TaskInput {
	in := defaultTaskInput()
	in.ProjectID = project
	in.Priority = priority
	in.Kind = kind
	in.Title = title
	return in
}

// factFor returns the fact for taskID, failing the test if it is absent.
func factFor(t *testing.T, facts []ProjectWorkFact, taskID string) ProjectWorkFact {
	t.Helper()
	for _, f := range facts {
		if f.Task.ID == taskID {
			return f
		}
	}
	t.Fatalf("no fact for task %s in %#v", taskID, facts)
	return ProjectWorkFact{}
}

// recordStateEvent inserts one events row (source, externalID) and one
// state_log field:state row against taskID, both stamped with the given at,
// bypassing LogChange (which always stamps time.Now()) so tests can force
// exact at ties and isolate the id DESC tie-break. Returns the state_log
// row's own id.
func recordStateEvent(t *testing.T, s *Store, source, externalID, taskID string, at time.Time) int64 {
	t.Helper()
	var eventID int64
	if err := s.DBForTests().QueryRow(
		`INSERT INTO events (source, external_id, type, received_at) VALUES ($1, $2, 'test.state', $3) RETURNING id`,
		source, externalID, at.UTC(),
	).Scan(&eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	var stateLogID int64
	if err := s.DBForTests().QueryRow(
		`INSERT INTO state_log (entity_kind, entity_id, change, event_id, at)
		 VALUES ('task', $1, '{"field":"state","old":"ready","new":"ready"}', $2, $3)
		 RETURNING id`,
		taskID, eventID, at.UTC(),
	).Scan(&stateLogID); err != nil {
		t.Fatalf("insert state_log: %v", err)
	}
	return stateLogID
}

// TestListProjectWorkFacts covers the complete read contract in one
// project-a scenario: ordering by priority then id, a task's parent, a
// task's open blockers, a task's active lease, and its newest state-change
// event.
//
// project-a has exactly 3 tasks: the critical-priority task (claimed by
// agent-x, with a github-sourced state event), a parent's high-priority
// child, and the parent itself (medium priority, blocked by a task in
// project-b — 'blocks' edges are not project-scoped, unlike child_of).
func TestListProjectWorkFacts(t *testing.T) {
	t.Parallel()
	s := openProjectWorkStore(t)
	ctx := t.Context()

	critical := createTask(t, s, taskTestNow, projectWorkTaskInput("project-a", "critical", "feature", "Critical task"))
	child := createTask(t, s, taskTestNow, projectWorkTaskInput("project-a", "high", "feature", "Child task"))
	container := createTask(t, s, taskTestNow, projectWorkTaskInput("project-a", "medium", "feature", "Parent task"))
	blocker := createTask(t, s, taskTestNow, projectWorkTaskInput("project-b", "medium", "feature", "Blocker task"))

	if err := addEdge(t, s, child.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("child_of edge: %v", err)
	}
	if err := addEdge(t, s, blocker.ID, container.ID, "blocks"); err != nil {
		t.Fatalf("blocks edge: %v", err)
	}

	if _, err := s.Claim(ctx, critical.ID, "agent-x", "host:/wt-1", 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Claim's own Transition logs a cli-sourced state_log row. Add a second,
	// github-sourced row after it (so it wins the id DESC tie-break) to
	// prove StateEvent reflects the newest row, not the first.
	if _, _, err := s.RecordEvent(ctx, "github", nextExt(t), "status_check.completed", nil,
		func(tx *sql.Tx, eventID int64) error {
			return LogChange(tx, "task", critical.ID, eventID,
				map[string]string{"field": "state", "old": "in_progress", "new": "in_progress"})
		}); err != nil {
		t.Fatalf("record github state event: %v", err)
	}

	criticalID, parentID, blockerID, agentID := critical.ID, container.ID, blocker.ID, "agent-x"

	facts, err := s.ListProjectWorkFacts(ctx, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 3 {
		t.Fatalf("len = %d, want 3", len(facts))
	}
	if facts[0].Task.ID != criticalID {
		t.Errorf("first = %s, want %s", facts[0].Task.ID, criticalID)
	}
	if facts[1].Parent == nil || facts[1].Parent.ID != parentID {
		t.Errorf("parent = %#v", facts[1].Parent)
	}
	if len(facts[2].OpenBlockers) != 1 || facts[2].OpenBlockers[0].ID != blockerID {
		t.Errorf("blockers = %#v", facts[2].OpenBlockers)
	}
	if facts[0].Lease == nil || facts[0].Lease.ActorID != agentID {
		t.Errorf("lease = %#v", facts[0].Lease)
	}
	// Branch must match what scanTask derives everywhere else (WL-183): the
	// projection used to hand-clear it, so /board and the admin board served
	// "" for the same task /tasks served a real branch name for.
	if want := BranchFor(&facts[0].Task); facts[0].Task.Branch != want {
		t.Errorf("facts[0].Task.Branch = %q, want %q", facts[0].Task.Branch, want)
	}
	if facts[0].Task.Branch == "" {
		t.Error("facts[0].Task.Branch is empty, want a real branch name")
	}
	if facts[0].StateEvent == nil || facts[0].StateEvent.Source != "github" {
		t.Errorf("state event = %#v", facts[0].StateEvent)
	}

	// OpenBlockers is always an initialized empty slice, never nil.
	if facts[0].OpenBlockers == nil {
		t.Errorf("facts[0].OpenBlockers = nil, want an initialized empty slice")
	}
	if len(facts[1].OpenBlockers) != 0 {
		t.Errorf("facts[1].OpenBlockers = %#v, want empty", facts[1].OpenBlockers)
	}
	if !facts[2].Blocked() {
		t.Errorf("facts[2].Blocked() = false, want true")
	}
	if facts[0].Blocked() {
		t.Errorf("facts[0].Blocked() = true, want false")
	}
}

// TestListProjectWorkFactsAllProjects asserts projectID == "" returns tasks
// from every project, not just one.
func TestListProjectWorkFactsAllProjects(t *testing.T) {
	t.Parallel()
	s := openProjectWorkStore(t)
	ctx := t.Context()

	a := createTask(t, s, taskTestNow, projectWorkTaskInput("project-a", "medium", "feature", "A task"))
	b := createTask(t, s, taskTestNow, projectWorkTaskInput("project-b", "medium", "feature", "B task"))

	facts, err := s.ListProjectWorkFacts(ctx, "")
	if err != nil {
		t.Fatalf(`ListProjectWorkFacts(""): %v`, err)
	}
	got := map[string]bool{}
	for _, f := range facts {
		got[f.Task.ID] = true
	}
	if !got[a.ID] || !got[b.ID] {
		t.Fatalf("facts = %v, want both %s and %s present", got, a.ID, b.ID)
	}
}

// TestListProjectWorkFactsNewTaskHasNoStateEvent asserts a task that has
// never transitioned has StateEvent == nil (there is no state_log row for
// it at all, so the lateral join's LIMIT 1 yields no row).
func TestListProjectWorkFactsNewTaskHasNoStateEvent(t *testing.T) {
	t.Parallel()
	s := openProjectWorkStore(t)
	ctx := t.Context()

	task := createTask(t, s, taskTestNow, projectWorkTaskInput("project-a", "medium", "feature", "Brand new"))

	facts, err := s.ListProjectWorkFacts(ctx, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	f := factFor(t, facts, task.ID)
	if f.StateEvent != nil {
		t.Errorf("StateEvent = %#v, want nil for a task that has never transitioned", f.StateEvent)
	}
}

// TestListProjectWorkFactsNewestStateWins asserts the newest state_log row
// wins by at DESC, id DESC. Both rows share the same at (an explicit,
// controlled timestamp bypassing LogChange's time.Now() stamp), isolating
// the id tie-break: the row inserted second always has the higher id.
func TestListProjectWorkFactsNewestStateWins(t *testing.T) {
	t.Parallel()
	s := openProjectWorkStore(t)
	ctx := t.Context()

	task := createTask(t, s, taskTestNow, projectWorkTaskInput("project-a", "medium", "feature", "Tracked"))

	older := recordStateEvent(t, s, "cli", "cli-first", task.ID, taskTestNow)
	newer := recordStateEvent(t, s, "github", "github-second", task.ID, taskTestNow)
	if newer <= older {
		t.Fatalf("test setup: newer state_log id %d must exceed older %d", newer, older)
	}

	facts, err := s.ListProjectWorkFacts(ctx, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	f := factFor(t, facts, task.ID)
	if f.StateEvent == nil || f.StateEvent.Source != "github" {
		t.Errorf("state event = %#v, want the github-sourced row (higher id, same at)", f.StateEvent)
	}
}

// TestListProjectWorkFactsClosedBlockerDisappears asserts a blocker that
// moves to a closed state (taskClosed) no longer counts as an open
// blocker.
func TestListProjectWorkFactsClosedBlockerDisappears(t *testing.T) {
	t.Parallel()
	s := openProjectWorkStore(t)
	ctx := t.Context()

	dependent := createTask(t, s, taskTestNow, projectWorkTaskInput("project-a", "medium", "feature", "Dependent"))
	blocker := createTask(t, s, taskTestNow, projectWorkTaskInput("project-a", "medium", "feature", "Blocker"))
	if err := addEdge(t, s, blocker.ID, dependent.ID, "blocks"); err != nil {
		t.Fatalf("blocks edge: %v", err)
	}

	facts, err := s.ListProjectWorkFacts(ctx, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	dep := factFor(t, facts, dependent.ID)
	if len(dep.OpenBlockers) != 1 || dep.OpenBlockers[0].ID != blocker.ID {
		t.Fatalf("before closing: blockers = %#v, want [%s]", dep.OpenBlockers, blocker.ID)
	}

	walkTo(t, s, blocker.ID, "abandoned")

	facts, err = s.ListProjectWorkFacts(ctx, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	dep = factFor(t, facts, dependent.ID)
	if len(dep.OpenBlockers) != 0 {
		t.Fatalf("after closing blocker: blockers = %#v, want none", dep.OpenBlockers)
	}
	if dep.Blocked() {
		t.Fatalf("Blocked() = true after the only blocker closed, want false")
	}
}

// TestListProjectWorkFactsReleasedLeaseAbsent asserts a released lease does
// not surface as the task's Lease.
func TestListProjectWorkFactsReleasedLeaseAbsent(t *testing.T) {
	t.Parallel()
	s := openProjectWorkStore(t)
	ctx := t.Context()

	task := createTask(t, s, taskTestNow, projectWorkTaskInput("project-a", "medium", "feature", "Leased then released"))
	if _, err := s.Claim(ctx, task.ID, "agent-x", "host:/wt-1", 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.Release(ctx, task.ID, "agent-x"); err != nil {
		t.Fatalf("release: %v", err)
	}

	facts, err := s.ListProjectWorkFacts(ctx, "project-a")
	if err != nil {
		t.Fatal(err)
	}
	f := factFor(t, facts, task.ID)
	if f.Lease != nil {
		t.Errorf("Lease = %#v, want nil after Release", f.Lease)
	}
}

// TestListProjectWorkFactsPlanBlocked: a task held by a plan-to-plan ordering
// edge (025 §9.3) is blocked on the cockpit too, and names the blocking plan's
// open tasks — the ready set, Claim and the board must not disagree about what
// is pickable.
func TestListProjectWorkFactsPlanBlocked(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	blocked := mintReadyPlan(t, s, "plan-b", planTaskBody("", "Plan B"))
	blockers := mintReadyPlan(t, s, "plan-a", planTaskBody("blocks: plan-b\n", "Plan A"))

	facts, err := s.ListProjectWorkFacts(t.Context(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	dep := factFor(t, facts, blocked[0])
	if !dep.Blocked() {
		t.Errorf("Blocked() = false for %s, which Claim refuses (plan A holds it)", blocked[0])
	}
	if len(dep.OpenBlockers) != 1 || dep.OpenBlockers[0].ID != blockers[0] {
		t.Errorf("OpenBlockers = %#v, want plan A's open task %s", dep.OpenBlockers, blockers[0])
	}
	if len(dep.BlockingPlans) != 1 || dep.BlockingPlans[0].Slug != "plan-a" {
		t.Errorf("BlockingPlans = %#v, want plan-a", dep.BlockingPlans)
	}
	if b := factFor(t, facts, blockers[0]); b.Blocked() {
		t.Errorf("Blocked() = true for %s, the blocking plan's own task", blockers[0])
	}

	walkTo(t, s, blockers[0], "merged")

	facts, err = s.ListProjectWorkFacts(t.Context(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if dep = factFor(t, facts, blocked[0]); dep.Blocked() {
		t.Errorf("Blocked() = true after plan A's set closed: %#v / %#v", dep.OpenBlockers, dep.BlockingPlans)
	}
}

// TestListProjectWorkFactsBlockedByDraftPlan: a blocking plan still draft has
// an unminted task set, so it holds with no task to name. The fact carries the
// blocking plan itself, so Blocked() still agrees with Claim.
func TestListProjectWorkFactsBlockedByDraftPlan(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)

	blocked := mintReadyPlan(t, s, "plan-d", planTaskBody("", "Plan D"))
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "plan-c",
		Body: planTaskBody("blocks: plan-d\n", "Plan C"), CreatedBy: "stig",
	})

	facts, err := s.ListProjectWorkFacts(t.Context(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	dep := factFor(t, facts, blocked[0])
	if len(dep.OpenBlockers) != 0 {
		t.Errorf("OpenBlockers = %#v, want none: a draft plan has minted no task", dep.OpenBlockers)
	}
	if len(dep.BlockingPlans) != 1 || dep.BlockingPlans[0].Slug != "plan-c" ||
		dep.BlockingPlans[0].Status != "draft" {
		t.Errorf("BlockingPlans = %#v, want draft plan-c", dep.BlockingPlans)
	}
	if !dep.Blocked() {
		t.Errorf("Blocked() = false for %s, which Claim refuses (draft plan C holds it)", blocked[0])
	}
}
