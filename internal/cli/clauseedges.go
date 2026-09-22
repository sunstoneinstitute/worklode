package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// LinkClauses calls POST /api/v1/clauses/{ref}/edges.
func (c *Client) LinkClauses(ctx context.Context, ref string, in model.ClauseEdgeInput) ([]byte, error) {
	_, raw, err := doJSON[model.ClauseEdgeInput](ctx, c, http.MethodPost, "/api/v1/clauses/"+url.PathEscape(ref)+"/edges", in, "clause edge")
	return raw, err
}

// UnlinkClauses calls DELETE /api/v1/clauses/{ref}/edges.
func (c *Client) UnlinkClauses(ctx context.Context, ref string, in model.ClauseEdgeInput) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/clauses/"+url.PathEscape(ref)+"/edges", in)
}
