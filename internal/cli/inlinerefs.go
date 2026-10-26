package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// InlineRefsRender prints the "Referenced by" block of a node's detail: one
// line per field of another node that names it, with the lines it does so on
// (WL-REQ-1852). Nothing is printed when no node does.
func InlineRefsRender(w io.Writer, refs model.InlineRefs) {
	if len(refs.In) == 0 {
		return
	}
	fmt.Fprintln(w, "\nReferenced by:")
	for _, r := range refs.In {
		lines := make([]string, len(r.Lines))
		for i, n := range r.Lines {
			lines[i] = strconv.Itoa(n)
		}
		fmt.Fprintf(w, "  %s  %s", r.FromRef, r.Field)
		if len(lines) > 0 {
			fmt.Fprintf(w, "  line %s", strings.Join(lines, ", "))
		}
		fmt.Fprintln(w)
	}
}

// deriveBatch is the page size of a derive selected by filter or --all. Dev
// finished 109 docs inside the 30s client timeout, so a doc costs at most
// about 275ms; 50 of the slowest kind stay near 14s.
const deriveBatch = 50

// classifyBatch is the page size under Classify: each node may wait on one
// Jev request, budgeted at up to 5s, so 5 nodes stay near 25s.
const classifyBatch = 5

// DeriveRefs calls POST /api/v1/projects/{id}/refs/derive (WL-REQ-1860). A
// selection by filter or All is paged deriveBatch nodes per request, so no
// request outlives the client or ingress timeout; the pages merge into one
// report, and raw is that report's JSON.
func (c *Client) DeriveRefs(ctx context.Context, project string, in model.RefDeriveParams) (model.RefDeriveReport, []byte, error) {
	path := "/api/v1/projects/" + url.PathEscape(project) + "/refs/derive"
	if len(in.Refs) > 0 || in.Limit > 0 {
		return doJSON[model.RefDeriveReport](ctx, c, http.MethodPost, path, in, "derive refs")
	}
	in.Limit = deriveBatch
	if in.Classify {
		in.Limit = classifyBatch
	}
	rep := model.RefDeriveReport{Nodes: []model.RefDeriveNode{}}
	for {
		page, _, err := doJSON[model.RefDeriveReport](ctx, c, http.MethodPost, path, in, "derive refs")
		if err != nil {
			return model.RefDeriveReport{}, nil, err
		}
		rep.Nodes = append(rep.Nodes, page.Nodes...)
		if page.Next == "" {
			raw, err := json.Marshal(rep)
			return rep, raw, err
		}
		in.After = page.Next
	}
}

// RefDeriveTable prints one line per node: the references it gained and lost,
// the lines rewritten, what resolved to nothing, the classifications recorded,
// and its error if it failed.
func RefDeriveTable(w io.Writer, rep model.RefDeriveReport) {
	if len(rep.Nodes) == 0 {
		fmt.Fprintln(w, "No nodes selected.")
		return
	}
	for _, n := range rep.Nodes {
		fmt.Fprintf(w, "%s  +%d -%d refs, %d rewrites", n.Ref, len(n.Added), len(n.Removed), n.Rewrites)
		if len(n.Unresolved) > 0 {
			fmt.Fprintf(w, ", unresolved: %s", strings.Join(n.Unresolved, ", "))
		}
		if len(n.Classified) > 0 {
			fmt.Fprintf(w, ", %d classified", len(n.Classified))
		}
		if n.Error != "" {
			fmt.Fprintf(w, "  ERROR: %s", n.Error)
		}
		fmt.Fprintln(w)
	}
}

// RefDeriveDiff prints each node's text changes, then its reference changes
// as + and - lines and its classifications as ~ lines.
func RefDeriveDiff(w io.Writer, rep model.RefDeriveReport) {
	for _, n := range rep.Nodes {
		fmt.Fprintf(w, "== %s\n", n.Ref)
		if n.Error != "" {
			fmt.Fprintf(w, "ERROR: %s\n", n.Error)
		}
		fmt.Fprint(w, n.Diff)
		for _, r := range n.Added {
			fmt.Fprintf(w, "+ %s %s -> %s\n", n.Ref, r.Field, r.ToRef)
		}
		for _, r := range n.Removed {
			fmt.Fprintf(w, "- %s %s -> %s\n", n.Ref, r.Field, r.ToRef)
		}
		for _, u := range n.Unresolved {
			fmt.Fprintf(w, "? %s %s\n", n.Ref, u)
		}
		for _, c := range n.Classified {
			fmt.Fprintf(w, "~ %s %s %s %s %s:%d (%s %.2f)\n", n.Ref, c.Predicate, c.ToKind, c.ToID, c.Field, c.Line, c.Classifier, c.Confidence)
		}
	}
}
