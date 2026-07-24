package store

import (
	"database/sql"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestBlockingFanOutChainAndBranch(t *testing.T) {
	s := openTaskStore(t)

	a := createTask(t, s, taskTestNow, defaultTaskInput())
	b := createTask(t, s, taskTestNow, defaultTaskInput())
	c := createTask(t, s, taskTestNow, defaultTaskInput())
	d := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, a.ID, b.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge a blocks b: %v", err)
	}
	if err := addEdge(t, s, b.ID, c.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge b blocks c: %v", err)
	}
	if err := addEdge(t, s, a.ID, d.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge a blocks d: %v", err)
	}

	got, err := s.BlockingFanOut(t.Context())
	if err != nil {
		t.Fatalf("BlockingFanOut: %v", err)
	}
	want := map[string]int{a.ID: 3, b.ID: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BlockingFanOut: got %v, want %v", got, want)
	}
	if _, ok := got[c.ID]; ok {
		t.Fatalf("BlockingFanOut: %s should be absent (fan-out 0), got %v", c.ID, got)
	}
	if _, ok := got[d.ID]; ok {
		t.Fatalf("BlockingFanOut: %s should be absent (fan-out 0), got %v", d.ID, got)
	}
}

// TestBlockingFanOutCycleTerminates pins the query's one real failure mode:
// the schema does not prevent blocks-cycles (AddEdge only cycle-checks
// child_of), and only the CTE's UNION dedup keeps the recursion finite. A
// UNION ALL regression would hang here instead of returning.
func TestBlockingFanOutCycleTerminates(t *testing.T) {
	s := openTaskStore(t)

	a := createTask(t, s, taskTestNow, defaultTaskInput())
	b := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, a.ID, b.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge a blocks b: %v", err)
	}
	if err := addEdge(t, s, b.ID, a.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge b blocks a: %v", err)
	}

	got, err := s.BlockingFanOut(t.Context())
	if err != nil {
		t.Fatalf("BlockingFanOut: %v", err)
	}
	// In a cycle each root reaches every member including itself.
	want := map[string]int{a.ID: 2, b.ID: 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BlockingFanOut cycle: got %v, want %v", got, want)
	}
}

func TestBlockingFanOutDiamond(t *testing.T) {
	s := openTaskStore(t)

	a := createTask(t, s, taskTestNow, defaultTaskInput())
	b := createTask(t, s, taskTestNow, defaultTaskInput())
	c := createTask(t, s, taskTestNow, defaultTaskInput())
	d := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, a.ID, b.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge a blocks b: %v", err)
	}
	if err := addEdge(t, s, a.ID, c.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge a blocks c: %v", err)
	}
	if err := addEdge(t, s, b.ID, d.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge b blocks d: %v", err)
	}
	if err := addEdge(t, s, c.ID, d.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge c blocks d: %v", err)
	}

	got, err := s.BlockingFanOut(t.Context())
	if err != nil {
		t.Fatalf("BlockingFanOut: %v", err)
	}
	// d is reachable from a via both b and c, but must be counted once.
	want := map[string]int{a.ID: 3, b.ID: 1, c.ID: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BlockingFanOut diamond: got %v, want %v", got, want)
	}
	if _, ok := got[d.ID]; ok {
		t.Fatalf("BlockingFanOut diamond: %s should be absent (fan-out 0), got %v", d.ID, got)
	}
}

// rankTestNow is a shared timestamp for rankTasks fixtures that don't care
// about created_at ordering.
var rankTestNow = time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)

func rankTask(id, priority, concern string, createdAt time.Time) Task {
	return Task{ID: id, Priority: priority, Concern: concern, CreatedAt: createdAt}
}

func rankIDs(tasks []Task) []string {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	return ids
}

