package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newTabwriter returns a tabwriter configured the same way for every table
// in this package: 2 spaces of padding between columns.
func newTabwriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// dash renders an unset string as the "-" every view uses for "not set".
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// DocNumber renders a document's number, or "-" for a plan, which carries
// none (025 §14.3).
func DocNumber(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

// LocalTime formats t in the local zone, or "-" for the zero value. Every
// timestamp the CLI prints goes through it, including the one-line
// confirmations in internal/cmd, so a lease expiry reads the same wherever it
// appears.
func LocalTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(time.RFC3339)
}

// HumanTokens abbreviates a token count for a table cell: 1.2k, 11.8M. Token
// counts run to eight digits in an agentic session, where the exact figure is
// noise and the magnitude is the point.
func HumanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// Money renders a stored decimal amount for reading, at two decimal places.
// Amounts are stored and summed at micro-unit precision because per-token
// rates need it — a token of cache read costs half a millionth of a dollar —
// but nobody reads a bill in millionths.
//
// A nonzero amount below half a cent renders as "<0.01": rounding real spend
// down to "0.00" would report it as free. Rounding is half-up, so the total
// line and the day lines can disagree by a cent; that is the honest cost of
// showing rounded components and a total that was summed unrounded.
func Money(amount string) string {
	whole, frac, _ := strings.Cut(strings.TrimSpace(amount), ".")
	if whole == "" {
		whole = "0"
	}
	for len(frac) < 3 {
		frac += "0"
	}
	units, err := strconv.ParseInt(whole+frac[:2], 10, 64)
	if err != nil {
		return amount // not a decimal we understand; show it verbatim
	}
	if frac[2] >= '5' {
		units++
	}
	if units == 0 && strings.ContainsAny(whole+frac, "123456789") {
		return "<0.01"
	}
	return fmt.Sprintf("%d.%02d", units/100, units%100)
}

// sessionStatus renders an agent session's lifecycle state for `task show`:
// "active" while running, "ended <ts>" once closed.
func sessionStatus(sess model.AgentSession) string {
	if sess.EndedAt != nil {
		return "ended " + LocalTime(*sess.EndedAt)
	}
	return "active"
}

// sessionTokens renders an agent session's input/output token counts, or
// "-" when neither has been reported yet (a session between claim and its
// first heartbeat).
func sessionTokens(sess model.AgentSession) string {
	if sess.InputTokens == nil && sess.OutputTokens == nil {
		return "-"
	}
	var in, out int64
	if sess.InputTokens != nil {
		in = *sess.InputTokens
	}
	if sess.OutputTokens != nil {
		out = *sess.OutputTokens
	}
	return fmt.Sprintf("%s in / %s out", HumanTokens(in), HumanTokens(out))
}

// sessionCost renders an agent session's recorded spend, or "-" before any
// cost has been reported.
func sessionCost(sess model.AgentSession) string {
	if sess.CostAmount == nil {
		return "-"
	}
	return fmt.Sprintf("%s %s", Money(*sess.CostAmount), sess.CostCurrency)
}

// TaskTable prints one row per task: id, priority, kind, state, project,
// assignee (- when unassigned), title.
func TaskTable(w io.Writer, tasks []model.Task) {
	tbl := newTable(
		column{header: "ID"},
		column{header: "PRIORITY"},
		column{header: "KIND"},
		column{header: "STATE"},
		column{header: "PROJECT"},
		column{header: "ASSIGNEE"},
		titleColumn("TITLE"),
	)
	for _, t := range tasks {
		tbl.add(t.ID, t.Priority, t.Kind, t.State, t.Project, dash(t.Assignee), t.Title)
	}
	tbl.flush(w)
}

