package store

import (
	"database/sql"
	"errors"
	"maps"
	"reflect"
	"regexp"
	"slices"
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

// TestActiveRally reads the project's active rally and counts the read. A
// closed one is not active, matching what the unique index treats as active.
func TestActiveRally(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()

	if _, err := s.ActiveRally(ctx, "horndb"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveRally with no rally: want ErrNotFound, got %v", err)
	}
	rally := createTask(t, s, taskTestNow, rallyInput())
	got, err := s.ActiveRally(ctx, "horndb")
	if err != nil {
		t.Fatalf("ActiveRally: %v", err)
	}
	if got.ID != rally.ID {
		t.Fatalf("ActiveRally = %s, want %s", got.ID, rally.ID)
	}
	walkTo(t, s, rally.ID, "abandoned")
	if _, err := s.ActiveRally(ctx, "horndb"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveRally after abandon: want ErrNotFound, got %v", err)
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

// TestRallyMembersIgnoreClosedRally: membership comes from the active rally
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

// rallyRankingPair builds the two candidates every rally ranking test sorts:
// one low-priority task and one critical one.
func rallyRankingPair(t *testing.T, s *Store) (low, critical *model.Task) {
	t.Helper()
	lowIn := defaultTaskInput()
	lowIn.Priority = "low"
	low = createTask(t, s, claimNextTestNow, lowIn)
	critIn := defaultTaskInput()
	critIn.Priority = "critical"
	critical = createTask(t, s, claimNextTestNow, critIn)
	return low, critical
}

// rallyFixture is rallyRankingPair plus an active rally not yet pointing at
// anything. The caller decides what joins it.
func rallyFixture(t *testing.T, s *Store) (rally, low, critical *model.Task) {
	t.Helper()
	rally = createTask(t, s, claimNextTestNow, rallyInput())
	low, critical = rallyRankingPair(t, s)
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

// TestNoRallyKeepsCriticalFirst: with no active rally the order is the one
// the pre-rally key gave — critical first.
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
	return slices.Sorted(maps.Keys(m))
}

// draftRallyInput is rallyInput in draft state.
func draftRallyInput() TaskInput {
	in := rallyInput()
	in.Draft = true
	return in
}

// TestDraftRalliesAreUnlimited: the one-per-project index covers active
// rallies only, so a project may hold any number of drafts.
func TestDraftRalliesAreUnlimited(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	first := createTask(t, s, taskTestNow, draftRallyInput())
	second := createTask(t, s, taskTestNow, draftRallyInput())
	if first.ID == second.ID {
		t.Fatalf("both draft rallies got id %s", first.ID)
	}
	if _, err := s.ActiveRally(t.Context(), "horndb"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveRally with only drafts: want ErrNotFound, got %v", err)
	}
}

// TestPublishingASecondRallyIsRefused: publishing is draft -> ready, and that
// is what activates a rally. The second one to try finds the slot taken. The
// refusal comes from the index, so it reads as ErrInvalidInput rather than as
// a raw unique violation.
func TestPublishingASecondRallyIsRefused(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	first := createTask(t, s, taskTestNow, draftRallyInput())
	second := createTask(t, s, taskTestNow, draftRallyInput())

	if err := transition(t, s, taskTestNow, first.ID, "draft", "ready"); err != nil {
		t.Fatalf("publish the first rally: %v", err)
	}
	err := transition(t, s, taskTestNow, second.ID, "draft", "ready")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("publish a second rally: want ErrInvalidInput, got %v", err)
	}
	if !strings.Contains(err.Error(), second.ID) {
		t.Fatalf("refusal %q does not name the rally %s", err, second.ID)
	}
	// Closing the active one frees the slot.
	walkTo(t, s, first.ID, "abandoned")
	if err := transition(t, s, taskTestNow, second.ID, "draft", "ready"); err != nil {
		t.Fatalf("publish after the active rally closed: %v", err)
	}
}

// TestDraftRallyContributesNoMembers: a draft is inert, so its blockers are
// not members. Publishing it makes them members, which is what shows the
// emptiness came from the draft state and not from the fixture.
func TestDraftRallyContributesNoMembers(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	rally := createTask(t, s, taskTestNow, draftRallyInput())
	member := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, member.ID, rally.ID, "blocks"); err != nil {
		t.Fatalf("addEdge: %v", err)
	}

	got, err := s.rallyMembers(t.Context())
	if err != nil {
		t.Fatalf("rallyMembers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("rallyMembers with a draft rally = %v, want empty", sortedKeys(got))
	}

	if err := transition(t, s, taskTestNow, rally.ID, "draft", "ready"); err != nil {
		t.Fatalf("publish the rally: %v", err)
	}
	got, err = s.rallyMembers(t.Context())
	if err != nil {
		t.Fatalf("rallyMembers after publish: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]bool{member.ID: true}) {
		t.Fatalf("rallyMembers after publish = %v, want [%s]", sortedKeys(got), member.ID)
	}
}

// TestDraftRallySteersNothing: `lode work next` behaves exactly as if the
// draft were not there. Same fixture and same assertion as
// TestNoRallyKeepsCriticalFirst, with a draft rally pointing at the low task.
func TestDraftRallySteersNothing(t *testing.T) {
	t.Parallel()
	s := openClaimNextStore(t)
	low, critical := rallyRankingPair(t, s)
	draft := createTask(t, s, claimNextTestNow, draftRallyInput())
	if err := addEdge(t, s, low.ID, draft.ID, "blocks"); err != nil {
		t.Fatalf("addEdge: %v", err)
	}

	ranked, _, err := s.rankedFrontier(t.Context(), "", "", false)
	if err != nil {
		t.Fatalf("rankedFrontier: %v", err)
	}
	want := []string{critical.ID, low.ID}
	if got := taskIDs(ranked); !reflect.DeepEqual(got, want) {
		t.Fatalf("rankedFrontier with a draft rally = %v, want %v (the pre-rally order)", got, want)
	}
}

// TestRallyIndexPredicateMatchesGo pins rallyInactiveStates to the state list
// migration 0069's index actually carries, read back from Postgres. The Go
// condition and the index have to agree: the index is what makes the active
// rally singular, and the Go condition is what every read of it asks.
func TestRallyIndexPredicateMatchesGo(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)

	var def string
	if err := s.db.QueryRow(
		`SELECT indexdef FROM pg_indexes WHERE indexname = 'tasks_one_active_rally'`).Scan(&def); err != nil {
		t.Fatalf("read tasks_one_active_rally: %v", err)
	}
	// The predicate reads "... AND (state <> ALL (ARRAY['draft'::text, ...]))";
	// take the states from inside that ARRAY, not from the whole definition,
	// which also carries kind = 'rally'.
	inside := regexp.MustCompile(`ARRAY\[([^\]]*)\]`).FindStringSubmatch(def)
	if inside == nil {
		t.Fatalf("no state ARRAY in the index definition:\n%s", def)
	}
	got := quotedLiterals(inside[1])
	want := quotedLiterals(rallyInactiveStates)
	if !slices.Equal(got, want) {
		t.Errorf("tasks_one_active_rally excludes %v, rallyInactiveStates excludes %v\n"+
			"the index and the Go condition disagree about which states are inactive; "+
			"a migration must move with the Go\nindex: %s", got, want, def)
	}
}

// quotedLiterals returns the sorted single-quoted words in s.
func quotedLiterals(s string) []string {
	out := []string{}
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return out
}

// retag drives UpdateTaskFields' kind field through RecordEvent, the one door
// that can change a task's kind after creation.
func retag(t *testing.T, s *Store, id, kind string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.update", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UpdateTaskFields(tx, taskTestNow, id, nil, nil, nil, nil, nil, nil, nil, &kind, nil)
		})
	return err
}

