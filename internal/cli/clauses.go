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

// GetClause calls GET /api/v1/clauses/{ref}: one design clause with its
// current text and the documents arranging it.
func (c *Client) GetClause(ctx context.Context, ref string) (model.Clause, []byte, error) {
	return doJSON[model.Clause](ctx, c, http.MethodGet, "/api/v1/clauses/"+url.PathEscape(ref), nil, "clause")
}

// ClauseListFilter narrows ListClauses. Doc is any document ref
// (WL-SPEC-73, a slug, an id). Zero-valued fields do not filter.
type ClauseListFilter struct {
	Project, Doc, Status string
}

// ListClauses calls GET /api/v1/clauses: clauses without their text, in
// arrangement order when Doc is set, by number otherwise.
func (c *Client) ListClauses(ctx context.Context, f ClauseListFilter) ([]model.Clause, []byte, error) {
	q := url.Values{}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	if f.Doc != "" {
		q.Set("doc", f.Doc)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	path := "/api/v1/clauses"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return doJSON[[]model.Clause](ctx, c, http.MethodGet, path, nil, "clauses")
}

// ClausesTable prints `lode clause list`: one row per clause with every
// placement that arranges it.
func ClausesTable(w io.Writer, clauses []model.Clause) {
	tbl := newTable(
		column{header: "REF"},
		column{header: "STATUS"},
		column{header: "VER"},
		column{header: "ARRANGED"},
		titleColumn("HEADING"),
	)
	for _, c := range clauses {
		placed := make([]string, len(c.ArrangedIn))
		for i, a := range c.ArrangedIn {
			placed[i] = a.DocRef + "#" + a.Anchor
		}
		tbl.add(c.Ref, c.Status, strconv.Itoa(c.Version), strings.Join(placed, ", "), c.Heading)
	}
	tbl.flush(w)
}

// ClauseRender is the human view of one clause: its ref and heading, status
// and version, where it is arranged, then its text.
func ClauseRender(w io.Writer, c model.Clause) {
	fmt.Fprintf(w, "%s  %s\n", c.Ref, c.Heading)
	fmt.Fprintf(w, "  status:   %s\n", c.Status)
	fmt.Fprintf(w, "  version:  %d\n", c.Version)
	if c.Owner != "" {
		fmt.Fprintf(w, "  owner:    %s\n", c.Owner)
	}
	if len(c.Tags) > 0 {
		fmt.Fprintf(w, "  tags:     %s\n", strings.Join(c.Tags, ", "))
	}
	fmt.Fprintf(w, "  updated:  %s\n", LocalTime(c.UpdatedAt))
	for _, a := range c.ArrangedIn {
		fmt.Fprintf(w, "  arranged: %s#%s (depth %d, v%d)\n", a.DocRef, a.Anchor, a.Depth, a.ClauseVersion)
	}
	for _, gt := range c.GovernedTasks {
		fmt.Fprintf(w, "  governs:  %s %s (%s, %s, v%d)\n", gt.ID, gt.Title, gt.State, gt.Source, gt.ClauseVersion)
	}
	for _, e := range c.Edges {
		if e.From == c.Ref {
			fmt.Fprintf(w, "  %-9s %s %s (%s)\n", e.Type+":", e.To, e.ToHeading, e.Source)
		} else {
			fmt.Fprintf(w, "  %-9s %s %s (%s, incoming)\n", e.Type+":", e.From, e.FromHeading, e.Source)
		}
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

// SetClauseMeta calls PATCH /api/v1/clauses/{ref}: owner and/or tags (S15).
func (c *Client) SetClauseMeta(ctx context.Context, ref string, in model.ClauseMetaInput) (model.Clause, []byte, error) {
	return doJSON[model.Clause](ctx, c, http.MethodPatch, "/api/v1/clauses/"+url.PathEscape(ref), in, "clause")
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
