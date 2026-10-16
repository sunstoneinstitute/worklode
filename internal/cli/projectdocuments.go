// projectdocuments.go is the client and the rendering for a project's
// documents register (WL-REQ-1798).
package cli

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ListProjectDocuments calls GET /api/v1/projects/{id}/documents.
func (c *Client) ListProjectDocuments(ctx context.Context, project string) (model.ProjectDocumentListResponse, []byte, error) {
	return doJSON[model.ProjectDocumentListResponse](ctx, c, http.MethodGet,
		"/api/v1/projects/"+url.PathEscape(project)+"/documents", nil, "document list")
}

// CreateProjectDocument calls POST /api/v1/projects/{id}/documents.
func (c *Client) CreateProjectDocument(ctx context.Context, project string, in model.CreateProjectDocumentInput) (model.ProjectDocument, []byte, error) {
	return doJSON[model.ProjectDocument](ctx, c, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(project)+"/documents", in, "document")
}

// PatchProjectDocument calls PATCH /api/v1/documents/{id}.
func (c *Client) PatchProjectDocument(ctx context.Context, id string, in model.PatchProjectDocumentInput) (model.ProjectDocument, []byte, error) {
	return doJSON[model.ProjectDocument](ctx, c, http.MethodPatch,
		"/api/v1/documents/"+url.PathEscape(id), in, "document")
}

// DeleteProjectDocument calls DELETE /api/v1/documents/{id}.
func (c *Client) DeleteProjectDocument(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/v1/documents/"+url.PathEscape(id), nil)
	return err
}

// ProjectDocumentTable prints register rows.
func ProjectDocumentTable(w io.Writer, ds []model.ProjectDocument) {
	tbl := newTable(
		column{header: "ID"},
		titleColumn("NAME"),
		column{header: "CATEGORY"},
		column{header: "TYPE"},
		column{header: "STATUS"},
		column{header: "URL"},
	)
	for _, d := range ds {
		tbl.add(d.ID, d.Name, d.Category, d.Type, d.Status, dash(d.URL))
	}
	tbl.flush(w)
}
