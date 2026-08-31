// runboard.go is the run board's pure fact-to-group classifier and assembler
// (032 §8; see docs/plans/2026-08-27-project-cockpit-3-run-board.md). It has
// no store calls and no HTTP: it only turns already-fetched
// store.ProjectWorkFact rows (plus session, PR/CI, and cost maps) into the
// ui.RunBoardView a page renders. The rest of §8 — presets, scoped
// authority, pre-run confirmation, retry/stop, Pause automation — has no
// governed objects yet (see the plan's Coverage section) and is not
// attempted here.
package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// runBoardPage handles GET /projects/{id}/work: the project's run board
// (032 §8), crewPage-shaped. It loads the project header first (so an
// unknown project 404s the same way every other project route does), then
// every fact assembleRunBoard needs — work facts, open sessions, open PRs
// and the CI runs on their head SHAs — and prices only the tasks that
// classify into Running or Needs judgment, since those are the only groups
// a cost line renders for (classify first, price the active set second, so
// a project with no active work costs no TaskCostsForTasks query).
func (s *server) runBoardPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.projectHeader(ctx, r.PathValue("id"))
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	facts, err := s.st.ListProjectWorkFacts(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	sessions, err := s.st.OpenAgentSessionsForProject(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	prs, err := s.st.OpenPRsForProject(ctx, project.ID)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	shas := make([]store.RepoSHA, 0, len(prs))
	for _, pr := range prs {
		shas = append(shas, store.RepoSHA{Repo: pr.Repo, SHA: pr.HeadSHA})
	}
	ci, err := s.st.CIRunsForSHAs(ctx, shas)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	var activeTaskIDs []string
	for _, f := range facts {
		if g := runGroupOf(f); g == runGroupRunning || g == runGroupJudgment {
			activeTaskIDs = append(activeTaskIDs, f.Task.ID)
		}
	}
	costs, err := s.st.TaskCostsForTasks(ctx, activeTaskIDs)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}

	board := assembleRunBoard(runBoardInputs{
		Facts: facts, Sessions: sessions, PRs: prs, CI: ci, Costs: costs, Now: s.st.Now(),
	})
	outcome := runBoardRenderRendered
	if board == nil {
		outcome = runBoardRenderEmpty
	}
	s.observeRunBoardRender(outcome)
	s.renderWeb(w, r, http.StatusOK, "run board page", ui.RunBoard(runBoardView(project, board)))
}

// runBoardView wraps assembleRunBoard's group list with the page shell
// identity (title, canonical URL, project header) — assembleRunBoard has no
// HTTP concerns and knows nothing of either, so this is the one place they
// meet. A nil board (no task classifies into any group) renders as a
// RunBoardView with no groups, which ui.RunBoard shows as the honest
// empty-board line.
func runBoardView(project ui.CockpitProject, board *ui.RunBoardView) ui.RunBoardView {
	v := ui.RunBoardView{
		Page:         ui.PageProps{Title: "worklode: " + project.Name + ": Work"},
		CanonicalURL: "/projects/" + project.ID + "/work",
		Project:      project,
	}
	if board != nil {
		v.Groups = board.Groups
	}
	return v
}

// runGroup is 032 §8's grouping of live work, in the spec's own order.
// runGroupNone marks a task the board excludes (draft: not execution yet).
type runGroup int

const (
	runGroupNone runGroup = iota
	runGroupReady
	runGroupRunning
	runGroupWaiting
	runGroupJudgment // "Needs judgment"
	runGroupFailed
	runGroupCompleted
)

// runGroupOf classifies one task's facts per the pinned table in the plan's
// Global Constraints. Blockedness is f.Blocked() — the claim path's own
// predicate — and "running" requires the active lease ListProjectWorkFacts
// attaches, so an in_progress task whose lease expired lands in Needs
// judgment rather than lying about a worker that is gone.
func runGroupOf(f store.ProjectWorkFact) runGroup {
	switch f.Task.State {
	case "merged", "deployed_dev", "deployed_prod", "released":
		return runGroupCompleted
	case "abandoned":
		return runGroupFailed
	case "in_review":
		return runGroupJudgment
	case "in_progress":
		if f.Lease != nil {
			return runGroupRunning
		}
		return runGroupJudgment
	case "ready":
		if f.Blocked() {
			return runGroupWaiting
		}
		return runGroupReady
	default:
		return runGroupNone
	}
}

