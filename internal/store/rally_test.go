package store

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// rallyInput is defaultTaskInput retitled and retagged as a rally.
func rallyInput() TaskInput {
	in := defaultTaskInput()
	in.Kind = "rally"
	in.Title = "finish the cockpit"
	return in
}

// TestRallyNeverInReadySet: a rally carries no work, so it is never handed
// out by the ranked path and a direct claim by id is refused too — the same
// pair of rules a decision gets.
func TestRallyNeverInReadySet(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	rally := createTask(t, s, taskTestNow, rallyInput())
	sibling := createTask(t, s, taskTestNow, defaultTaskInput())

	got, err := s.readyCandidates(t.Context(), "", "")
	if err != nil {
		t.Fatalf("readyCandidates: %v", err)
	}
	if len(got) != 1 || got[0].ID != sibling.ID {
		t.Fatalf("candidates = %v, want [%s] (rally %s excluded)", taskIDs(got), sibling.ID, rally.ID)
	}

	if _, err := s.Claim(t.Context(), rally.ID, "stig", "host:/wt", 0); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("claim rally: want ErrBadTransition, got %v", err)
	}
	if active, total := countLeases(t, s, rally.ID); active != 0 || total != 0 {
		t.Fatalf("rally %s: %d/%d leases after rejected claim, want 0/0", rally.ID, active, total)
	}
}

// TestRallyCannotTakeChildren covers both doors into child_of: the edge and
// Decompose. A rally's membership is its 'blocks' edges; children would be a
// second, contradicting set.
func TestRallyCannotTakeChildren(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	rally := createTask(t, s, taskTestNow, rallyInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, child.ID, rally.ID, "child_of"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("child_of onto rally: want ErrInvalidInput, got %v", err)
	}
	if _, err := decompose(t, s, rally.ID, []string{"A"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("decompose rally: want ErrInvalidInput, got %v", err)
	}
}

// TestRallyCannotBlock: a rally is only ever the to_task of a 'blocks' edge.
func TestRallyCannotBlock(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	rally := createTask(t, s, taskTestNow, rallyInput())
	other := createTask(t, s, taskTestNow, defaultTaskInput())

	err := addEdge(t, s, rally.ID, other.ID, "blocks")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("rally blocks %s: want ErrInvalidInput, got %v", other.ID, err)
	}
	if !strings.Contains(err.Error(), rally.ID) {
		t.Fatalf("refusal %q does not name the rally %s", err, rally.ID)
	}
	// The other direction is the whole point of the kind.
	if err := addEdge(t, s, other.ID, rally.ID, "blocks"); err != nil {
		t.Fatalf("%s blocks rally: %v", other.ID, err)
	}
}

// TestRallyCarriesNoDecisions covers both paths a decision row can reach a
// task by: posed on it, or re-parented onto it.
func TestRallyCarriesNoDecisions(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()
	rally := createTask(t, s, taskTestNow, rallyInput())
	work := createTask(t, s, taskTestNow, defaultTaskInput())

	posed := model.DecisionInput{Key: "ship-it", Question: "Do we ship?", ResponseType: "yes_no"}
	_, err := s.AddDecision(ctx, rally.ID, "stig", posed)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("pose decision on rally: want ErrInvalidInput, got %v", err)
	}
	if !strings.Contains(err.Error(), rally.ID) {
		t.Fatalf("refusal %q does not name the rally %s", err, rally.ID)
	}

	if _, err := s.AddDecision(ctx, work.ID, "stig", posed); err != nil {
		t.Fatalf("pose decision on work task: %v", err)
	}
	_, err = s.EditDecision(ctx, work.ID, "ship-it", "stig", model.DecisionInput{Task: rally.ID})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("re-parent decision onto rally: want ErrInvalidInput, got %v", err)
	}
	if !strings.Contains(err.Error(), rally.ID) {
		t.Fatalf("refusal %q does not name the rally %s", err, rally.ID)
	}
}

