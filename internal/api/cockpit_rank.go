// cockpit_rank.go implements rule det-v1 from the WL-187 research spike
// (docs/research/cockpit-exception-ranking.md §6): order the cockpit's
// secondary concerns by root cause rather than the arbitrary order
// ListProjectWorkFacts happens to emit them in.
//
// A "root" is a blocker that carries no open blockers of its own — the thing
// someone could actually act on — found by following blocked-by edges up
// from each ready-and-blocked task's direct blockers (task or unfinished
// plan). One SecondaryConcern is emitted per root, ordered by (best priority
// among the tasks it transitively holds, its fan-out, the oldest held task's
// blocked-since), all computed in-memory over the ProjectWorkFacts
// assembleProjectCockpit already fetched: no new queries, no storage, no
// background loop (022: assembly stays observed only by
// worklode_cockpit_projection_requests_total).
package api

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// blockerRef is one direct blocker edge read off a ready-and-blocked task's
// facts: either an open blocker task (from OpenBlockers) or an unfinished
// blocking plan with no task yet minted to name (from BlockingPlans, 025
// §9.3). id namespaces plan blockers as "plan:<docID>" — a form no real task
// id takes — so the two kinds never collide as map keys.
type blockerRef struct {
	isPlan bool
	id     string
	title  string
	state  string // task state, or plan status for a plan blocker
	doc    model.DocRef
}

// directBlockers lists f's own direct blockers, task and plan alike, in the
// same fixed order blockerConcerns used to (open blockers, then blocking
// plans) — a stable base order that only matters for the deterministic
// tiebreaks below.
func directBlockers(f store.ProjectWorkFact) []blockerRef {
	out := make([]blockerRef, 0, len(f.OpenBlockers)+len(f.BlockingPlans))
	for _, b := range f.OpenBlockers {
		out = append(out, blockerRef{id: b.ID, title: b.Title, state: b.State})
	}
	for _, p := range f.BlockingPlans {
		out = append(out, blockerRef{isPlan: true, id: "plan:" + strconv.FormatInt(p.ID, 10), title: p.Title, state: p.Status, doc: p})
	}
	return out
}

// concernRoot is one actionable root cause and everything det-v1 scores it
// on: the ready-and-blocked tasks it transitively holds (keyed by id — a
// task can be held by more than one root when its own blocked-by chain
// forks) and, for evidence rendering, the direct parent -> children edges
// among them. fact is the root's own ProjectWorkFact when it is a task this
// project's facts cover; it stays nil for a cross-project blocker det-v1
// cannot see past, or for a plan root (which has no task to look up).
type concernRoot struct {
	ref      blockerRef
	fact     *store.ProjectWorkFact
	held     map[string]store.ProjectWorkFact
	children map[string][]store.ProjectWorkFact
}

