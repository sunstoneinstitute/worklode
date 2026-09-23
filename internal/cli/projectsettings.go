package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// SetProjectSettings calls PATCH /api/v1/projects/{id}/settings, merging
// patch into the project's settings (increment 3 R9). A nil value for a key
// removes it. The server's allowlist refuses an unknown key or a
// wrong-shaped value with a 422 naming it.
func (c *Client) SetProjectSettings(ctx context.Context, id string, patch map[string]any) (model.Project, []byte, error) {
	return doJSON[model.Project](ctx, c, http.MethodPatch,
		"/api/v1/projects/"+url.PathEscape(id)+"/settings", patch, "project")
}