// TestRankTasksWorkedExample pins the spec-02 worked example exactly: focus
// [security, completeness], five candidates with the given priority/concern/
// fan-out. Default order is critical-first; --strict-focus drops that and
// sorts T3 (critical but off-focus) by its own concern rank instead.
func TestRankTasksWorkedExample(t *testing.T) {
	focus := []string{"security", "completeness"}
	t1 := rankTask("WL-1", "high", "completeness", rankTestNow)
	t2 := rankTask("WL-2", "high", "security", rankTestNow)
	t3 := rankTask("WL-3", "critical", "usability", rankTestNow)
	t4 := rankTask("WL-4", "medium", "security", rankTestNow)
	t5 := rankTask("WL-5", "high", "performance", rankTestNow)

	in := []rankInput{
		{Task: t1, Focus: focus, FanOut: 5},
		{Task: t2, Focus: focus, FanOut: 1},
		{Task: t3, Focus: focus, FanOut: 0},
		{Task: t4, Focus: focus, FanOut: 8},
		{Task: t5, Focus: focus, FanOut: 12},
	}

	gotDefault := rankIDs(rankTasks(in, false))
	wantDefault := []string{"WL-3", "WL-2", "WL-4", "WL-1", "WL-5"}
	if !reflect.DeepEqual(gotDefault, wantDefault) {
		t.Fatalf("rankTasks default: got %v, want %v", gotDefault, wantDefault)
	}

	gotStrict := rankIDs(rankTasks(in, true))
	wantStrict := []string{"WL-2", "WL-4", "WL-1", "WL-3", "WL-5"}
	if !reflect.DeepEqual(gotStrict, wantStrict) {
		t.Fatalf("rankTasks strict-focus: got %v, want %v", gotStrict, wantStrict)
	}
}

// TestRankTasksDeterministic pins spec acceptance 7: identical input always
// yields identical output, run repeatedly.
func TestRankTasksDeterministic(t *testing.T) {
	focus := []string{"security", "completeness"}
	in := []rankInput{
		{Task: rankTask("WL-1", "high", "completeness", rankTestNow), Focus: focus, FanOut: 5},
		{Task: rankTask("WL-2", "high", "security", rankTestNow), Focus: focus, FanOut: 1},
		{Task: rankTask("WL-3", "critical", "usability", rankTestNow), Focus: focus, FanOut: 0},
		{Task: rankTask("WL-4", "medium", "security", rankTestNow), Focus: focus, FanOut: 8},
		{Task: rankTask("WL-5", "high", "performance", rankTestNow), Focus: focus, FanOut: 12},
	}

	first := rankIDs(rankTasks(in, false))
	for i := 0; i < 10; i++ {
		got := rankIDs(rankTasks(in, false))
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("rankTasks run %d: got %v, want %v (same as first run)", i, got, first)
		}
	}
}

// TestRankTasksTiebreakCreatedAt pins the created_at asc tiebreak: two
// otherwise-identical candidates sort by which was created first.
func TestRankTasksTiebreakCreatedAt(t *testing.T) {
	older := rankTestNow
	newer := rankTestNow.Add(time.Hour)
	in := []rankInput{
		{Task: rankTask("WL-2", "high", "", newer), FanOut: 0},
		{Task: rankTask("WL-1", "high", "", older), FanOut: 0},
	}
	got := rankIDs(rankTasks(in, false))
	want := []string{"WL-1", "WL-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rankTasks tiebreak created_at: got %v, want %v", got, want)
	}
}

// TestRankTasksTiebreakNumericID pins the numeric-id tiebreak: WL-9 must
// sort before WL-10 (a plain string compare would get this backwards).
func TestRankTasksTiebreakNumericID(t *testing.T) {
	in := []rankInput{
		{Task: rankTask("WL-10", "high", "", rankTestNow), FanOut: 0},
		{Task: rankTask("WL-9", "high", "", rankTestNow), FanOut: 0},
	}
	got := rankIDs(rankTasks(in, false))
	want := []string{"WL-9", "WL-10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rankTasks tiebreak numeric id: got %v, want %v", got, want)
	}
}

