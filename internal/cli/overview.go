package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// --- drift & overview (spec 007) -------------------------------------------

// Overview calls GET /api/v1/overview: the one-screen roll-up. An empty
// project rolls up every project.
func (c *Client) Overview(ctx context.Context, project string) (model.Overview, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	return doJSON[model.Overview](ctx, c, http.MethodGet, withQuery("/api/v1/overview", q), nil, "overview")
}

// Drift calls GET /api/v1/drift. With acknowledged the response also carries
// the accepted deviations, active and expired.
func (c *Client) Drift(ctx context.Context, acknowledged bool) (model.Drift, []byte, error) {
	q := url.Values{}
	if acknowledged {
		q.Set("acknowledged", "1")
	}
	return doJSON[model.Drift](ctx, c, http.MethodGet, withQuery("/api/v1/drift", q), nil, "drift")
}

// Gaps calls GET /api/v1/gaps: components with no governing doc, and repo
// paths no component claims.
func (c *Client) Gaps(ctx context.Context) (model.GapList, []byte, error) {
	return doJSON[model.GapList](ctx, c, http.MethodGet, "/api/v1/gaps", nil, "gaps")
}

// Frontier calls GET /api/v1/frontier: the ready set in pickup order,
// annotated with the overview-only criticality measures.
func (c *Client) Frontier(ctx context.Context, project string) (model.FrontierList, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	return doJSON[model.FrontierList](ctx, c, http.MethodGet, withQuery("/api/v1/frontier", q), nil, "frontier")
}

// CriticalPath calls GET /api/v1/critical-path: the estimate-free critical
// path over blocks + requires, plus any cycles found on the way.
func (c *Client) CriticalPath(ctx context.Context) (model.CriticalPath, []byte, error) {
	return doJSON[model.CriticalPath](ctx, c, http.MethodGet, "/api/v1/critical-path", nil, "critical path")
}

// RunDerive calls POST /api/v1/derive: run the server-side derivers
// (pr-affects, deploy). The repo-local derivers run from a checkout instead,
// through `lode derive` without --server.
func (c *Client) RunDerive(ctx context.Context) (model.DeriveResponse, []byte, error) {
	return doJSON[model.DeriveResponse](ctx, c, http.MethodPost, "/api/v1/derive", nil, "derive results")
}

// OverviewRender prints `lode overview`: the roll-up counts as a label/value
// block, then any cycles found, then the note that says the counts are
// missing rather than zero when no graph is configured.
func OverviewRender(w io.Writer, o model.Overview) {
	tw := newTabwriter(w)
	fmt.Fprintf(tw, "violations\t%d\nstale intent\t%d\ngaps\t%d\nready frontier\t%d\n",
		o.Violations, o.StaleIntent, o.Gaps, o.FrontierSize)
	if o.CriticalHead != nil {
		fmt.Fprintf(tw, "critical head\t%s\n", o.CriticalHead.ID)
	}
	for _, cyc := range o.Cycles {
		fmt.Fprintf(tw, "CYCLE\t%s\n", strings.Join(cyc, " -> "))
	}
	if !o.GraphEnabled {
		fmt.Fprintf(tw, "note\tgraph not configured; drift/gap counts unavailable\n")
	}
	tw.Flush()
}

// DriftFiltered returns d with both edge lists and the acknowledged list
// narrowed to the edges leaving component; the zero component returns d
// unchanged. The human view and `--json` under the same flag both run this,
// so they cannot disagree about what --component means.
func DriftFiltered(d model.Drift, component string) model.Drift {
	if component == "" {
		return d
	}
	out := model.Drift{}
	for _, e := range d.Violations {
		if e.From == component {
			out.Violations = append(out.Violations, e)
		}
	}
	for _, e := range d.StaleIntent {
		if e.From == component {
			out.StaleIntent = append(out.StaleIntent, e)
		}
	}
	for _, a := range d.Acknowledged {
		if a.From == component {
			out.Acknowledged = append(out.Acknowledged, a)
		}
	}
	return out
}

// CriticalPathFiltered returns cp with Tasks narrowed to task; the zero task
// returns cp unchanged. MaxDepth and Cycles are properties of the whole graph
// and are left as they are — the renderer drops the chain length under a
// filter rather than pretending one row has its own.
func CriticalPathFiltered(cp model.CriticalPath, task string) model.CriticalPath {
	if task == "" {
		return cp
	}
	out := model.CriticalPath{MaxDepth: cp.MaxDepth, Cycles: cp.Cycles}
	for _, t := range cp.Tasks {
		if t.ID == task {
			out.Tasks = append(out.Tasks, t)
		}
	}
	return out
}

