package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// newTabwriter returns a tabwriter configured the same way for every table
// in this package: 2 spaces of padding between columns.
func newTabwriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// localTime formats t in the local zone, or "-" for the zero value.
func localTime(t time.Time) string {
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

// TaskTable prints one row per task: id, priority, kind, state, project,
// assignee (- when unassigned), title.
func TaskTable(w io.Writer, tasks []Task) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tPRIORITY\tKIND\tSTATE\tPROJECT\tASSIGNEE\tTITLE")
	for _, t := range tasks {
		assignee := t.Assignee
		if assignee == "" {
			assignee = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Priority, t.Kind, t.State, t.Project, assignee, t.Title)
	}
	tw.Flush()
}

// TaskDetailRender prints one task with its edges, blocked status, and lease
// holder (if any) — the `lode task show` view.
func TaskDetailRender(w io.Writer, t TaskDetail) {
	fmt.Fprintf(w, "%s  %s\n", t.ID, t.Title)
	fmt.Fprintf(w, "  project:  %s\n", t.Project)
	fmt.Fprintf(w, "  priority: %s\n", t.Priority)
	fmt.Fprintf(w, "  kind:     %s\n", t.Kind)
	fmt.Fprintf(w, "  state:    %s\n", t.State)
	assignee := t.Assignee
	if assignee == "" {
		assignee = "-"
	}
	fmt.Fprintf(w, "  assignee: %s\n", assignee)
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
		fmt.Fprintf(w, "  held by:  %s (expires %s)\n", t.Lease.ActorID, localTime(t.Lease.ExpiresAt))
	}
	if t.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, t.Body)
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
}

// IssueTable prints one row per inbox issue: repo, number, triage state,
// state, title.
func IssueTable(w io.Writer, issues []Issue) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "REPO\t#\tTRIAGE\tSTATE\tTITLE")
	for _, is := range issues {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", is.Repo, is.Number, is.TriageState, is.State, is.Title)
	}
	tw.Flush()
}

// DocTable prints one row per synced document: id, kind, status, version, a
// dirty-provenance marker (025 §16.2), and title.
func DocTable(w io.Writer, docs []Doc) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tKIND\tSTATUS\tV\tDIRTY\tTITLE")
	for _, d := range docs {
		dirty := "-"
		if d.SourceDirty {
			dirty = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", d.ID, d.Kind, d.Status, d.Version, dirty, d.Title)
	}
	tw.Flush()
}

// ProjectTable prints one row per project: id, key, name, repos. Each repo is
// rendered as "owner/name (done_state)".
func ProjectTable(w io.Writer, projects []Project) {
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

// BoardRender prints one section per project, one table per non-empty
// bucket (in progress, in review, blocked, ready), and a trailing recent-
// failures section when present.
func BoardRender(w io.Writer, board BoardResponse) {
	for i, p := range board.Projects {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "== %s (%s) ==\n", p.Name, p.ID)
		boardSection(w, "IN PROGRESS", p.InProgress)
		boardSection(w, "IN REVIEW", p.InReview)
		boardSection(w, "BLOCKED", p.Blocked)
		boardSection(w, "READY", p.Ready)
	}
	if board.RecentFailures != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "== recent failures ==")
		if len(board.RecentFailures) == 0 {
			fmt.Fprintln(w, "(none)")
			return
		}
		tw := newTabwriter(w)
		fmt.Fprintln(tw, "TIME\tCLUSTER\tKIND\tWORKLOAD\tMESSAGE")
		for _, e := range board.RecentFailures {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", localTime(e.OccurredAt), e.Cluster, e.Kind, e.Workload, e.Message)
		}
		tw.Flush()
	}
}

func boardSection(w io.Writer, label string, tasks []BoardTask) {
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
	// child whose parent is in another bucket keeps its own position.
	anchor := func(t BoardTask) (int, int) {
		if p, ok := pos[t.Parent]; ok {
			return p, 1
		}
		return pos[t.ID], 0
	}
	rows := make([]BoardTask, len(tasks))
	copy(rows, tasks)
	sort.SliceStable(rows, func(i, j int) bool {
		ai, ri := anchor(rows[i])
		aj, rj := anchor(rows[j])
		if ai != aj {
			return ai < aj
		}
		return ri < rj
	})

	fmt.Fprintf(w, "\n%s\n", label)
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tPRIORITY\tTITLE\tHOLDER")
	for _, t := range rows {
		holder := "-"
		if t.Holder != nil {
			holder = fmt.Sprintf("%s (until %s)", t.Holder.ActorID, localTime(t.Holder.ExpiresAt))
		}
		id := t.ID
		if _, ok := pos[t.Parent]; ok {
			id = "└ " + id
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", id, t.Priority, t.Title, holder)
	}
	tw.Flush()
}

// TimelineRender prints one line per entry: timestamp, type, and a
// type-specific one-line summary.
func TimelineRender(w io.Writer, entries []map[string]any) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "TIME\tTYPE\tSUMMARY")
	for _, e := range entries {
		at, _ := e["at"].(string)
		typ, _ := e["type"].(string)
		fmt.Fprintf(tw, "%s\t%s\t%s\n", at, typ, timelineSummary(typ, e))
	}
	tw.Flush()
}

// timelineSummary builds a one-line, human-readable summary from a timeline
// entry's type-specific fields. Field shapes mirror internal/api/timeline.go.
func timelineSummary(typ string, e map[string]any) string {
	str := func(k string) string {
		v, _ := e[k].(string)
		return v
	}
	num := func(k string) string {
		v, ok := e[k].(float64)
		if !ok {
			return ""
		}
		return fmt.Sprintf("%.0f", v)
	}
	switch typ {
	case "state":
		if change, ok := e["change"].(map[string]any); ok {
			field, _ := change["field"].(string)
			old, _ := change["old"].(string)
			nw, _ := change["new"].(string)
			if old != "" {
				return fmt.Sprintf("%s: %s -> %s", field, old, nw)
			}
			return fmt.Sprintf("%s: %s", field, nw)
		}
		return ""
	case "pr":
		return fmt.Sprintf("%s#%s %q (%s)", str("repo"), num("number"), str("title"), str("state"))
	case "ci":
		return fmt.Sprintf("%s %s: %s/%s", str("repo"), str("workflow"), str("status"), str("conclusion"))
	case "review":
		return fmt.Sprintf("%s reviewed %s#%s: %s", str("reviewer"), str("repo"), num("number"), str("state"))
	case "artifact":
		return fmt.Sprintf("%s %s %s", str("kind"), str("name"), str("version"))
	case "deployment":
		return fmt.Sprintf("%s/%s: %s", str("environment"), str("target_name"), str("status"))
	case "runtime":
		return fmt.Sprintf("%s on %s: %s", str("kind"), str("workload"), str("message"))
	default:
		return ""
	}
}

// TreeNode is one parent and its direct children, with the parent's derived
// progress — the unit `lode task tree` renders.
type TreeNode struct {
	Parent   Task
	Progress TaskProgress
	Children []Task
}

// TreeRender prints each parent with its progress, then its children indented
// one level. Subtasks — the third tier the depth cap allows — are not
// expanded.
func TreeRender(w io.Writer, nodes []TreeNode) {
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
