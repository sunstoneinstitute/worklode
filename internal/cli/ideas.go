// ideas.go is the client and the rendering for ideas (WL-REQ-1799).
package cli

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func ideaPath(id string) string { return "/api/v1/ideas/" + url.PathEscape(id) }

// ListIdeas calls GET /api/v1/ideas.
func (c *Client) ListIdeas(ctx context.Context) (model.IdeaListResponse, []byte, error) {
	return doJSON[model.IdeaListResponse](ctx, c, http.MethodGet, "/api/v1/ideas", nil, "idea list")
}

// CreateIdea calls POST /api/v1/ideas.
func (c *Client) CreateIdea(ctx context.Context, in model.CreateIdeaInput) (model.Idea, []byte, error) {
	return doJSON[model.Idea](ctx, c, http.MethodPost, "/api/v1/ideas", in, "idea")
}

// GetIdea calls GET /api/v1/ideas/{id}.
func (c *Client) GetIdea(ctx context.Context, id string) (model.Idea, []byte, error) {
	return doJSON[model.Idea](ctx, c, http.MethodGet, ideaPath(id), nil, "idea")
}

// PatchIdea calls PATCH /api/v1/ideas/{id}.
func (c *Client) PatchIdea(ctx context.Context, id string, in model.PatchIdeaInput) (model.Idea, []byte, error) {
	return doJSON[model.Idea](ctx, c, http.MethodPatch, ideaPath(id), in, "idea")
}

// ShareIdea calls POST /api/v1/ideas/{id}/share.
func (c *Client) ShareIdea(ctx context.Context, id string) (model.Idea, []byte, error) {
	return doJSON[model.Idea](ctx, c, http.MethodPost, ideaPath(id)+"/share", nil, "idea")
}

// DeleteIdea calls DELETE /api/v1/ideas/{id}.
func (c *Client) DeleteIdea(ctx context.Context, id, justification string) error {
	_, err := c.do(ctx, http.MethodDelete, ideaPath(id), model.DeleteInput{Justification: justification})
	return err
}

// SetIdeaIntake calls PATCH /api/v1/ideas/{id}/intake.
func (c *Client) SetIdeaIntake(ctx context.Context, id string, in model.PatchIdeaIntakeInput) (model.Idea, []byte, error) {
	return doJSON[model.Idea](ctx, c, http.MethodPatch, ideaPath(id)+"/intake", in, "idea")
}

// IdeaTable prints idea rows.
func IdeaTable(w io.Writer, ideas []model.Idea) {
	tbl := newTable(
		column{header: "ID"},
		titleColumn("TITLE"),
		column{header: "TYPE"},
		column{header: "STATUS"},
		column{header: "AUTHOR"},
		column{header: "TASK"},
		column{header: "SUBMITTED"},
	)
	for _, i := range ideas {
		tbl.add(i.ID, i.Title, i.Type, i.Status, i.Author, dash(i.Task), i.Submitted)
	}
	tbl.flush(w)
}