// TestRankTasksFanOutDirection pins the fan_out desc key: two candidates
// equal on every earlier key must sort higher fan-out first. (The worked
// example never isolates this key, so a direction flip would otherwise
// survive the suite.)
func TestRankTasksFanOutDirection(t *testing.T) {
	in := []rankInput{
		{Task: rankTask("WL-1", "high", "", rankTestNow), FanOut: 2},
		{Task: rankTask("WL-2", "high", "", rankTestNow), FanOut: 7},
	}
	got := rankIDs(rankTasks(in, false))
	want := []string{"WL-2", "WL-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rankTasks fan-out direction: got %v, want %v", got, want)
	}
}

func TestNumericTaskIDGeneralPrefix(t *testing.T) {
	if numericTaskID("SW-9") != 9 {
		t.Errorf("numericTaskID(SW-9) = %d, want 9", numericTaskID("SW-9"))
	}
	if numericTaskID("AB12-10") != 10 {
		t.Errorf("numericTaskID(AB12-10) = %d, want 10", numericTaskID("AB12-10"))
	}
	if numericTaskID("bad") != math.MaxInt {
		t.Errorf("numericTaskID(bad) should be MaxInt")
	}
}

// claimNextTestNow is the shared clock for ClaimNext DB fixtures.
var claimNextTestNow = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

// openClaimNextStore opens a task store with a controllable clock, the way
// openLeaseStore does for lease tests (ClaimNext calls through to Claim).
func openClaimNextStore(t *testing.T) *Store {
	t.Helper()
	s := openTaskStore(t)
	now := claimNextTestNow
	s.SetNowFunc(func() time.Time { return now })
	return s
}

// workedExampleFiller creates n draft filler tasks and adds a "blocks" edge
// from task to each, giving task the desired blocking fan-out without
// affecting task's own ready-set membership (a draft filler is never a
// candidate, and blocking a filler does not block the blocker).
func workedExampleFiller(t *testing.T, s *Store, task *Task, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		in := defaultTaskInput()
		in.Draft = true
		filler := createTask(t, s, claimNextTestNow, in)
		if err := addEdge(t, s, task.ID, filler.ID, "blocks"); err != nil {
			t.Fatalf("addEdge filler for %s: %v", task.ID, err)
		}
	}
}

// workedExampleFixture creates the five spec-02 worked-example tasks (in id
// order, so the shared created_at tiebreaks by numeric id exactly as the
// worked example expects) with fan-out wired via real blocks edges to draft
// filler tasks, and sets the project focus to [security, completeness]. It
// returns the tasks in T1..T5 order.
func workedExampleFixture(t *testing.T, s *Store) (t1, t2, t3, t4, t5 *Task) {
	t.Helper()
	ctx := t.Context()
	if err := s.SetProjectFocus(ctx, "horndb", []string{"security", "completeness"}); err != nil {
		t.Fatalf("SetProjectFocus: %v", err)
	}

	mk := func(priority, concern string) *Task {
		in := defaultTaskInput()
		in.Priority = priority
		in.Concern = concern
		return createTask(t, s, claimNextTestNow, in)
	}
	t1 = mk("high", "completeness")
	t2 = mk("high", "security")
	t3 = mk("critical", "usability")
	t4 = mk("medium", "security")
	t5 = mk("high", "performance")

	workedExampleFiller(t, s, t1, 5)
	workedExampleFiller(t, s, t2, 1)
	workedExampleFiller(t, s, t4, 8)
	workedExampleFiller(t, s, t5, 12)
	return t1, t2, t3, t4, t5
}

// TestClaimNextWorkedExampleDefault pins spec acceptance 2: the default
// (critical-first) key over the worked-example fixture claims T3.
func TestClaimNextWorkedExampleDefault(t *testing.T) {
	s := openClaimNextStore(t)
	ctx := t.Context()
	t1, t2, t3, t4, t5 := workedExampleFixture(t, s)
	_, _, _, _ = t1, t2, t4, t5

	res, err := s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "h:/wt/1"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if !res.Claimed || res.Task == nil || res.Task.ID != t3.ID {
		t.Fatalf("ClaimNext default: got %+v, want claim of %s", res, t3.ID)
	}
	if res.Lease == nil || res.Lease.Worktree != "h:/wt/1" {
		t.Fatalf("ClaimNext default lease: got %+v, want worktree h:/wt/1", res.Lease)
	}
	mustState(t, s, t3.ID, "in_progress")
}