// TestOpenRally reads the project's open rally and counts the read. A closed
// rally is not one, matching what the unique index treats as open.
func TestOpenRally(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()

	if _, err := s.OpenRally(ctx, "horndb"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OpenRally with no rally: want ErrNotFound, got %v", err)
	}
	rally := createTask(t, s, taskTestNow, rallyInput())
	got, err := s.OpenRally(ctx, "horndb")
	if err != nil {
		t.Fatalf("OpenRally: %v", err)
	}
	if got.ID != rally.ID {
		t.Fatalf("OpenRally = %s, want %s", got.ID, rally.ID)
	}
	walkTo(t, s, rally.ID, "abandoned")
	if _, err := s.OpenRally(ctx, "horndb"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OpenRally after abandon: want ErrNotFound, got %v", err)
	}

	for _, outcome := range []string{"ok", "none"} {
		want := float64(1)
		if outcome == "none" {
			want = 2
		}
		if got := testutil.ToFloat64(s.metrics.rallyReads.WithLabelValues(outcome)); got != want {
			t.Errorf("rally_reads{%s} = %v, want %v", outcome, got, want)
		}
	}
	if !strings.Contains(gatheredNames(t, reg), "worklode_rally_reads_total") {
		t.Error("worklode_rally_reads_total is not registered")
	}
}

// TestRallyMembersTransitive: membership is the transitive open-blocker
// closure, so a blocker of a blocker is a member. A closed blocker drops out,
// which is blockerRelation's own rule, and a task blocking nothing in the
// rally is never a member.
func TestRallyMembersTransitive(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	rally := createTask(t, s, taskTestNow, rallyInput())
	near := createTask(t, s, taskTestNow, defaultTaskInput())
	far := createTask(t, s, taskTestNow, defaultTaskInput())
	done := createTask(t, s, taskTestNow, defaultTaskInput())
	outside := createTask(t, s, taskTestNow, defaultTaskInput())

	for _, e := range [][2]string{{near.ID, rally.ID}, {far.ID, near.ID}, {done.ID, rally.ID}} {
		if err := addEdge(t, s, e[0], e[1], "blocks"); err != nil {
			t.Fatalf("addEdge %s blocks %s: %v", e[0], e[1], err)
		}
	}
	walkTo(t, s, done.ID, "abandoned")

	got, err := s.rallyMembers(t.Context())
	if err != nil {
		t.Fatalf("rallyMembers: %v", err)
	}
	want := map[string]bool{near.ID: true, far.ID: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rallyMembers = %v, want %v (outside %s must not be one)", sortedKeys(got), sortedKeys(want), outside.ID)
	}
}

// TestRallyMembersIgnoreClosedRally: membership comes from the open rally
// only. A closed one steers nothing.
func TestRallyMembersIgnoreClosedRally(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	rally := createTask(t, s, taskTestNow, rallyInput())
	member := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, member.ID, rally.ID, "blocks"); err != nil {
		t.Fatalf("addEdge: %v", err)
	}
	walkTo(t, s, rally.ID, "abandoned")

	got, err := s.rallyMembers(t.Context())
	if err != nil {
		t.Fatalf("rallyMembers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("rallyMembers with a closed rally = %v, want empty", sortedKeys(got))
	}
}

// TestRallyMembersCycleTerminates: 'blocks' is not cycle-checked on write, so
// only the CTE's UNION dedup keeps the walk finite. A UNION ALL regression
// hangs here instead of returning.
func TestRallyMembersCycleTerminates(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	rally := createTask(t, s, taskTestNow, rallyInput())
	a := createTask(t, s, taskTestNow, defaultTaskInput())
	b := createTask(t, s, taskTestNow, defaultTaskInput())
	for _, e := range [][2]string{{a.ID, rally.ID}, {b.ID, a.ID}, {a.ID, b.ID}} {
		if err := addEdge(t, s, e[0], e[1], "blocks"); err != nil {
			t.Fatalf("addEdge %s blocks %s: %v", e[0], e[1], err)
		}
	}

	got, err := s.rallyMembers(t.Context())
	if err != nil {
		t.Fatalf("rallyMembers: %v", err)
	}
	want := map[string]bool{a.ID: true, b.ID: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rallyMembers cycle = %v, want %v", sortedKeys(got), sortedKeys(want))
	}
}