// TaskDetailRender prints one task with its edges, blocked status, and lease
// holder — worktree and agent sessions included when leased — the
// `lode task show` view. server is the API base URL, used to absolutize
// /blob/ references in the rendered body (MarkdownWithBase); pass "" when
// none is known.
func TaskDetailRender(w io.Writer, t model.TaskDetail, server string) {
	fmt.Fprintf(w, "%s  %s\n", t.ID, t.Title)
	fmt.Fprintf(w, "  project:  %s\n", t.Project)
	fmt.Fprintf(w, "  priority: %s\n", t.Priority)
	fmt.Fprintf(w, "  kind:     %s\n", t.Kind)
	fmt.Fprintf(w, "  state:    %s\n", t.State)
	fmt.Fprintf(w, "  assignee: %s\n", dash(t.Assignee))
	if t.Hierarchy.Parent != nil {
		fmt.Fprintf(w, "  parent:   %s  %s (%s)\n",
			t.Hierarchy.Parent.ID, t.Hierarchy.Parent.Title, t.Hierarchy.Parent.State)
	}
	if t.Hierarchy.Progress.Total > 0 {
		fmt.Fprintf(w, "  progress: %d/%d children closed\n",
			t.Hierarchy.Progress.Closed, t.Hierarchy.Progress.Total)
	}
	if t.Concern != "" {
		fmt.Fprintf(w, "  concern:  %s\n", t.Concern)
	}
	if t.NeedsDecomposition {
		fmt.Fprintf(w, "  needs decomposition: yes\n")
	}
	if t.Blocked {
		fmt.Fprintf(w, "  blocked:  yes\n")
	}
	if t.Lease != nil {
		fmt.Fprintf(w, "  held by:  %s (expires %s)\n", t.Lease.ActorID, LocalTime(t.Lease.ExpiresAt))
		fmt.Fprintf(w, "  worktree: %s\n", t.Lease.Worktree)
	}
	if len(t.AgentSessions) > 0 {
		fmt.Fprintln(w, "\n  sessions:")
		tw := newTabwriter(w)
		fmt.Fprintln(tw, "    AGENT\tSESSION\tSTARTED\tSTATUS\tTOKENS\tCOST")
		for _, sess := range t.AgentSessions {
			fmt.Fprintf(tw, "    %s\t%s\t%s\t%s\t%s\t%s\n",
				sess.Agent, sess.SessionID, LocalTime(sess.StartedAt),
				sessionStatus(sess), sessionTokens(sess), sessionCost(sess))
		}
		tw.Flush()
	}
	if t.Body != "" {
		fmt.Fprintln(w)
		MarkdownWithBase(w, t.Body, server)
	}
	if len(t.Edges.Out) > 0 || len(t.Edges.In) > 0 {
		fmt.Fprintln(w, "\nedges:")
		for _, e := range t.Edges.Out {
			fmt.Fprintf(w, "  %s %s %s\n", t.ID, e.Type, e.To)
		}
		for _, e := range t.Edges.In {
			fmt.Fprintf(w, "  %s %s %s\n", e.From, e.Type, t.ID)
		}
	}
	if len(t.Blobs) > 0 {
		fmt.Fprintln(w, "\nattachments:")
		tw := newTabwriter(w)
		fmt.Fprintln(tw, "  FILE\tTYPE\tSIZE\tWHERE\tURL")
		for _, b := range t.Blobs {
			where := "attached"
			if b.Embedded {
				where = "in body"
			}
			name := b.Filename
			if name == "" {
				name = b.Hash[:12]
			}
			fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\n",
				name, b.MediaType, b.Size, where, b.URL)
		}
		tw.Flush()
	}
}

// BriefRender prints a task's brief as a readable summary — the `lode task
// brief` and `lode next` view.
func BriefRender(w io.Writer, b model.Brief) {
	fmt.Fprintf(w, "%s: %s\n", b.Task.ID, b.Task.Title)
	fmt.Fprintf(w, "state: %s   priority: %s\n", b.Task.State, b.Task.Priority)
	fmt.Fprintf(w, "branch: %s\n", b.Branch)
	if len(b.Task.Secrets) > 0 {
		fmt.Fprintf(w, "secrets: %s\n", strings.Join(b.Task.Secrets, ", "))
	}
	if b.Lease != nil {
		fmt.Fprintf(w, "lease: %s (expires %s)\n", b.Lease.Worktree, LocalTime(b.Lease.ExpiresAt))
	}
	BlockersRender(w, b.OpenBlockers, b.BlockingPlans)
	if b.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, b.Body)
	}
	// Warnings alone still print the section: a user who misspelled every pin
	// would otherwise see nothing at all, which is exactly the case the
	// warnings exist for.
	if len(b.Skills.Pinned) > 0 || len(b.Skills.Matches) > 0 || len(b.Skills.Warnings) > 0 {
		fmt.Fprintln(w, "\nSkills:")
		for _, p := range b.Skills.Pinned {
			fmt.Fprintf(w, "  pinned  %s — %s (content in brief)\n", p.Name, p.Description)
		}
		for _, m := range b.Skills.Matches {
			fmt.Fprintf(w, "  %.2f    %s — %s\n", m.Score, m.Name, m.Description)
		}
		for _, warn := range b.Skills.Warnings {
			fmt.Fprintf(w, "  warning: %s\n", warn)
		}
	}
}

