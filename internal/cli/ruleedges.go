package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// LinkRules calls POST /api/v1/rules/{ref}/edges.
func (c *Client) LinkRules(ctx context.Context, ref string, in model.RuleEdgeInput) ([]byte, error) {
	_, raw, err := doJSON[model.RuleEdgeInput](ctx, c, http.MethodPost, "/api/v1/rules/"+url.PathEscape(ref)+"/edges", in, "rule edge")
	return raw, err
}

// UnlinkRules calls DELETE /api/v1/rules/{ref}/edges.
func (c *Client) UnlinkRules(ctx context.Context, ref string, in model.RuleEdgeInput) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/rules/"+url.PathEscape(ref)+"/edges", in)
}