// runGroupOrder is the §8 groups in the spec's own pinned order — the order
// the board renders them in, and the only order runGroup's iota values are
// allowed to imply.
var runGroupOrder = []runGroup{
	runGroupReady, runGroupRunning, runGroupWaiting,
	runGroupJudgment, runGroupFailed, runGroupCompleted,
}

// runGroupLabels are the §8 group headings, quoted exactly (Global
// Constraints: "No seventh group, no renames, no reordering").
var runGroupLabels = map[runGroup]string{
	runGroupReady:     "Ready",
	runGroupRunning:   "Running",
	runGroupWaiting:   "Waiting",
	runGroupJudgment:  "Needs judgment",
	runGroupFailed:    "Failed",
	runGroupCompleted: "Completed",
}

// runBoardBound is how many rows a terminal group (Failed, Completed) shows
// before the "and N more" line — the board is a live view, not the
// project's history (Global Constraints).
const runBoardBound = 10

// runBoardInputs is everything the board derivation reads, already fetched.
// Sessions, PRs, CI, and Costs are keyed/joinable by task id and head SHA;
// Now anchors the relative lease-age and event-time strings.
type runBoardInputs struct {
	Facts    []store.ProjectWorkFact
	Sessions []store.ProjectAgentSession
	PRs      []store.PullRequest
	CI       map[store.RepoSHA][]store.CIRun
	Costs    map[string][]store.CostTotal
	Now      time.Time
}

// assembleRunBoard derives the board: groups in the pinned §8 order, each
// omitted when empty; Running and Needs judgment rows carry the full §8
// fact list; Failed and Completed are bounded to the newest runBoardBound by
// state event with an "and N more" count. Returns nil when no task
// classifies into any group — the page then renders the empty-board line.
func assembleRunBoard(in runBoardInputs) *ui.RunBoardView {
	byGroup := make(map[runGroup][]store.ProjectWorkFact)
	for _, f := range in.Facts {
		if g := runGroupOf(f); g != runGroupNone {
			byGroup[g] = append(byGroup[g], f)
		}
	}

	sessions := openSessionByTask(in.Sessions)
	prs := newestPRByTask(in.PRs)

	var groups []ui.RunGroupView
	for _, g := range runGroupOrder {
		facts := byGroup[g]
		if len(facts) == 0 {
			continue
		}

		more := 0
		if g == runGroupFailed || g == runGroupCompleted {
			facts, more = boundToNewest(facts, runBoardBound)
		}

		rows := make([]ui.RunRowView, 0, len(facts))
		for _, f := range facts {
			rows = append(rows, runRow(f, g, sessions, prs, in.CI, in.Costs, in.Now))
		}
		groups = append(groups, ui.RunGroupView{Label: runGroupLabels[g], Rows: rows, More: more})
	}

	if len(groups) == 0 {
		return nil
	}
	return &ui.RunBoardView{Groups: groups}
}

// boundToNewest sorts facts newest-state-event-first (a fact with no state
// event sorts last) and returns the first n plus how many were cut.
func boundToNewest(facts []store.ProjectWorkFact, n int) (bounded []store.ProjectWorkFact, more int) {
	sorted := append([]store.ProjectWorkFact(nil), facts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ei, ej := sorted[i].StateEvent, sorted[j].StateEvent
		switch {
		case ei == nil:
			return false
		case ej == nil:
			return true
		default:
			return ei.At.After(ej.At)
		}
	})
	if len(sorted) <= n {
		return sorted, 0
	}
	return sorted[:n], len(sorted) - n
}

