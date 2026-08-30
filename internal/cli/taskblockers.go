// taskblockers.go is the client and view for the transitive blocker tree:
// what is holding a task, what is holding those, down to the tasks nothing
// holds — the ones worth claiming.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// BlockerTree calls GET /api/v1/tasks/{id}/blockers.
func (c *Client) BlockerTree(ctx context.Context, id string) (model.BlockerTree, []byte, error) {
	return doJSON[model.BlockerTree](ctx, c,
		http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id)+"/blockers", nil, "blocker tree")
}

// BlockerForest calls GET /api/v1/blockers?project=<id>: one tree per blocked
// task in scope. An empty project spans every project.
func (c *Client) BlockerForest(ctx context.Context, project string) (model.BlockerForest, []byte, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	return doJSON[model.BlockerForest](ctx, c,
		http.MethodGet, withQuery("/api/v1/blockers", q), nil, "blocker forest")
}

// BlockerForestRender prints one BlockerTreeRender block per tree, blank-line
// separated, or a single line when nothing in scope is blocked.
func BlockerForestRender(w io.Writer, f model.BlockerForest) {
	if len(f.Trees) == 0 {
		fmt.Fprintln(w, "nothing is blocked")
		return
	}
	for i, t := range f.Trees {
		if i > 0 {
			fmt.Fprintln(w)
		}
		BlockerTreeRender(w, t)
	}
}

// BlockerTreeRender prints the blockers as an indented tree, deepest chains
// intact, and the blocking plans under it. The server returns a flat slice
// with each node's Via and Depth, so the nesting is rebuilt here rather than
// asked for per level.
//
// A node the server marked Cycle is printed and not expanded: its blockers
// are the ones already above it on the same branch.
func BlockerTreeRender(w io.Writer, t model.BlockerTree) {
	if len(t.Blockers) == 0 && len(t.BlockingPlans) == 0 {
		fmt.Fprintf(w, "nothing is blocking %s\n", t.Root)
		return
	}
	if len(t.Blockers) > 0 {
		fmt.Fprintf(w, "%s is blocked by:\n", t.Root)
	}

	children := map[string][]model.BlockerNode{}
	for _, n := range t.Blockers {
		children[n.Via] = append(children[n.Via], n)
	}
	var walk func(via string, indent int)
	walk = func(via string, indent int) {
		for _, n := range children[via] {
			mark := ""
			if n.Cycle {
				mark = "  (cycle)"
			}
			fmt.Fprintf(w, "%s%s  %s  (%s)%s\n",
				strings.Repeat("  ", indent+1), n.ID, n.Title, n.State, mark)
			if !n.Cycle {
				walk(n.ID, indent+1)
			}
		}
	}
	walk(t.Root, 0)

	if len(t.BlockingPlans) > 0 {
		fmt.Fprintln(w, "and by plans:")
		for _, p := range t.BlockingPlans {
			fmt.Fprintf(w, "  %s  %s  (%s)\n", p.Slug, p.Title, p.Status)
		}
	}
}