// BlockersRender prints what is holding a task up, shared by `lode task
// brief`, `lode next` and `lode status`. Each section is omitted when empty.
func BlockersRender(w io.Writer, blockers []model.BriefBlocker, plans []model.DocRef) {
	if len(blockers) > 0 {
		fmt.Fprintln(w, "blocked by:")
		for _, blk := range blockers {
			fmt.Fprintf(w, "  - %s: %s (%s)\n", blk.ID, blk.Title, blk.State)
		}
	}
	if len(plans) > 0 {
		fmt.Fprintln(w, "blocked by plans:")
		for _, p := range plans {
			fmt.Fprintf(w, "  - %s: %s (%s)\n", p.Slug, p.Title, p.Status)
		}
	}
}

// PinnedSkillList prints a task's pinned skills, one per line, or a note when
// there are none — a bare blank line reads as a rendering bug, not "no pins".
func PinnedSkillList(w io.Writer, skills []string) {
	if len(skills) == 0 {
		fmt.Fprintln(w, "(no pinned skills)")
		return
	}
	fmt.Fprintln(w, strings.Join(skills, "\n"))
}

// TaskCostRender prints `lode task cost`: which task and scope, how many agent
// sessions billed usage, then the cost blocks CostRender renders. window is the
// human label for the requested period ("last 7 days", "all time").
func TaskCostRender(w io.Writer, tc model.TaskCost, window string) {
	if tc.IncludesChildren {
		fmt.Fprintf(w, "%s (including child tasks)\n", tc.Task)
	} else {
		fmt.Fprintf(w, "%s\n", tc.Task)
	}
	fmt.Fprintf(w, "sessions with recorded usage: %d\n", tc.Sessions)
	CostRender(w, tc.Cost, window)
}

// DocTable prints one row per document: id, kind, number, slug, title,
// status, version. Number is "-" for a plan, which carries none (025 §14.3).
func DocTable(w io.Writer, docs []model.Doc) {
	tbl := newTable(
		column{header: "ID"},
		column{header: "KIND"},
		column{header: "NUMBER"},
		column{header: "SLUG"},
		titleColumn("TITLE"),
		column{header: "STATUS"},
		column{header: "VERSION"},
	)
	for _, d := range docs {
		tbl.add(strconv.FormatInt(d.ID, 10), d.Kind, DocNumber(d.Number), d.Slug, d.Title,
			d.Status, strconv.Itoa(d.Version))
	}
	tbl.flush(w)
}

// DocPlanningTable prints the `lode doc list --needs-planning` view: one row
// per accepted spec, with the gap ratio 026 §2.1 shows and each undischarged
// anchor annotated with why it is still a gap — "sec-2.4(partial)
// sec-4(unplanned)" (026 §2.1's sample output). gaps is keyed by document id,
// so a document without one renders as no gap rather than misaligning the
// table.
func DocPlanningTable(w io.Writer, docs []model.Doc, gaps []model.DocPlanningGap) {
	byDoc := make(map[int64]model.DocPlanningGap, len(gaps))
	for _, g := range gaps {
		byDoc[g.Doc] = g
	}
	docGapTable(w, "GAPS", "ANCHORS", docs, func(d model.Doc) (int, []string) {
		g := byDoc[d.ID]
		anchors := make([]string, len(g.Gaps))
		for i, s := range g.Gaps {
			anchors[i] = fmt.Sprintf("%s(%s)", s.Anchor, s.Coverage)
		}
		return g.Sections, anchors
	})
}

// DocSupersessionTable prints the `lode doc list --bare-superseded` view: one
// row per superseded document that has a section nothing explains — 025 §6
// rule 2 (026 §2.4) — with the bare ratio and the anchors that need it. gaps
// is keyed by document id, mirroring DocPlanningTable.
func DocSupersessionTable(w io.Writer, docs []model.Doc, gaps []model.DocSupersessionGap) {
	byDoc := make(map[int64]model.DocSupersessionGap, len(gaps))
	for _, g := range gaps {
		byDoc[g.Doc] = g
	}
	docGapTable(w, "BARE", "UNEXPLAINED", docs, func(d model.Doc) (int, []string) {
		g := byDoc[d.ID]
		return g.Sections, g.Unexplained
	})
}

// docGapTable is the shape both gap views share: the document's identity, the
// undischarged-over-total ratio, and the anchors behind it. gapsOf returns the
// document's section total and its outstanding anchors; a document with no gap
// row renders as no gap rather than misaligning the table.
func docGapTable(w io.Writer, ratioHeader, anchorHeader string, docs []model.Doc,
	gapsOf func(model.Doc) (sections int, anchors []string)) {
	tbl := newTable(
		column{header: "ID"},
		column{header: "NUMBER"},
		column{header: "SLUG"},
		titleColumn("TITLE"),
		column{header: ratioHeader},
		column{header: anchorHeader},
	)
	for _, d := range docs {
		sections, anchors := gapsOf(d)
		tbl.add(strconv.FormatInt(d.ID, 10), DocNumber(d.Number), d.Slug, d.Title,
			fmt.Sprintf("%d/%d", len(anchors), sections),
			strings.Join(anchors, " "))
	}
	tbl.flush(w)
}

