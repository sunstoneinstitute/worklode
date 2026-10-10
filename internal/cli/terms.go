package cli

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ListTerms calls GET /api/v1/projects/{id}/terms: the project's
// definition rules with their term slugs (WL-REQ-1368).
func (c *Client) ListTerms(ctx context.Context, project string) ([]model.Term, []byte, error) {
	return doJSON[[]model.Term](ctx, c, http.MethodGet, "/api/v1/projects/"+url.PathEscape(project)+"/terms", nil, "terms")
}

// TermsTable prints `lode rule terms`: one row per definition.
func TermsTable(w io.Writer, terms []model.Term) {
	tbl := newTable(
		column{header: "SLUG"},
		column{header: "REF"},
		column{header: "STATUS"},
		column{header: "CONCEPT"},
		titleColumn("HEADING"),
	)
	for _, t := range terms {
		tbl.add(t.Slug, t.Rule.Ref, t.Rule.Status, t.Rule.ConceptIRI, t.Rule.Heading)
	}
	tbl.flush(w)
}
