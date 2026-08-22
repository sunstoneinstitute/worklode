package cli

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// --- actors and tokens --------------------------------------------------

// CreateActor calls POST /api/v1/actors.
func (c *Client) CreateActor(ctx context.Context, in model.CreateActorInput) (model.Actor, []byte, error) {
	return doJSON[model.Actor](ctx, c, http.MethodPost, "/api/v1/actors", in, "actor")
}

// CreateToken calls POST /api/v1/actors/{id}/tokens. A nil expiresAt means
// the token never expires.
func (c *Client) CreateToken(ctx context.Context, actorID, description string, expiresAt *time.Time) (model.TokenResponse, []byte, error) {
	in := model.CreateTokenInput{Description: description}
	if expiresAt != nil {
		exp := expiresAt.UTC().Format(time.RFC3339)
		in.ExpiresAt = &exp
	}
	return doJSON[model.TokenResponse](ctx, c, http.MethodPost, "/api/v1/actors/"+url.PathEscape(actorID)+"/tokens", in, "token response")
}

// RevokeToken calls DELETE /api/v1/tokens (204, no body). token may be
// either the plaintext or its stored hash.
func (c *Client) RevokeToken(ctx context.Context, token string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tokens", model.RevokeTokenInput{Token: token})
}

// WhoAmI calls GET /api/v1/whoami: which actor the configured token belongs
// to. A *ClientError with Status 401 means the token is not accepted; a
// transport error means the server is unreachable — lode doctor tells those
// two failures apart.
func (c *Client) WhoAmI(ctx context.Context) (model.WhoAmI, []byte, error) {
	return doJSON[model.WhoAmI](ctx, c, http.MethodGet, "/api/v1/whoami", nil, "whoami")
}
