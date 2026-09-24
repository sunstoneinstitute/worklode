package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Govern calls POST /api/v1/tasks/{id}/governed-by: rule governs id. pin
// records the current version as the version the link stays pinned to (S10).
func (c *Client) Govern(ctx context.Context, id, rule string, pin bool) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/governed-by", model.GovernInput{Rule: rule, Pin: pin})
}

// Ungovern calls DELETE /api/v1/tasks/{id}/governed-by: rule no longer
// governs id.
func (c *Client) Ungovern(ctx context.Context, id, rule string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/governed-by", model.GovernInput{Rule: rule})
}