// runRow renders one task's row. Ready and terminal (Failed/Completed) rows
// carry only the identifying fields; Waiting rows add Holds; Running and
// Needs-judgment rows carry the full §8 active-item fact list.
func runRow(f store.ProjectWorkFact, g runGroup, sessions map[string]store.ProjectAgentSession,
	prs map[string]store.PullRequest, ci map[store.RepoSHA][]store.CIRun,
	costs map[string][]store.CostTotal, now time.Time) ui.RunRowView {

	t := f.Task
	row := ui.RunRowView{
		TaskID:  t.ID,
		Title:   t.Title,
		TaskURL: "/tasks/" + t.ID,
		Owner:   t.Assignee,
	}

	if g == runGroupWaiting {
		row.Holds = holdsLabel(f)
	}
	if g != runGroupRunning && g != runGroupJudgment {
		return row
	}

	// An in_progress task with no active lease is here because its worker
	// is gone without done/block, not because of the fact its state event
	// happens to name — say so plainly (Global Constraints).
	if t.State == "in_progress" && f.Lease == nil {
		row.LastEvent = "lease expired"
	} else {
		row.LastEvent = lastEventLabel(f.StateEvent, now)
	}

	if f.Lease != nil {
		row.LeaseAge = ui.FmtAge(f.Lease.AcquiredAt, now)
	}
	if s, ok := sessions[t.ID]; ok {
		row.Delegate = delegateLabel(s)
	}
	row.Costs = costLabels(costs[t.ID])
	if pr, ok := prs[t.ID]; ok {
		row.PRLabel = prLabel(pr)
		row.PRURL = pr.URL
		row.CheckLabel = checkLabel(ci[store.RepoSHA{Repo: pr.Repo, SHA: pr.HeadSHA}])
	}
	return row
}

// holdsLabel names what holds a Waiting row: its open blocker task ids, then
// its blocking plans' doc numbers — a plan carries no other number (025
// §14.3), so the doc id is what "blocking plan numbers" means.
func holdsLabel(f store.ProjectWorkFact) string {
	parts := make([]string, 0, len(f.OpenBlockers)+len(f.BlockingPlans))
	for _, b := range f.OpenBlockers {
		parts = append(parts, b.ID)
	}
	for _, p := range f.BlockingPlans {
		parts = append(parts, fmt.Sprintf("plan %d", p.ID))
	}
	return strings.Join(parts, ", ")
}

// lastEventLabel renders a fact's newest state-change event as "<type>
// <relative age>", or "" when the task has never transitioned.
func lastEventLabel(ev *store.EventFact, now time.Time) string {
	if ev == nil {
		return ""
	}
	return fmt.Sprintf("%s %s", ev.Type, ui.FmtAge(ev.At, now))
}

// delegateLabel renders an open session's holder as "agent vN", or the bare
// agent name when no version was reported.
func delegateLabel(s store.ProjectAgentSession) string {
	if s.AgentVersion == "" {
		return s.Agent
	}
	return fmt.Sprintf("%s v%s", s.Agent, s.AgentVersion)
}

// costLabels renders per-currency cost totals as "<currency> <amount>",
// nil when there are none — an active row with no priced usage shows no
// cost line rather than a fabricated zero.
func costLabels(totals []store.CostTotal) []string {
	if len(totals) == 0 {
		return nil
	}
	out := make([]string, 0, len(totals))
	for _, ct := range totals {
		out = append(out, ct.Currency+" "+model.Money(ct.Cost))
	}
	return out
}

// prLabel renders a paired PR as "#<number> <state>".
func prLabel(pr store.PullRequest) string {
	return fmt.Sprintf("#%d %s", pr.Number, pr.State)
}

// checkLabel renders the newest CI run on a head SHA: its conclusion once
// finished, its status while not, "" when no run is recorded.
func checkLabel(runs []store.CIRun) string {
	if len(runs) == 0 {
		return ""
	}
	newest := runs[0]
	for _, r := range runs[1:] {
		if r.StartedAt.After(newest.StartedAt) {
			newest = r
		}
	}
	if newest.Conclusion != nil && *newest.Conclusion != "" {
		return *newest.Conclusion
	}
	return newest.Status
}

// openSessionByTask indexes a project's open agent sessions by task id,
// keeping the first (most-recently-seen, per OpenAgentSessionsForProject's
// ordering) session for each task — a row shows one delegate, not a list.
func openSessionByTask(sessions []store.ProjectAgentSession) map[string]store.ProjectAgentSession {
	out := make(map[string]store.ProjectAgentSession, len(sessions))
	for _, s := range sessions {
		if _, ok := out[s.TaskID]; !ok {
			out[s.TaskID] = s
		}
	}
	return out
}

// newestPRByTask indexes a project's PRs by task id, keeping the first
// (newest-UpdatedAt-first, per OpenPRsForProject's ordering) PR for each
// task.
func newestPRByTask(prs []store.PullRequest) map[string]store.PullRequest {
	out := make(map[string]store.PullRequest, len(prs))
	for _, pr := range prs {
		if pr.TaskID == nil {
			continue
		}
		if _, ok := out[*pr.TaskID]; !ok {
			out[*pr.TaskID] = pr
		}
	}
	return out
}