// TestRetagRefusesStateTheKindForbids is the guard on the one door every
// other rule leaves open: the kind is checked when each of these states is
// created, and never again. Each case builds a state that the target kind's
// own creation paths refuse, then retags into it. rally and decision share
// the first two; the last two are rally's alone, since a decision task is
// meant to carry decision rows and may block other work.
func TestRetagRefusesStateTheKindForbids(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		kind  string
		setUp func(t *testing.T, s *Store, id string)
		want  string
	}{
		{"rally with children", "rally", func(t *testing.T, s *Store, id string) {
			if _, err := decompose(t, s, id, []string{"A", "B"}); err != nil {
				t.Fatalf("decompose: %v", err)
			}
		}, "has children"},
		{"decision with children", "decision", func(t *testing.T, s *Store, id string) {
			if _, err := decompose(t, s, id, []string{"A", "B"}); err != nil {
				t.Fatalf("decompose: %v", err)
			}
		}, "has children"},
		{"rally under lease", "rally", func(t *testing.T, s *Store, id string) {
			if _, err := s.Claim(t.Context(), id, "stig", "host:/wt", 0); err != nil {
				t.Fatalf("claim: %v", err)
			}
		}, "is held by"},
		{"decision under lease", "decision", func(t *testing.T, s *Store, id string) {
			if _, err := s.Claim(t.Context(), id, "stig", "host:/wt", 0); err != nil {
				t.Fatalf("claim: %v", err)
			}
		}, "is held by"},
		{"rally carrying a decision row", "rally", func(t *testing.T, s *Store, id string) {
			if _, err := s.AddDecision(t.Context(), id, "stig", model.DecisionInput{
				Key: "ship-it", Question: "Do we ship?", ResponseType: "yes_no"}); err != nil {
				t.Fatalf("pose decision: %v", err)
			}
		}, "decision row"},
		{"rally blocking another task", "rally", func(t *testing.T, s *Store, id string) {
			other := createTask(t, s, taskTestNow, defaultTaskInput())
			if err := addEdge(t, s, id, other.ID, "blocks"); err != nil {
				t.Fatalf("addEdge: %v", err)
			}
		}, "blocks 1 task"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openTaskStore(t)
			task := createTask(t, s, taskTestNow, defaultTaskInput())
			tc.setUp(t, s, task.ID)

			err := retag(t, s, task.ID, tc.kind)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("retag to %s: want ErrInvalidInput, got %v", tc.kind, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q does not mention %q", err, tc.want)
			}
			got, err := s.GetTask(t.Context(), task.ID)
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if got.Kind != "feature" {
				t.Fatalf("kind is %q after the refused retag, want feature", got.Kind)
			}
		})
	}
}

