package cli

import (
	"fmt"
	"io"
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

// TaskTable prints one row per task: id, priority, kind, state, project, title.
func TaskTable(w io.Writer, tasks []Task) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tPRIORITY\tKIND\tSTATE\tPROJECT\tTITLE")
	for _, t := range tasks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Priority, t.Kind, t.State, t.Project, t.Title)
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
	fmt.Fprintf(w, "\n%s\n", label)
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tPRIORITY\tTITLE\tHOLDER")
	for _, t := range tasks {
		holder := "-"
		if t.Holder != nil {
			holder = fmt.Sprintf("%s (until %s)", t.Holder.ActorID, localTime(t.Holder.ExpiresAt))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.ID, t.Priority, t.Title, holder)
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
