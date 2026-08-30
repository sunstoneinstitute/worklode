package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Board calls GET /api/v1/board. An empty project fetches every project.
func (c *Client) Board(ctx context.Context, project string) (model.BoardResponse, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	return doJSON[model.BoardResponse](ctx, c, http.MethodGet, withQuery("/api/v1/board", q), nil, "board")
}

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
		for _, b := range []boardBucket{
			{label: "IN PROGRESS", tasks: p.InProgress, holder: true, kind: true},
			{label: "IN REVIEW", tasks: p.InReview, holder: true},
			{label: "BLOCKED", tasks: p.Blocked, kind: true, depHeader: "BLOCKED BY",
				dep: func(t model.BoardTask) []string { return t.BlockedBy }},
			{label: "READY", tasks: p.Ready, kind: true, depHeader: "BLOCKING",
				dep: func(t model.BoardTask) []string { return t.Blocking }},
		} {
			boardSection(w, b)
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

// boardBucket is one state bucket of the board and the columns it shows:
// holder and kind, plus the dependency column that names the other side of
// the blocker edges (what a blocked task waits on, what a ready task holds
// up). dep is nil for a bucket with no such column.
type boardBucket struct {
	label     string
	tasks     []model.BoardTask
	holder    bool
	kind      bool
	depHeader string
	dep       func(model.BoardTask) []string
}

func boardSection(w io.Writer, b boardBucket) {
	label, tasks, hasHolders, hasKind := b.label, b.tasks, b.holder, b.kind
	if len(tasks) == 0 {
		return
	}
	// The dependency column is dropped when no row in the bucket has one:
	// most ready tasks block nothing, and a column of dashes is noise.
	hasDeps := false
	if b.dep != nil {
		for _, t := range tasks {
			if len(b.dep(t)) > 0 {
				hasDeps = true
				break
			}
		}
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
	if hasDeps {
		cols = append(cols, column{header: b.depHeader, wrap: wrapWords, min: minHolderWidth})
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
		if hasDeps {
			deps := "-"
			if d := b.dep(t); len(d) > 0 {
				deps = strings.Join(d, ", ")
			}
			row = append(row, deps)
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
