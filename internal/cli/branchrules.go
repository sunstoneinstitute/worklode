package cli

import (
	"context"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// BranchRules calls GET /api/v1/repos/branch-rules: the rules last observed
// on the default branch of the repo remote names. The server normalizes the
// remote. A *ClientError with Status 404 means nothing has been observed.
func (c *Client) BranchRules(ctx context.Context, remote string) (model.RepoBranchRules, error) {
	r, _, err := doJSON[model.RepoBranchRules](ctx, c, http.MethodGet,
		withParams("/api/v1/repos/branch-rules", model.RepoBranchRulesParams{Remote: remote}), nil, "branch rules")
	return r, err
}