// DocDetailRender prints one document: its metadata, body, sections, and
// edges both ways — the `lode doc get` view.
func DocDetailRender(w io.Writer, d model.DocDetail) {
	fmt.Fprintf(w, "%d  %s\n", d.ID, d.Title)
	fmt.Fprintf(w, "  project:  %s\n", d.Project)
	fmt.Fprintf(w, "  kind:     %s\n", d.Kind)
	if d.Number != 0 {
		fmt.Fprintf(w, "  number:   %d\n", d.Number)
	}
	fmt.Fprintf(w, "  slug:     %s\n", d.Slug)
	fmt.Fprintf(w, "  status:   %s\n", d.Status)
	fmt.Fprintf(w, "  version:  %d\n", d.Version)
	if d.Issued != "" {
		fmt.Fprintf(w, "  issued:   %s\n", d.Issued)
	}
	fmt.Fprintf(w, "  assignee: %s\n", dash(d.Assignee))
	if d.Revision != nil {
		fmt.Fprintf(w, "  open revision: by %s at %s\n", d.Revision.CreatedBy, LocalTime(d.Revision.CreatedAt))
	}
	if len(d.Sections) > 0 {
		fmt.Fprintln(w, "\n  sections:")
		tw := newTabwriter(w)
		fmt.Fprintln(tw, "    ANCHOR\tNUMBER\tHEADING")
		for _, s := range d.Sections {
			fmt.Fprintf(tw, "    %s\t%s\t%s\n", s.Anchor, s.Number, s.Heading)
		}
		tw.Flush()
	}
	if d.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, d.Body)
	}
	if len(d.Edges) > 0 || len(d.EdgesIn) > 0 {
		fmt.Fprintln(w, "\nedges:")
		for _, e := range d.Edges {
			fmt.Fprintf(w, "  %s %s %s\n", d.Slug, e.Type, docEdgeTarget(e))
		}
		for _, e := range d.EdgesIn {
			fmt.Fprintf(w, "  %s %s %s\n", docEdgeTarget(e), e.Type, d.Slug)
		}
	}
}

// docEdgeTarget renders one edge's far end: the document's slug and optional
// anchor — the id only when a read did not resolve the slug — or the external
// reference an unresolved edge carries.
func docEdgeTarget(e model.DocEdge) string {
	if e.ToDoc == 0 {
		return e.ToExternal
	}
	name := e.ToSlug
	if name == "" {
		name = strconv.FormatInt(e.ToDoc, 10)
	}
	if e.ToAnchor != "" {
		return name + "#" + e.ToAnchor
	}
	return name
}

// IssueTable prints one row per inbox issue: repo, number, triage state,
// state, title.
func IssueTable(w io.Writer, issues []model.Issue) {
	tbl := newTable(
		column{header: "REPO"},
		column{header: "#"},
		column{header: "TRIAGE"},
		column{header: "STATE"},
		titleColumn("TITLE"),
	)
	for _, is := range issues {
		tbl.add(is.Repo, strconv.FormatInt(is.Number, 10), is.TriageState, is.State, is.Title)
	}
	tbl.flush(w)
}

// ProjectTable prints one row per project: id, key, name, repos. Each repo is
// rendered as "owner/name (done_state)".
func ProjectTable(w io.Writer, projects []model.Project) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tKEY\tNAME\tREPOS")
	for _, p := range projects {
		repos := make([]string, 0, len(p.Repos))
		for _, m := range p.Repos {
			repos = append(repos, fmt.Sprintf("%s (%s)", m.Repo, m.DoneState))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, p.Key, p.Name, strings.Join(repos, ", "))
	}
	tw.Flush()
}

// ProjectDetailRender prints `lode project show`: the project's identity,
// focus, and repos, then one cost block per currency. window is the human
// label for the cost period.
func ProjectDetailRender(w io.Writer, d model.ProjectDetail, window string) {
	fmt.Fprintf(w, "%s%s — %s\n", d.ID, KeySuffix(d.Key), d.Name)
	FocusLine(w, d.Focus)
	if len(d.Repos) > 0 {
		fmt.Fprintln(w, "repos:")
		tw := newTabwriter(w)
		for _, r := range d.Repos {
			fmt.Fprintf(tw, "  %s\tdone: %s\n", r.Repo, r.DoneState)
		}
		tw.Flush()
	}
	CostRender(w, d.Cost, window)
}

