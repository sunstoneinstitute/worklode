package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// GetClause calls GET /api/v1/clauses/{ref}: one design clause with its
// current text and the documents arranging it.
func (c *Client) GetClause(ctx context.Context, ref string) (model.Clause, []byte, error) {
	return doJSON[model.Clause](ctx, c, http.MethodGet, "/api/v1/clauses/"+url.PathEscape(ref), nil, "clause")
}

// ClauseRender is the human view of one clause: its ref and heading, status
// and version, where it is arranged, then its text.
func ClauseRender(w io.Writer, c model.Clause) {
	fmt.Fprintf(w, "%s  %s\n", c.Ref, c.Heading)
	fmt.Fprintf(w, "  status:   %s\n", c.Status)
	fmt.Fprintf(w, "  version:  %d\n", c.Version)
	fmt.Fprintf(w, "  updated:  %s\n", LocalTime(c.UpdatedAt))
	for _, a := range c.ArrangedIn {
		fmt.Fprintf(w, "  arranged: %s#%s (depth %d, v%d)\n", a.DocRef, a.Anchor, a.Depth, a.ClauseVersion)
	}
	if c.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, c.Body)
	}
}