// rallyFixture builds the shared ranking fixture: one low-priority task, one
// critical task, and a rally not yet pointing at anything. The caller decides
// whether the low task joins the rally.
func rallyFixture(t *testing.T, s *Store) (rally, low, critical *model.Task) {
	t.Helper()
	rally = createTask(t, s, claimNextTestNow, rallyInput())
	lowIn := defaultTaskInput()
	lowIn.Priority = "low"
	low = createTask(t, s, claimNextTestNow, lowIn)
	critIn := defaultTaskInput()
	critIn.Priority = "critical"
	critical = createTask(t, s, claimNextTestNow, critIn)
	return rally, low, critical
}

// TestRallyMemberOutranksCritical is the point of the feature: a hand-picked
// rally member sorts ahead of a critical task nobody picked, in both focus
// modes. Its companion below runs the same fixture with no rally and gets the
// pre-rally order back, so this test fails if the rally arm is removed and
// that one fails if the arm ever displaces the rest of the key.
func TestRallyMemberOutranksCritical(t *testing.T) {
	t.Parallel()
	s := openClaimNextStore(t)
	rally, low, critical := rallyFixture(t, s)
	if err := addEdge(t, s, low.ID, rally.ID, "blocks"); err != nil {
		t.Fatalf("addEdge: %v", err)
	}

	for _, strict := range []bool{false, true} {
		ranked, _, err := s.rankedFrontier(t.Context(), "", "", strict)
		if err != nil {
			t.Fatalf("rankedFrontier(strict=%v): %v", strict, err)
		}
		want := []string{low.ID, critical.ID}
		if got := taskIDs(ranked); !reflect.DeepEqual(got, want) {
			t.Fatalf("rankedFrontier(strict=%v) = %v, want %v", strict, got, want)
		}
	}
}

// TestNoRallyKeepsCriticalFirst: with no open rally the order is the one the
// pre-rally key gave — critical first.
func TestNoRallyKeepsCriticalFirst(t *testing.T) {
	t.Parallel()
	s := openClaimNextStore(t)
	_, low, critical := rallyFixture(t, s)

	ranked, _, err := s.rankedFrontier(t.Context(), "", "", false)
	if err != nil {
		t.Fatalf("rankedFrontier: %v", err)
	}
	want := []string{critical.ID, low.ID}
	if got := taskIDs(ranked); !reflect.DeepEqual(got, want) {
		t.Fatalf("rankedFrontier with no rally = %v, want %v", got, want)
	}
}

// TestRallyMemberTransitiveRanksIn: the member two hops from the rally is the
// only one of the two that is claimable (its own blocker aside), and it still
// outranks the critical non-member.
func TestRallyMemberTransitiveRanksIn(t *testing.T) {
	t.Parallel()
	s := openClaimNextStore(t)
	rally, near, critical := rallyFixture(t, s)
	far := createTask(t, s, claimNextTestNow, defaultTaskInput())
	for _, e := range [][2]string{{near.ID, rally.ID}, {far.ID, near.ID}} {
		if err := addEdge(t, s, e[0], e[1], "blocks"); err != nil {
			t.Fatalf("addEdge %s blocks %s: %v", e[0], e[1], err)
		}
	}

	ranked, _, err := s.rankedFrontier(t.Context(), "", "", false)
	if err != nil {
		t.Fatalf("rankedFrontier: %v", err)
	}
	// near is blocked by far, so it is not a candidate at all.
	want := []string{far.ID, critical.ID}
	if got := taskIDs(ranked); !reflect.DeepEqual(got, want) {
		t.Fatalf("rankedFrontier = %v, want %v", got, want)
	}
}

// TestClaimNextFallsThroughLeasedRallyMembers pins the soft semantics: the
// rally sorts, it does not filter. With every member leased, an agent gets
// other ready work rather than nothing.
func TestClaimNextFallsThroughLeasedRallyMembers(t *testing.T) {
	t.Parallel()
	s := openClaimNextStore(t)
	ctx := t.Context()
	rally, member, other := rallyFixture(t, s)
	if err := addEdge(t, s, member.ID, rally.ID, "blocks"); err != nil {
		t.Fatalf("addEdge: %v", err)
	}
	if _, err := s.Claim(ctx, member.ID, "stig", "host:/wt-1", 0); err != nil {
		t.Fatalf("claim member: %v", err)
	}

	res, err := s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "host:/wt-2"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if !res.Claimed || res.Task.ID != other.ID {
		t.Fatalf("ClaimNext = %+v, want a claim of %s", res, other.ID)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