// FocusLine writes the "focus: a, b" (or "focus: (none)") line for a project's
// ranking focus.
func FocusLine(w io.Writer, focus []string) {
	if len(focus) == 0 {
		fmt.Fprintln(w, "focus: (none)")
		return
	}
	fmt.Fprintf(w, "focus: %s\n", strings.Join(focus, ", "))
}

// KeySuffix renders " (WL)" for a known task-id key, or nothing.
func KeySuffix(key string) string {
	if key == "" {
		return ""
	}
	return " (" + key + ")"
}

// CostRender writes one block per currency: a headline total, a row per day,
// and — when some tokens were billed on a model with no price on file — the
// shortfall that headline therefore omits.
func CostRender(w io.Writer, cost model.CostReport, window string) {
	if len(cost.Totals) == 0 {
		fmt.Fprintf(w, "\ncost, %s: none recorded\n", window)
		return
	}
	// No currency symbol: a vendor need not bill in dollars, and one block per
	// currency already names it in the header. "$12.000000 EUR" is the kind of
	// wrong a symbol table earns you.
	for _, total := range cost.Totals {
		fmt.Fprintf(w, "\ncost, %s: %s %s\n", window, Money(total.CostAmount), total.Currency)
		tw := newTabwriter(w)
		for _, d := range cost.Days {
			if d.Currency != total.Currency {
				continue
			}
			fmt.Fprintf(tw, "  %s\t%s\tin %s\tcache-w %s\tcache-r %s\tout %s\n",
				d.Day, Money(d.CostAmount),
				HumanTokens(d.InputTokens),
				HumanTokens(d.CacheWrite5mTokens+d.CacheWrite1hTokens),
				HumanTokens(d.CacheReadTokens),
				HumanTokens(d.OutputTokens))
		}
		tw.Flush()
		if total.UnpricedTokens > 0 {
			fmt.Fprintf(w, "note: %s tokens from models with no price on file are excluded from the total.\n",
				HumanTokens(total.UnpricedTokens))
		}
	}
}

// ReposDoctorRender prints `lode project doctor`: per mapped repo, whether the
// GitHub App check ran and what it found, when the last delivery arrived, and
// the reconcile hint for a repo that has never delivered. Senders that map to
// no project follow.
func ReposDoctorRender(w io.Writer, resp model.ReposDoctorResponse) {
	for _, r := range resp.Repos {
		// A nil app_installed means the check did not run; the reason is in
		// app_error when there is one, and its absence means no GitHub App is
		// configured at all.
		app := "unchecked (no GitHub App configured)"
		switch {
		case r.AppInstalled == nil && r.AppError != "":
			app = "unchecked (" + r.AppError + ")"
		case r.AppInstalled != nil && *r.AppInstalled:
			app = "installed"
		case r.AppInstalled != nil:
			app = "NOT INSTALLED (" + r.AppError + ")"
		}
		last := "never"
		if r.LastEventAt != nil {
			last = LocalTime(*r.LastEventAt)
		}
		fmt.Fprintf(w, "%s (project %s)\n", r.Repo, r.Project)
		fmt.Fprintf(w, "  app:        %s\n", app)
		fmt.Fprintf(w, "  last event: %s (types: %s)\n", last, strings.Join(r.EventTypes, ", "))
		fmt.Fprintf(w, "  unapplied:  %d\n", r.UnappliedEvents)
		if r.Stale {
			fmt.Fprintf(w, "  STALE: no delivery since mapping — run `lode reconcile --repo %s`\n", r.Repo)
		}
	}
	for _, u := range resp.UnmappedSenders {
		fmt.Fprintf(w, "unmapped sender: %s (%d events, last %s)\n",
			u.Repo, u.Events, LocalTime(u.LastEventAt))
	}
}

// ReconcileRender prints `lode reconcile`: the run id, what the replay pass
// repaired (or would repair, on a dry run), and what the poll pass did.
func ReconcileRender(w io.Writer, resp model.ReconcileResponse) {
	verb := "repaired"
	if resp.DryRun {
		verb = "would repair"
	}
	fmt.Fprintf(w, "run %s\n", resp.RunID)
	if resp.Replay != nil {
		fmt.Fprintf(w, "replay: %s %d of %d candidate event(s), %d still unmapped\n",
			verb, resp.Replay.Replayed, resp.Replay.Candidates, resp.Replay.StillUnmapped)
		for _, e := range resp.Replay.Errors {
			fmt.Fprintf(w, "  error: %s\n", e)
		}
		if n := resp.Replay.ErrorsOmitted; n > 0 {
			fmt.Fprintf(w, "  ... and %d more error(s), not reported\n", n)
		}
		if resp.Replay.Truncated {
			fmt.Fprintf(w, "  batch full: more candidates remain, run again\n")
		}
	}
	switch {
	case resp.PollSkipped != "":
		fmt.Fprintf(w, "poll: skipped (%s)\n", resp.PollSkipped)
	case resp.Poll != nil:
		fmt.Fprintf(w, "poll: %v\n", resp.Poll)
	}
}

