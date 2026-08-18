package model

// SecretCatalogEntry is the wire form of one org secrets-catalog entry
// (spec 017): a symbolic name mapped to a 1Password reference plus policy.
type SecretCatalogEntry struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	Description string `json:"description"`
	Baseline    bool   `json:"baseline"`
}

// SecretCatalogResponse is the response body of GET /api/v1/secrets/catalog.
type SecretCatalogResponse struct {
	Secrets []SecretCatalogEntry `json:"secrets"`
}

// SecretsMaterializedInput is the request body of POST
// /api/v1/tasks/{id}/secrets-materialized: the claim-ceremony hook reporting
// which secret names it put in the local keystore. Names only — an op://
// ref or a raw value cannot pass the name grammar, so neither can enter the
// audit trail through this endpoint.
type SecretsMaterializedInput struct {
	Names []string `json:"names"`
}
