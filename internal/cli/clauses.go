package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

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
	for _, gt := range c.GovernedTasks {
		fmt.Fprintf(w, "  governs:  %s %s (%s, %s, v%d)\n", gt.ID, gt.Title, gt.State, gt.Source, gt.ClauseVersion)
	}
	if c.Body != "" {
		fmt.Fprintln(w)
		Markdown(w, c.Body)
	}
}

// EditClause calls PUT /api/v1/clauses/{ref}.
func (c *Client) EditClause(ctx context.Context, ref string, in model.EditClauseInput) (model.Clause, []byte, error) {
	return doJSON[model.Clause](ctx, c, http.MethodPut, "/api/v1/clauses/"+url.PathEscape(ref), in, "clause")
}

// ListClauseVersions calls GET /api/v1/clauses/{ref}/versions.
func (c *Client) ListClauseVersions(ctx context.Context, ref string) ([]model.ClauseVersion, []byte, error) {
	return doJSON[[]model.ClauseVersion](ctx, c, http.MethodGet, "/api/v1/clauses/"+url.PathEscape(ref)+"/versions", nil, "clause versions")
}

// GetClauseVersion calls GET /api/v1/clauses/{ref}/versions/{n}.
func (c *Client) GetClauseVersion(ctx context.Context, ref string, version int) (model.Clause, []byte, error) {
	return doJSON[model.Clause](ctx, c, http.MethodGet, fmt.Sprintf("/api/v1/clauses/%s/versions/%d", url.PathEscape(ref), version), nil, "clause")
}

// ClauseVersionsTable lists a clause's versions, newest first: the `lode
// clause versions` view.
func ClauseVersionsTable(w io.Writer, vs []model.ClauseVersion) {
	tbl := newTable(
		column{header: "VERSION"},
		titleColumn("HEADING"),
		column{header: "CREATED"},
	)
	for _, v := range vs {
		tbl.add(strconv.Itoa(v.Version), v.Heading, LocalTime(v.CreatedAt))
	}
	tbl.flush(w)
}