// CrewTable renders a project's Crew roster: name, roles comma-joined, and a
// "lead" marker on the row of the project's one lead, if any.
func CrewTable(w io.Writer, members []model.CrewMember) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ACTOR\tNAME\tROLES\tLEAD")
	for _, m := range members {
		lead := ""
		if m.Lead {
			lead = "lead"
		}
		// display_name is nullable; the web page falls back to the actor id
		// (internal/ui/crew.templ), so the CLI table matches it rather than
		// printing a blank NAME cell.
		name := m.DisplayName
		if name == "" {
			name = m.Actor
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Actor, name, strings.Join(m.Roles, ", "), lead)
	}
	tw.Flush()
}

// Skill table layout. A skill description is a paragraph of trigger prose, not
// a table cell — several run past 400 characters — so the description column
// wraps to the terminal instead of overflowing it. The name column is capped
// so one long skill name cannot squeeze the prose into a ribbon.
const (
	maxSkillNameWidth = 32
	minSkillDescWidth = 24
)

// SkillTable prints one row per skill: name, then the description wrapped to
// the terminal width with continuation lines aligned under the first.
func SkillTable(w io.Writer, skills []model.Skill) {
	skillTable(w, skills, tableWidth(w))
}

func skillTable(w io.Writer, skills []model.Skill, width int) {
	name := len("NAME")
	for _, sk := range skills {
		name = max(name, displayWidth(sk.Name))
	}
	name = min(name, maxSkillNameWidth)
	desc := max(width-name-2, minSkillDescWidth)

	fmt.Fprintf(w, "%-*s  %s\n", name, "NAME", "DESCRIPTION")
	for _, sk := range skills {
		lines := wrapSkillDesc(sk.Description, desc)
		if len(lines) == 0 {
			lines = []string{""}
		}
		// A name past the cap would push its own description right and break
		// the column; give it the row to itself instead.
		if displayWidth(sk.Name) > name {
			fmt.Fprintln(w, sk.Name)
		} else {
			fmt.Fprintf(w, "%-*s  %s\n", name, sk.Name, lines[0])
			lines = lines[1:]
		}
		for _, l := range lines {
			fmt.Fprintf(w, "%-*s  %s\n", name, "", l)
		}
	}
}

// wrapSkillDesc breaks s into lines of at most width columns, splitting on
// whitespace only. A word longer than width gets a line of its own rather than
// being cut: skill prose carries URLs and backticked identifiers that are
// worse mangled than overlong. The table's own wrapper (wrapWordsAt) hard-
// splits instead, which is why the skill table does not share it.
func wrapSkillDesc(s string, width int) []string {
	var lines []string
	var cur, curWidth = "", 0
	for _, word := range strings.Fields(s) {
		n := displayWidth(word)
		switch {
		case cur == "":
			cur, curWidth = word, n
		case curWidth+1+n <= width:
			cur += " " + word
			curWidth += 1 + n
		default:
			lines = append(lines, cur)
			cur, curWidth = word, n
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// tableWidth is the column count SkillTable renders to: the terminal's when w
// is one, else a conventional 80 so piped and captured output stays stable.
// It does not go unlimited off-TTY the way table.flush does, because a skill
// description has no natural width to fall back to — see the off-TTY width
// policy on termWidth (markdown.go).
func tableWidth(w io.Writer) int {
	width, isTTY := termWidth(w)
	if !isTTY || width <= 0 {
		return defaultTableWidth
	}
	return max(width, minTableWidth)
}

const (
	defaultTableWidth = 80
	minTableWidth     = 40
)

// BoardRender prints one section per project, one table per non-empty
// bucket (in progress, in review, blocked, ready), and a trailing recent-
// failures section when present.
func BoardRender(w io.Writer, board model.BoardResponse) {
	for i, p := range board.Projects {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "== %s (%s) ==\n", p.Name, p.ID)
		// Blocked and ready tasks are never claimed, so a HOLDER column would
		// be all dashes; show the task kind there instead. In progress tasks
		// get both: HOLDER to see who's on it, KIND to see what it is.
		for _, b := range []struct {
			label  string
			tasks  []model.BoardTask
			holder bool
			kind   bool
		}{
			{"IN PROGRESS", p.InProgress, true, true},
			{"IN REVIEW", p.InReview, true, false},
			{"BLOCKED", p.Blocked, false, true},
			{"READY", p.Ready, false, true},
		} {
			boardSection(w, b.label, b.tasks, b.holder, b.kind)
		}
	}
	if board.RecentFailures != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "== recent failures ==")
		if len(board.RecentFailures) == 0 {
			fmt.Fprintln(w, "(none)")
			return
		}
		tbl := newTable(
			column{header: "TIME"},
			column{header: "CLUSTER"},
			column{header: "KIND"},
			column{header: "WORKLOAD"},
			titleColumn("MESSAGE"),
		)
		for _, e := range board.RecentFailures {
			tbl.add(LocalTime(e.OccurredAt), e.Cluster, e.Kind, e.Workload, e.Message)
		}
		tbl.flush(w)
	}
}

