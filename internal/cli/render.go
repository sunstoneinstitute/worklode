package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/sunstoneinstitute/worklode/internal/model"
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
		assignee := t.Assignee
		if assignee == "" {
			assignee = "-"
		}
		tbl.add(t.ID, t.Priority, t.Kind, t.State, t.Project, assignee, t.Title)
	}
	tbl.flush(w)
}

// TaskDetailRender prints one task with its edges, blocked status, and lease
// holder (if any) — the `lode task show` view.
func TaskDetailRender(w io.Writer, t model.TaskDetail) {
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
		name = max(name, utf8.RuneCountInString(sk.Name))
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
		if utf8.RuneCountInString(sk.Name) > name {
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
		n := utf8.RuneCountInString(word)
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

// tableWidth is the column count a wrapped table renders to: the terminal's
// when w is one, else a conventional 80 so piped and captured output stays
// stable.
func tableWidth(w io.Writer) int {
	fd, isTTY := terminalFd(w)
	if !isTTY {
		return defaultTableWidth
	}
	width, _, err := term.GetSize(fd)
	if err != nil || width <= 0 {
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
		tbl := newTable(
			column{header: "TIME"},
			column{header: "CLUSTER"},
			column{header: "KIND"},
			column{header: "WORKLOAD"},
			titleColumn("MESSAGE"),
		)
		for _, e := range board.RecentFailures {
			tbl.add(localTime(e.OccurredAt), e.Cluster, e.Kind, e.Workload, e.Message)
		}
		tbl.flush(w)
	}
}

func boardSection(w io.Writer, label string, tasks []model.BoardTask) {
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
	anchor := func(t model.BoardTask) (int, int) {
		if p, ok := pos[t.Parent]; ok {
			return p, 1
		}
		return pos[t.ID], 0
	}
	rows := make([]model.BoardTask, len(tasks))
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
	tbl := newTable(
		column{header: "ID"},
		column{header: "PRIORITY"},
		titleColumn("TITLE"),
		holderColumn("HOLDER"),
	)
	now := time.Now()
	for _, t := range rows {
		holder := "-"
		if t.Holder != nil {
			holder = fmt.Sprintf("%s (%s)", actorName(t.Holder.ActorID), leaseLeft(t.Holder.ExpiresAt, now))
		}
		id := t.ID
		if _, ok := pos[t.Parent]; ok {
			id = "└ " + id
		}
		tbl.add(id, t.Priority, t.Title, holder)
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

// EventTable prints one row per event, newest last (025 §18): id, received
// time, source, type, external id. The `lode event tail` view.
func EventTable(w io.Writer, events []Event) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tRECEIVED\tSOURCE\tTYPE\tEXTERNAL_ID")
	for _, e := range events {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", e.ID, localTime(e.ReceivedAt), e.Source, e.Type, e.ExternalID)
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
func EventStreamRow(w io.Writer, e Event) {
	fmt.Fprintf(w, eventStreamRowFmt, e.ID, localTime(e.ReceivedAt), e.Source, e.Type, e.ExternalID)
}

// EventSubscriberTable prints one row per subscriber: name, offsets, lag,
// lock holder pid (- when unheld), last updated. The `lode event
// subscribers` view.
func EventSubscriberTable(w io.Writer, subs []EventSubscriberStatus) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "NAME\tREAD\tACKED\tLAG\tHOLDER\tUPDATED")
	for _, s := range subs {
		holder := "-"
		if s.HolderPID != 0 {
			holder = strconv.FormatInt(s.HolderPID, 10)
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\n",
			s.Name, s.LastReadOffset, s.LastAckedOffset, s.Lag, holder, localTime(s.UpdatedAt))
	}
	tw.Flush()
}

// TreeNode is one parent and its direct children, with the parent's derived
// progress — the unit `lode task tree` renders.
type TreeNode struct {
	Parent   model.Task
	Progress model.TaskProgress
	Children []model.Task
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