// rankSecondaryConcerns builds the cockpit's SecondaryConcerns per det-v1: one
// entry per root cause, ordered highest-signal first. Returns an empty (never
// nil) slice when the project has no ready-and-blocked task (§9 — an empty
// concern set is not an error case to special-case, just the natural result
// of an empty held set).
func rankSecondaryConcerns(facts []store.ProjectWorkFact, now time.Time) []model.SecondaryConcern {
	factsByID := make(map[string]store.ProjectWorkFact, len(facts))
	for _, f := range facts {
		factsByID[f.Task.ID] = f
	}

	var held []store.ProjectWorkFact
	for _, f := range facts {
		if f.Task.State == "ready" && f.Blocked() {
			held = append(held, f)
		}
	}
	if len(held) == 0 {
		return []model.SecondaryConcern{}
	}

	roots := map[string]*concernRoot{}
	rootFor := func(ref blockerRef) *concernRoot {
		r, ok := roots[ref.id]
		if ok {
			return r
		}
		r = &concernRoot{ref: ref, held: map[string]store.ProjectWorkFact{}, children: map[string][]store.ProjectWorkFact{}}
		if f, ok := factsByID[ref.id]; ok {
			cp := f
			r.fact = &cp
		}
		roots[ref.id] = r
		return r
	}

	for _, h := range held {
		for _, direct := range directBlockers(h) {
			// visited is path-based (reset per direct edge) and seeded with
			// h itself, so a blocked-by cycle that loops back to h cannot
			// recurse forever (§9).
			visited := map[string]bool{h.Task.ID: true}
			for _, rootRef := range rootCauses(direct, factsByID, visited) {
				r := rootFor(rootRef)
				r.held[h.Task.ID] = h
				r.children[direct.id] = append(r.children[direct.id], h)
			}
		}
	}

	type stat struct {
		root         *concernRoot
		bestPriority int
		fanOut       int
		oldestAt     time.Time
	}
	stats := make([]stat, 0, len(roots))
	for _, r := range roots {
		best := priorityRank("")
		oldest := now
		for _, h := range r.held {
			if pr := priorityRank(h.Task.Priority); pr < best {
				best = pr
			}
			if at := blockedSince(h, now); at.Before(oldest) {
				oldest = at
			}
		}
		stats = append(stats, stat{root: r, bestPriority: best, fanOut: len(r.held), oldestAt: oldest})
	}

	// det-v1's score: (best priority, fan-out, oldest blocked-since), all
	// wanting the "biggest" value first, with the root's own id as the final
	// total tiebreak so two roots that score identically (§9) still land in
	// a fixed, repeatable order.
	slices.SortFunc(stats, func(a, b stat) int {
		if c := cmp.Compare(a.bestPriority, b.bestPriority); c != 0 {
			return c
		}
		if c := cmp.Compare(b.fanOut, a.fanOut); c != 0 {
			return c
		}
		if c := a.oldestAt.Compare(b.oldestAt); c != 0 {
			return c
		}
		return cmp.Compare(a.root.ref.id, b.root.ref.id)
	})

	out := make([]model.SecondaryConcern, 0, len(stats))
	for _, s := range stats {
		out = append(out, model.SecondaryConcern{
			Kind:  "blocker",
			Title: rootTitle(s.root),
			URL:   rootURL(s.root),
			Evidence: model.EvidenceSummary{
				Category: string(evidenceDeclared),
				Summary:  rootEvidence(s.root, s.fanOut, s.oldestAt, now),
			},
		})
	}
	return out
}

// rootCauses returns the actionable root(s) reached by chasing blocked-by
// edges up from ref: itself, when ref carries no open blockers of its own
// (or is a plan, which this layer never chases past — no doc-blocks-doc
// facts are fetched here), when ref falls outside facts (a cross-project
// blocker det-v1 cannot see past), or when ref is already on the current
// chase path (a blocked-by cycle — named as its own root rather than
// recursed into forever, §9). Otherwise it recurses into ref's own direct
// blockers and returns their deduplicated roots.
func rootCauses(ref blockerRef, factsByID map[string]store.ProjectWorkFact, visited map[string]bool) []blockerRef {
	if ref.isPlan {
		return []blockerRef{ref}
	}
	f, ok := factsByID[ref.id]
	if !ok || !f.Blocked() || visited[ref.id] {
		return []blockerRef{ref}
	}
	visited[ref.id] = true
	defer delete(visited, ref.id)

	seen := map[string]bool{}
	var out []blockerRef
	for _, next := range directBlockers(f) {
		for _, r := range rootCauses(next, factsByID, visited) {
			if !seen[r.id] {
				seen[r.id] = true
				out = append(out, r)
			}
		}
	}
	if len(out) == 0 {
		// f.Blocked() is true, so directBlockers(f) is never empty here —
		// kept as a safety net so a chase can never return zero roots.
		return []blockerRef{ref}
	}
	return out
}

// blockedSince is the moment h became ready-and-blocked: its newest state
// transition. A task that reached ready without ever recording one (created
// straight into a blocked state) has no evidence of when that happened, so
// it contributes no age — now, rather than a fabricated duration.
func blockedSince(h store.ProjectWorkFact, now time.Time) time.Time {
	if h.StateEvent == nil {
		return now
	}
	return h.StateEvent.At
}

// rootTitle and rootURL name the root the way the rest of the cockpit names
// a task or a plan: rootTitle prefers the root's own fetched fact (the
// current title) and falls back to the blocker reference's title for a
// cross-project blocker det-v1 never looked up.
func rootTitle(r *concernRoot) string {
	if r.ref.isPlan {
		return r.ref.title
	}
	if r.fact != nil {
		return r.fact.Task.Title
	}
	return r.ref.title
}

func rootURL(r *concernRoot) string {
	if r.ref.isPlan {
		return "/docs/" + strconv.FormatInt(r.ref.doc.ID, 10)
	}
	return "/tasks/" + r.ref.id
}

