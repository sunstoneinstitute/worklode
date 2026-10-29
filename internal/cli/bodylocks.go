package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// The body editor's lock routes (WL-REQ-1868), for the e2e suite: no lode
// verb takes a lock. A 409 comes back as a *ClientError whose Msg is the
// BodyLock or LockLost JSON.

func lockPath(kind, id string) string {
	return "/api/v1/locks/" + url.PathEscape(kind) + "/" + url.PathEscape(id)
}

// TakeLock calls POST /api/v1/locks/{kind}/{id}.
func (c *Client) TakeLock(ctx context.Context, kind, id string) (model.BodyLock, []byte, error) {
	return doJSON[model.BodyLock](ctx, c, http.MethodPost, lockPath(kind, id), nil, "lock")
}

// GetLock calls GET /api/v1/locks/{kind}/{id}.
func (c *Client) GetLock(ctx context.Context, kind, id string) (model.BodyLock, []byte, error) {
	return doJSON[model.BodyLock](ctx, c, http.MethodGet, lockPath(kind, id), nil, "lock")
}

// CheckInLock calls PUT /api/v1/locks/{kind}/{id}.
func (c *Client) CheckInLock(ctx context.Context, kind, id string, in model.LockCheckIn) (model.BodyLock, []byte, error) {
	return doJSON[model.BodyLock](ctx, c, http.MethodPut, lockPath(kind, id), in, "lock")
}

// ReleaseLock calls DELETE /api/v1/locks/{kind}/{id}?session=.
func (c *Client) ReleaseLock(ctx context.Context, kind, id, session string) error {
	_, err := c.do(ctx, http.MethodDelete, withParams(lockPath(kind, id), model.LockReleaseParams{Session: session}), nil)
	return err
}

// UnlockBody calls POST /api/v1/locks/{kind}/{id}/unlock.
func (c *Client) UnlockBody(ctx context.Context, kind, id string) error {
	_, err := c.do(ctx, http.MethodPost, lockPath(kind, id)+"/unlock", nil)
	return err
}