func boardSection(w io.Writer, label string, tasks []model.BoardTask, hasHolders, hasKind bool) {
	if len(tasks) == 0 {
		return
	}
	pos := make(map[string]int, len(tasks))
	for i, t := range tasks {
		pos[t.ID] = i
	}
	// A child sorts at its parent's position in the incoming slice (rank 1),
	// anything else at its own (rank 0), so grouping keeps a parent and its
	// children adjacent without disturbing the server's priority ordering. A
	// child whose parent is in another bucket keeps its own position. The key
	// is computed once per row rather than per comparison.
	type ranked struct {
		task   model.BoardTask
		anchor int
		rank   int
	}
	rows := make([]ranked, len(tasks))
	for i, t := range tasks {
		r := ranked{task: t, anchor: pos[t.ID]}
		if p, ok := pos[t.Parent]; ok {
			r.anchor, r.rank = p, 1
		}
		rows[i] = r
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].anchor != rows[j].anchor {
			return rows[i].anchor < rows[j].anchor
		}
		return rows[i].rank < rows[j].rank
	})

	fmt.Fprintf(w, "\n%s\n", label)
	cols := []column{
		{header: "ID"},
		{header: "PRIORITY"},
		titleColumn("TITLE"),
	}
	if hasHolders {
		cols = append(cols, holderColumn("HOLDER"))
	}
	if hasKind {
		cols = append(cols, holderColumn("KIND"))
	}
	tbl := newTable(cols...)
	now := time.Now()
	for _, r := range rows {
		t := r.task
		id := t.ID
		if r.rank == 1 {
			id = "└ " + id
		}
		row := []string{id, t.Priority, t.Title}
		if hasHolders {
			holder := "-"
			if t.Holder != nil {
				holder = fmt.Sprintf("%s (%s)", actorName(t.Holder.ActorID), leaseLeft(t.Holder.ExpiresAt, now))
			}
			row = append(row, holder)
		}
		if hasKind {
			row = append(row, t.Kind)
		}
		tbl.add(row...)
	}
	tbl.flush(w)
}

// actorName shortens an actor id for a table cell. Ids are Keycloak
// preferred_username values, so in a realm that logs users in by email they
// arrive as "stig@sunstoneinstitute.ai" and the domain is the same for
// everyone on the board — noise in a column that has to fit beside a title.
// Anything that is not email-shaped is left alone.
func actorName(actorID string) string {
	if local, _, ok := strings.Cut(actorID, "@"); ok && local != "" {
		return local
	}
	return actorID
}

// leaseLeft renders how much of a lease is left, e.g. "1h14m left". The board
// is read to decide whether a task is still being worked, which an absolute
// expiry timestamp answers only after the reader does the subtraction. A lease
// with seconds to go reads "<1m left" rather than "0m left", and one already
// past its expiry says so instead of counting backwards.
func leaseLeft(expiresAt, now time.Time) string {
	d := expiresAt.Sub(now)
	switch {
	case d <= 0:
		return "expired"
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm left", int(d/time.Hour), int(d%time.Hour/time.Minute))
	case d >= time.Minute:
		return fmt.Sprintf("%dm left", int(d/time.Minute))
	default:
		return "<1m left"
	}
}

// TimelineRender prints one line per entry: timestamp, type, and a
// type-specific one-line summary.
func TimelineRender(w io.Writer, entries []model.TimelineEntry) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "TIME\tTYPE\tSUMMARY")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", LocalTime(e.At), e.Type, timelineSummary(e))
	}
	tw.Flush()
}

