package cli

import (
	"context"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Replan calls POST /api/v1/work/replan: `lode work next --replan` (S28).
func (c *Client) Replan(ctx context.Context, in model.ReplanInput) (model.ClaimNextResponse, []byte, error) {
	return doJSON[model.ClaimNextResponse](ctx, c, http.MethodPost, "/api/v1/work/replan", in, "replan response")
}