// rootEvidence templates det-v1's evidence sentence from the computation
// (§5/§6), e.g. "WL-66 (ready, unclaimed) has held 4 tasks for 7 days — WL-22
// (high) -> WL-23, WL-49 -> WL-50." — never AI-produced, so its category
// stays declared (spec 032 reserves recommended for that).
func rootEvidence(r *concernRoot, fanOut int, oldestAt, now time.Time) string {
	return fmt.Sprintf("%s has held %s for %s — %s.",
		rootHeader(r), pluralCount(fanOut, "task"), humanAge(now.Sub(oldestAt)), rootChain(r))
}

// rootHeader is the evidence sentence's lead clause naming the root: a plan
// root names its status, a task root this project's facts cover names its
// state and whether an active lease claims it, and a task root outside those
// facts (cross-project) falls back to the state named on the blocker
// reference itself.
func rootHeader(r *concernRoot) string {
	switch {
	case r.ref.isPlan:
		return fmt.Sprintf("%s plan %s (%s)", r.ref.title, r.ref.doc.Slug, r.ref.state)
	case r.fact != nil:
		claimed := "unclaimed"
		if r.fact.Lease != nil {
			claimed = "claimed"
		}
		return fmt.Sprintf("%s (%s, %s)", r.ref.id, r.fact.Task.State, claimed)
	default:
		return fmt.Sprintf("%s (%s)", r.ref.id, r.ref.state)
	}
}

// rootChain renders the tree of tasks r transitively holds as an arrow
// diagram, e.g. "WL-22 (high) -> WL-23, WL-49 -> WL-50": the root's direct
// children carry their priority (the "within a root, by the directly-blocked
// task's priority" tiebreak also orders them here); deeper descendants don't
// repeat it. A held task whose own edge into the tree was never recorded —
// only possible for a task blocked but not itself ready, since only
// ready-and-blocked tasks contribute edges — still counts toward fanOut but
// is silently absent from this diagram rather than breaking it.
func rootChain(r *concernRoot) string {
	visited := map[string]bool{r.ref.id: true}
	var parts []string
	for _, c := range sortedChildren(r.children[r.ref.id]) {
		parts = append(parts, chainNode(c, true, r.children, visited))
	}
	return strings.Join(parts, ", ")
}

// sortedChildren orders held tasks by det-v1's within-root tiebreak — best
// priority first — with task id as the final, total tiebreak so equal
// priorities still sort deterministically.
func sortedChildren(items []store.ProjectWorkFact) []store.ProjectWorkFact {
	out := slices.Clone(items)
	slices.SortFunc(out, func(a, b store.ProjectWorkFact) int {
		if c := cmp.Compare(priorityRank(a.Task.Priority), priorityRank(b.Task.Priority)); c != 0 {
			return c
		}
		return cmp.Compare(a.Task.ID, b.Task.ID)
	})
	return out
}

// chainNode renders one node of rootChain's tree: its id, its priority when
// it is a root's direct child, and (recursively) an arrow to whatever it in
// turn holds. visited guards a cycle in the held-graph itself — impossible
// for well-formed data (held tasks are ready, not their own ancestor), kept
// only so a malformed one can never hang the render.
func chainNode(h store.ProjectWorkFact, direct bool, children map[string][]store.ProjectWorkFact, visited map[string]bool) string {
	s := h.Task.ID
	if direct {
		s += fmt.Sprintf(" (%s)", h.Task.Priority)
	}
	if visited[h.Task.ID] {
		return s
	}
	visited[h.Task.ID] = true
	kids := sortedChildren(children[h.Task.ID])
	if len(kids) == 0 {
		return s
	}
	parts := make([]string, 0, len(kids))
	for _, k := range kids {
		parts = append(parts, chainNode(k, false, children, visited))
	}
	return s + " -> " + strings.Join(parts, ", ")
}

// priorityRank maps a task priority to its sort weight, lower (more urgent)
// first — the same fixed order ListProjectWorkFacts' own SQL and
// internal/store's ranking use, duplicated here because it is a two-line
// mapping, not a shared type worth threading a new export for.
func priorityRank(p string) int {
	switch p {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

// pluralCount and humanAge render det-v1's evidence sentence's two counted
// quantities in fixed, simple English — "1 task"/"2 tasks",
// "1 day"/"7 days"/"3 hours" — never a raw duration string.
func pluralCount(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func humanAge(d time.Duration) string {
	if d < time.Hour {
		return "less than an hour"
	}
	if d < 24*time.Hour {
		return pluralCount(int(d.Hours()), "hour")
	}
	return pluralCount(int(d.Hours()/24), "day")
}