// TestClaimNextWorkedExampleStrictFocus pins spec acceptance 2 and 4: under
// --strict-focus (a fresh fixture, since the default test above already
// claimed T3), the top-ranked task is T2.
func TestClaimNextWorkedExampleStrictFocus(t *testing.T) {
	s := openClaimNextStore(t)
	ctx := t.Context()
	_, t2, _, _, _ := workedExampleFixture(t, s)

	res, err := s.ClaimNext(ctx, ClaimNextOpts{StrictFocus: true, ActorID: "stig", Worktree: "h:/wt/1"})
	if err != nil {
		t.Fatalf("ClaimNext strict-focus: %v", err)
	}
	if !res.Claimed || res.Task == nil || res.Task.ID != t2.ID {
		t.Fatalf("ClaimNext strict-focus: got %+v, want claim of %s", res, t2.ID)
	}
	mustState(t, s, t2.ID, "in_progress")
}

// TestClaimNextSoftFocusNeverIdles pins spec acceptance 3: with only
// off-focus ready tasks available, ClaimNext still claims one rather than
// reporting none-ready.
func TestClaimNextSoftFocusNeverIdles(t *testing.T) {
	s := openClaimNextStore(t)
	ctx := t.Context()
	if err := s.SetProjectFocus(ctx, "horndb", []string{"security"}); err != nil {
		t.Fatalf("SetProjectFocus: %v", err)
	}

	in := defaultTaskInput()
	in.Priority = "medium"
	in.Concern = "completeness"
	off := createTask(t, s, claimNextTestNow, in)

	res, err := s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "h:/wt/1"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if !res.Claimed || res.Task == nil || res.Task.ID != off.ID {
		t.Fatalf("ClaimNext soft focus: got %+v, want claim of off-focus task %s", res, off.ID)
	}
}

// TestClaimNextSkipsNeedsDecomposition pins spec acceptance 5: a task
// labelled needs_decomposition is excluded from the ready set even when it
// is the only ready task, so ClaimNext reports no claim rather than an
// error.
func TestClaimNextSkipsNeedsDecomposition(t *testing.T) {
	s := openClaimNextStore(t)
	ctx := t.Context()
	task := createTask(t, s, claimNextTestNow, defaultTaskInput())

	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateTaskFields(tx, claimNextTestNow, task.ID, nil, nil, nil, nil, boolPtr(true))
	}); err != nil {
		t.Fatalf("set needs_decomposition: %v", err)
	}

	res, err := s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "h:/wt/1"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if res.Claimed || res.Task != nil {
		t.Fatalf("ClaimNext with only a needs_decomposition task: got %+v, want Claimed:false Task:nil", res)
	}
}

// TestClaimNextDryRun pins spec acceptance: --dry-run returns the top
// candidate without leasing it or touching task state.
func TestClaimNextDryRun(t *testing.T) {
	s := openClaimNextStore(t)
	ctx := t.Context()
	_, _, t3, _, _ := workedExampleFixture(t, s)

	res, err := s.ClaimNext(ctx, ClaimNextOpts{DryRun: true})
	if err != nil {
		t.Fatalf("ClaimNext dry-run: %v", err)
	}
	if res.Claimed || res.Task == nil || res.Task.ID != t3.ID || res.Lease != nil {
		t.Fatalf("ClaimNext dry-run: got %+v, want Claimed:false Task:%s Lease:nil", res, t3.ID)
	}
	mustState(t, s, t3.ID, "ready")
	if _, err := s.ActiveLease(ctx, t3.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveLease after dry-run: want ErrNotFound, got %v", err)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM leases`).Scan(&total); err != nil {
		t.Fatalf("count leases: %v", err)
	}
	if total != 0 {
		t.Fatalf("leases table after dry-run: got %d rows, want 0", total)
	}
}
