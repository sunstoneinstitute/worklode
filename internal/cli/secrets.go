package cli

import (
	"context"
	"net/http"
	"net/url"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// SecretsCatalog calls GET /api/v1/secrets/catalog (authenticated; 404 when
// the server has no catalog configured).
func (c *Client) SecretsCatalog(ctx context.Context) (model.SecretCatalogResponse, []byte, error) {
	return doJSON[model.SecretCatalogResponse](ctx, c, http.MethodGet, "/api/v1/secrets/catalog", nil, "secrets catalog")
}

// RecordSecretsMaterialized calls POST /api/v1/tasks/{id}/secrets-materialized
// with the materialized name list — the names-only audit event of spec 017.
func (c *Client) RecordSecretsMaterialized(ctx context.Context, id string, names []string) error {
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/secrets-materialized",
		model.SecretsMaterializedInput{Names: names})
	return err
}