// timelineSummary builds a one-line, human-readable summary from a timeline
// entry's type-specific fields — see model.TimelineEntry for which fields
// each type populates.
func timelineSummary(e model.TimelineEntry) string {
	switch e.Type {
	case "state":
		// The change payload is a stored state_log row, not a shape this API
		// declares: "new" is a string for a field update and a list for the
		// secrets ones, so it is read key by key rather than decoded into a
		// struct (ADR 036 §3). Field "edge" (store.AddEdge/RemoveEdge) uses
		// op/type/from/to instead of old/new.
		var change map[string]any
		if json.Unmarshal(e.Change, &change) != nil {
			return ""
		}
		field, _ := change["field"].(string)
		if field == "edge" {
			op, _ := change["op"].(string)
			typ, _ := change["type"].(string)
			from, _ := change["from"].(string)
			to, _ := change["to"].(string)
			verb := "added"
			if op == "remove" {
				verb = "removed"
			}
			return fmt.Sprintf("edge %s: %s %s %s", verb, from, typ, to)
		}
		old, _ := change["old"].(string)
		nw, _ := change["new"].(string)
		if old != "" {
			return fmt.Sprintf("%s: %s -> %s", field, old, nw)
		}
		return fmt.Sprintf("%s: %s", field, nw)
	case "pr":
		return fmt.Sprintf("%s#%d %q (%s)", e.Repo, e.Number, e.Title, e.State)
	case "ci":
		conclusion := ""
		if e.Conclusion != nil {
			conclusion = *e.Conclusion
		}
		return fmt.Sprintf("%s %s: %s/%s", e.Repo, e.Workflow, e.Status, conclusion)
	case "review":
		return fmt.Sprintf("%s reviewed %s#%d: %s", e.Reviewer, e.Repo, e.Number, e.State)
	case "artifact":
		return fmt.Sprintf("%s %s %s", e.Kind, e.Name, e.Version)
	case "deployment":
		return fmt.Sprintf("%s/%s: %s", e.Environment, e.TargetName, e.Status)
	case "runtime":
		return fmt.Sprintf("%s on %s: %s", e.Kind, e.Workload, e.Message)
	case "landed":
		return fmt.Sprintf("%s %s on main", e.Repo, e.SHA)
	case "deployed":
		return fmt.Sprintf("%s confirmed in %s", e.Repo, e.Environment)
	case "released":
		return fmt.Sprintf("%s %s", e.Repo, e.Tag)
	default:
		return ""
	}
}

// EventTable prints one row per event, newest last (025 §18): id, received
// time, source, type, external id. The `lode event tail` view.
func EventTable(w io.Writer, events []model.Event) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tRECEIVED\tSOURCE\tTYPE\tEXTERNAL_ID")
	for _, e := range events {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", e.ID, LocalTime(e.ReceivedAt), e.Source, e.Type, e.ExternalID)
	}
	tw.Flush()
}

// eventStreamRowFmt lays out one `lode event tail --follow` row. Fixed
// widths rather than a tabwriter: a stream has no complete row set to
// measure, and re-measuring per row would make the columns jitter as events
// arrive.
const eventStreamRowFmt = "%-8v  %-20v  %-10v  %-28v  %v\n"

// EventStreamHeader prints the follow view's column header, once.
func EventStreamHeader(w io.Writer) {
	fmt.Fprintf(w, eventStreamRowFmt, "ID", "RECEIVED", "SOURCE", "TYPE", "EXTERNAL_ID")
}

// EventStreamRow prints one streamed event in EventTable's column order.
func EventStreamRow(w io.Writer, e model.Event) {
	fmt.Fprintf(w, eventStreamRowFmt, e.ID, LocalTime(e.ReceivedAt), e.Source, e.Type, e.ExternalID)
}

// EventSubscriberTable prints one row per subscriber: name, offsets, lag,
// lock holder pid (- when unheld), last updated. The `lode event
// subscribers` view.
func EventSubscriberTable(w io.Writer, subs []model.EventSubscriberStatus) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "NAME\tREAD\tACKED\tLAG\tHOLDER\tUPDATED")
	for _, s := range subs {
		holder := "-"
		if s.HolderPID != 0 {
			holder = strconv.FormatInt(s.HolderPID, 10)
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\n",
			s.Name, s.LastReadOffset, s.LastAckedOffset, s.Lag, holder, LocalTime(s.UpdatedAt))
	}
	tw.Flush()
}

// TreeRender prints each parent with its progress, then its children indented
// one level. Subtasks — the third tier the depth cap allows — are not
// expanded. The nodes come from the server as model.TaskTreeNode: the tree is
// one response, not a fetch per parent.
func TreeRender(w io.Writer, nodes []model.TaskTreeNode) {
	if len(nodes) == 0 {
		fmt.Fprintln(w, "no tasks with children")
		return
	}
	for i, n := range nodes {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  %s  [%s]  %d/%d closed\n",
			n.Parent.ID, n.Parent.Title, n.Parent.State, n.Progress.Closed, n.Progress.Total)
		for _, c := range n.Children {
			fmt.Fprintf(w, "  %s  %s  (%s)\n", c.ID, c.Title, c.State)
		}
		if len(n.Children) == 0 {
			fmt.Fprintln(w, "  (no children)")
		}
	}
}