// DriftRender prints `lode drift`: the violations, the stale intent, and —
// when the caller asked for them — the accepted deviations. component filters
// both edge sections to the edges leaving that component IRI.
func DriftRender(w io.Writer, d model.Drift, component string, acknowledged bool) {
	d = DriftFiltered(d, component)
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "# violations (observed - declared - acknowledged)")
	for _, e := range d.Violations {
		fmt.Fprintf(tw, "%s\trequires\t%s\n", e.From, e.To)
	}
	fmt.Fprintln(tw, "# stale intent (declared - observed)")
	for _, e := range d.StaleIntent {
		fmt.Fprintf(tw, "%s\trequires\t%s\n", e.From, e.To)
	}
	if acknowledged {
		fmt.Fprintln(tw, "# acknowledged deviations")
		for _, a := range d.Acknowledged {
			state := "active"
			if a.Expired {
				state = "EXPIRED"
			}
			fmt.Fprintf(tw, "%s\trequires\t%s\tby %s\t%s %s\n",
				a.From, a.To, a.SanctionedBy, state, dash(a.ValidUntil))
		}
	}
	tw.Flush()
}

// GapTable prints `lode gaps`: one line per finding. The two kinds carry
// different fields — a component with no governing doc, or a repo path no
// component claims — so the kind leads each row rather than being a column.
func GapTable(w io.Writer, gaps []model.Gap) {
	tw := newTabwriter(w)
	for _, g := range gaps {
		if g.Component != "" {
			fmt.Fprintf(tw, "no governing doc\t%s\n", g.Component)
			continue
		}
		fmt.Fprintf(tw, "unmatched path\t%s\t%s\n", g.Repo, g.Path)
	}
	tw.Flush()
}

// FrontierTable prints `lode task frontier`: the ready set in the order the server
// ranked it, with the criticality measures the overview adds. CRIT marks the
// tasks on the critical path.
func FrontierTable(w io.Writer, tasks []model.FrontierTask) {
	tbl := newTable(
		column{header: "ID"},
		column{header: "PRIO"},
		column{header: "CONCERN"},
		column{header: "FAN-OUT"},
		column{header: "DEPTH"},
		column{header: "CRIT"},
		titleColumn("TITLE"),
	)
	for _, t := range tasks {
		crit := ""
		if t.IsCritical {
			crit = "*"
		}
		tbl.add(t.ID, t.Priority, dash(t.Concern),
			strconv.Itoa(t.FanOut), strconv.Itoa(t.Depth), crit, t.Title)
	}
	tbl.flush(w)
}

// CriticalPathRender prints `lode task critical-path`: the chain length, one
// row per critical task, then any cycles. A task filter narrows the rows to
// that one task, so the chain length — a property of the whole graph, not of
// the row — is left out.
func CriticalPathRender(w io.Writer, cp model.CriticalPath, task string) {
	cp = CriticalPathFiltered(cp, task)
	tw := newTabwriter(w)
	if task == "" {
		fmt.Fprintf(tw, "chain length\t%d\n", cp.MaxDepth)
	}
	for _, t := range cp.Tasks {
		fmt.Fprintf(tw, "%d\t%s\tfan-out %d\n", t.Depth, t.ID, t.FanOut)
	}
	for _, cyc := range cp.Cycles {
		fmt.Fprintf(tw, "CYCLE\t%s\n", strings.Join(cyc, " -> "))
	}
	tw.Flush()
}

// DeriveResultTable prints one row per graph a deriver run touched. SKIPPED
// means the stored hash already matched; EMPTY means the deriver produced no
// triples, which is legitimate for some sources and a broken input for the
// rest — so it is a column, not an inference from BYTES.
func DeriveResultTable(w io.Writer, results []model.DeriveResult) {
	tbl := newTable(
		column{header: "GRAPH", wrap: wrapChars, min: minHolderWidth},
		column{header: "HASH"},
		column{header: "SKIPPED"},
		column{header: "EMPTY"},
		column{header: "BYTES"},
	)
	for _, r := range results {
		tbl.add(r.Graph, dash(r.Hash), strconv.FormatBool(r.Skipped),
			strconv.FormatBool(r.Empty), strconv.Itoa(r.Bytes))
	}
	tbl.flush(w)
}
