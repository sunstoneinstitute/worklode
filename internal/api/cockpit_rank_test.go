package api

import (
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// fact builds a minimal ready-and-blocked (or root) store.ProjectWorkFact for
// det-v1 tests: id/priority/state plus, when age >= 0, a StateEvent that
// blockedSince reads as "became ready-and-blocked age ago". A negative age
// means "no recorded transition" (StateEvent stays nil).
func fact(id, priority, state string, age time.Duration, blockers ...store.TaskRef) store.ProjectWorkFact {
	f := store.ProjectWorkFact{
		Task:         model.Task{ID: id, Title: id + " title", Priority: priority, State: state},
		OpenBlockers: blockers,
	}
	if age >= 0 {
		f.StateEvent = &store.EventFact{At: fixedNow.Add(-age)}
	}
	return f
}

// ref is shorthand for a store.TaskRef naming a blocker by id (title/state
// are not read by rootCauses beyond the terminal fallback case, so they are
// filled in generically).
func ref(id, state string) store.TaskRef {
	return store.TaskRef{ID: id, Title: id + " title", State: state}
}

var fixedNow = time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

// TestRankSecondaryConcernsRootCauseChain asserts a blocker-of-a-blocker
// resolves to the actionable root, not the intermediate: WL-66 (unblocked)
// blocks WL-22, which is itself ready-and-blocked and blocks WL-23. The only
// concern is WL-66 — WL-22 never appears as a named root, because a blocker
// that is itself blocked is not a root (det-v1 §6).
func TestRankSecondaryConcernsRootCauseChain(t *testing.T) {
	t.Parallel()
	facts := []store.ProjectWorkFact{
		fact("WL-66", "high", "ready", -1),
		fact("WL-22", "high", "ready", 2*24*time.Hour, ref("WL-66", "ready")),
		fact("WL-23", "medium", "ready", time.Hour, ref("WL-22", "ready")),
	}
	got := rankSecondaryConcerns(facts, fixedNow)
	if len(got) != 1 {
		t.Fatalf("concerns = %#v, want exactly one root", got)
	}
	if got[0].URL != "/tasks/WL-66" {
		t.Errorf("concern URL = %q, want /tasks/WL-66 (the actionable root, not WL-22)", got[0].URL)
	}
}

// TestRankSecondaryConcernsOrdersByPriorityThenFanOut asserts det-v1's first
// two score components: a root holding a higher-priority task always
// outranks one that only holds lower-priority tasks, and among equal best
// priority, the root with the larger fan-out wins.
func TestRankSecondaryConcernsOrdersByPriorityThenFanOut(t *testing.T) {
	t.Parallel()
	facts := []store.ProjectWorkFact{
		// Root WL-66 transitively holds four tasks, best priority high.
		fact("WL-66", "low", "ready", -1),
		fact("WL-22", "high", "ready", 7*24*time.Hour, ref("WL-66", "ready")),
		fact("WL-23", "medium", "ready", 3*24*time.Hour, ref("WL-22", "ready")),
		fact("WL-49", "medium", "ready", 2*24*time.Hour, ref("WL-22", "ready")),
		fact("WL-50", "low", "ready", 24*time.Hour, ref("WL-49", "ready")),
		// Root WL-100 also holds a high-priority task, but only one — smaller
		// fan-out, so it must rank below WL-66 despite matching priority.
		fact("WL-100", "low", "ready", -1),
		fact("WL-101", "high", "ready", time.Hour, ref("WL-100", "ready")),
		// Root WL-248 holds only a low-priority task — ranks last.
		fact("WL-248", "low", "ready", -1),
		fact("WL-192", "low", "ready", time.Hour, ref("WL-248", "ready")),
	}
	got := rankSecondaryConcerns(facts, fixedNow)
	wantOrder := []string{"/tasks/WL-66", "/tasks/WL-100", "/tasks/WL-248"}
	if len(got) != len(wantOrder) {
		t.Fatalf("concerns = %#v, want %d roots", got, len(wantOrder))
	}
	for i, want := range wantOrder {
		if got[i].URL != want {
			t.Errorf("concerns[%d].URL = %q, want %q (order %v)", i, got[i].URL, want, urlsOf(got))
		}
	}
}

// TestRankSecondaryConcernsOldestBreaksFanOutTie asserts det-v1's third score
// component: among roots tied on best priority and fan-out, the one holding
// the longer-blocked task ranks first.
func TestRankSecondaryConcernsOldestBreaksFanOutTie(t *testing.T) {
	t.Parallel()
	facts := []store.ProjectWorkFact{
		fact("WL-1", "high", "ready", -1),
		fact("WL-2", "high", "ready", 24*time.Hour, ref("WL-1", "ready")), // held 1 day
		fact("WL-3", "high", "ready", -1),
		fact("WL-4", "high", "ready", 5*24*time.Hour, ref("WL-3", "ready")), // held 5 days
	}
	got := rankSecondaryConcerns(facts, fixedNow)
	if len(got) != 2 || got[0].URL != "/tasks/WL-3" || got[1].URL != "/tasks/WL-1" {
		t.Fatalf("concerns = %v, want WL-3 (older) before WL-1", urlsOf(got))
	}
}

// TestRankSecondaryConcernsTotalOrderOnFullTie asserts the final tiebreak —
// the root's own id — so two roots identical on every det-v1 score component
// still land in one fixed, repeatable order rather than depending on map
// iteration (§9: "what we did not establish" flags untested ties, so this
// pins the fallback rather than leaving it to chance).
func TestRankSecondaryConcernsTotalOrderOnFullTie(t *testing.T) {
	t.Parallel()
	facts := []store.ProjectWorkFact{
		fact("WL-9", "high", "ready", -1),
		fact("WL-10", "high", "ready", 24*time.Hour, ref("WL-9", "ready")),
		fact("WL-2", "high", "ready", -1),
		fact("WL-3", "high", "ready", 24*time.Hour, ref("WL-2", "ready")),
	}
	for i := 0; i < 5; i++ {
		got := rankSecondaryConcerns(facts, fixedNow)
		if len(got) != 2 || got[0].URL != "/tasks/WL-2" || got[1].URL != "/tasks/WL-9" {
			t.Fatalf("run %d: concerns = %v, want [WL-2, WL-9] (id order) every time", i, urlsOf(got))
		}
	}
}

// TestRankSecondaryConcernsCycleTerminates asserts a blocked-by cycle (WL-1
// blocks WL-2, WL-2 blocks WL-1 — a malformed graph the API should never let
// through, but det-v1 must not hang or panic on it) resolves deterministically
// rather than recursing forever (§9).
func TestRankSecondaryConcernsCycleTerminates(t *testing.T) {
	t.Parallel()
	facts := []store.ProjectWorkFact{
		fact("WL-1", "high", "ready", time.Hour, ref("WL-2", "ready")),
		fact("WL-2", "high", "ready", time.Hour, ref("WL-1", "ready")),
	}
	done := make(chan []model.SecondaryConcern, 1)
	go func() { done <- rankSecondaryConcerns(facts, fixedNow) }()
	select {
	case got := <-done:
		if len(got) == 0 {
			t.Fatalf("concerns = %#v, want at least one root named from the cycle", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rankSecondaryConcerns did not terminate on a blocked-by cycle")
	}
}

// TestRankSecondaryConcernsEmpty asserts a project with no ready-and-blocked
// task returns an empty, non-nil slice (§9's "empty concern sets").
func TestRankSecondaryConcernsEmpty(t *testing.T) {
	t.Parallel()
	got := rankSecondaryConcerns([]store.ProjectWorkFact{
		fact("WL-1", "high", "ready", -1),
		fact("WL-2", "high", "in_progress", -1),
	}, fixedNow)
	if got == nil || len(got) != 0 {
		t.Fatalf("concerns = %#v, want an empty non-nil slice", got)
	}
}

// TestRankSecondaryConcernsEvidenceLine asserts the evidence sentence is
// templated from the computed root and chain exactly as det-v1 specifies
// (WL-280 brief / research note §5), for the note's own worked example:
// WL-66 -> WL-22 -> {WL-23, WL-49 -> WL-50}.
func TestRankSecondaryConcernsEvidenceLine(t *testing.T) {
	t.Parallel()
	facts := []store.ProjectWorkFact{
		fact("WL-66", "low", "ready", -1),
		fact("WL-22", "high", "ready", 7*24*time.Hour, ref("WL-66", "ready")),
		fact("WL-23", "medium", "ready", 3*24*time.Hour, ref("WL-22", "ready")),
		fact("WL-49", "medium", "ready", 2*24*time.Hour, ref("WL-22", "ready")),
		fact("WL-50", "low", "ready", 24*time.Hour, ref("WL-49", "ready")),
	}
	got := rankSecondaryConcerns(facts, fixedNow)
	if len(got) != 1 {
		t.Fatalf("concerns = %#v, want exactly one root", got)
	}
	c := got[0]
	if c.Kind != "blocker" {
		t.Errorf("kind = %q, want blocker", c.Kind)
	}
	if c.Title != "WL-66 title" {
		t.Errorf("title = %q, want the root's own title", c.Title)
	}
	if c.Evidence.Category != "declared" {
		t.Errorf("evidence.category = %q, want declared (032 reserves recommended for AI-produced content)", c.Evidence.Category)
	}
	const want = "WL-66 (ready, unclaimed) has held 4 tasks for 7 days — WL-22 (high) -> WL-23, WL-49 -> WL-50."
	if c.Evidence.Summary != want {
		t.Errorf("evidence.summary =\n%q\nwant\n%q", c.Evidence.Summary, want)
	}
}

// TestRankSecondaryConcernsClaimedRoot asserts a root with an active lease
// reports "claimed" rather than "unclaimed" in its evidence header.
func TestRankSecondaryConcernsClaimedRoot(t *testing.T) {
	t.Parallel()
	root := fact("WL-5", "high", "ready", -1)
	root.Lease = &store.Lease{ActorID: "agent-1"}
	facts := []store.ProjectWorkFact{
		root,
		fact("WL-6", "high", "ready", time.Hour, ref("WL-5", "ready")),
	}
	got := rankSecondaryConcerns(facts, fixedNow)
	if len(got) != 1 {
		t.Fatalf("concerns = %#v, want exactly one root", got)
	}
	const want = "WL-5 (ready, claimed) has held 1 task for 1 hour — WL-6 (high)."
	if got[0].Evidence.Summary != want {
		t.Errorf("evidence.summary = %q, want %q", got[0].Evidence.Summary, want)
	}
}

// TestRankSecondaryConcernsCrossProjectBlocker asserts a blocker outside this
// project's fetched facts (attachOpenBlockers is not project-scoped for
// 'blocks' edges) still names a root — from the blocker reference itself,
// since det-v1 has no further facts to chase it with — rather than being
// dropped or panicking on the missing lookup.
func TestRankSecondaryConcernsCrossProjectBlocker(t *testing.T) {
	t.Parallel()
	facts := []store.ProjectWorkFact{
		fact("OTHER-1", "medium", "ready", time.Hour, ref("EXT-9", "in_progress")),
	}
	got := rankSecondaryConcerns(facts, fixedNow)
	if len(got) != 1 || got[0].URL != "/tasks/EXT-9" {
		t.Fatalf("concerns = %v, want one root naming the cross-project blocker EXT-9", urlsOf(got))
	}
}

// TestRankSecondaryConcernsBlockingPlan asserts an unfinished plan ordered
// before a task's own plan (025 §9.3) becomes a plan root, distinct from the
// task-root case, when it has minted no task to chase further.
func TestRankSecondaryConcernsBlockingPlan(t *testing.T) {
	t.Parallel()
	f := fact("WL-2", "medium", "ready", time.Hour)
	f.BlockingPlans = []model.DocRef{{ID: 7, Slug: "plan-a", Title: "Plan A", Status: "draft"}}
	got := rankSecondaryConcerns([]store.ProjectWorkFact{f}, fixedNow)
	if len(got) != 1 || got[0].Title != "Plan A" || got[0].URL != "/docs/7" {
		t.Fatalf("concerns = %#v, want one root naming Plan A at /docs/7", got)
	}
	const want = "Plan A plan plan-a (draft) has held 1 task for 1 hour — WL-2 (medium)."
	if got[0].Evidence.Summary != want {
		t.Errorf("evidence.summary = %q, want %q", got[0].Evidence.Summary, want)
	}
}

func urlsOf(cs []model.SecondaryConcern) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.URL
	}
	return out
}
