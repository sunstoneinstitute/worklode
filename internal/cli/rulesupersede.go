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

// SupersedeRules calls POST /api/v1/projects/{id}/rules/supersede,
// applying a refactor map (S24).
func (c *Client) SupersedeRules(ctx context.Context, project string, in model.SupersedeInput) (model.SupersedeResult, []byte, error) {
	return doJSON[model.SupersedeResult](ctx, c, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(project)+"/rules/supersede", in, "supersede")
}

// SupersedeRender prints one line per resolved map entry, "WL-RULE-12 ->
// WL-RULE-40, WL-RULE-41" (or "WL-RULE-12 ->" for a withdraw-only entry), then the
// counts, prefixed "dry run:" when the result came from --dry-run.
func SupersedeRender(w io.Writer, res model.SupersedeResult) {
	for _, e := range res.Entries {
		if len(e.New) == 0 {
			fmt.Fprintf(w, "%s ->\n", e.Old)
			continue
		}
		fmt.Fprintf(w, "%s -> %s\n", e.Old, strings.Join(e.New, ", "))
	}
	prefix := ""
	if res.DryRun {
		prefix = "dry run: "
	}
	fmt.Fprintf(w, "%swithdrawn %d, edges %d, tasks %d, stale plans %d\n",
		prefix, res.Withdrawn, res.Edges, res.Tasks, res.StalePlans)
}
