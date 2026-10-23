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

// DeriveRefs calls POST /api/v1/projects/{id}/refs/derive (WL-REQ-1860).
func (c *Client) DeriveRefs(ctx context.Context, project string, in model.RefDeriveParams) (model.RefDeriveReport, []byte, error) {
	return doJSON[model.RefDeriveReport](ctx, c, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(project)+"/refs/derive", in, "derive refs")
}

// RefDeriveTable prints one line per node: the references it gained and lost,
// the lines rewritten, what resolved to nothing, and its error if it failed.
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
		if n.Error != "" {
			fmt.Fprintf(w, "  ERROR: %s", n.Error)
		}
		fmt.Fprintln(w)
	}
}

// RefDeriveDiff prints each node's text changes, then its reference changes
// as + and - lines.
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
	}
}