// TestRetagAllowsACleanTask: the guard refuses states, not retagging. A task
// holding none of the forbidden state becomes a rally, and an unrelated kind
// is never checked at all.
func TestRetagAllowsACleanTask(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	clean := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := retag(t, s, clean.ID, "rally"); err != nil {
		t.Fatalf("retag a clean task to rally: %v", err)
	}
	// Children are fine on a chore, so the same state that refused a rally
	// retag above passes here.
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := decompose(t, s, parent.ID, []string{"A", "B"}); err != nil {
		t.Fatalf("decompose: %v", err)
	}
	if err := retag(t, s, parent.ID, "chore"); err != nil {
		t.Fatalf("retag a container to chore: %v", err)
	}
}

// TestRetagToRallyRefusedWhenOneIsActive: the project's one-active-rally rule
// is a partial unique index, so this door is closed by the write, not by a
// prior read. It must still report as a refusal rather than a raw violation.
func TestRetagToRallyRefusedWhenOneIsActive(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	createTask(t, s, taskTestNow, rallyInput())
	other := createTask(t, s, taskTestNow, defaultTaskInput())

	err := retag(t, s, other.ID, "rally")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("retag to rally with one active: want ErrInvalidInput, got %v", err)
	}
	if !strings.Contains(err.Error(), other.ID) {
		t.Fatalf("refusal %q does not name the task %s", err, other.ID)
	}
}
