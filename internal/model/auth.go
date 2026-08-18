package model

// OIDCTokenInput is the request body for the OIDC token exchange (POST
// /auth/oidc/token): a verified Keycloak ID token traded for a wl_ token.
type OIDCTokenInput struct {
	IDToken string `json:"id_token"`
}

// LoginDiscovery is the response body of GET /.well-known/lode-login: where
// the CLI starts an interactive login and which providers are available.
type LoginDiscovery struct {
	AuthorizeURL string   `json:"authorize_url"`
	TokenURL     string   `json:"token_url"`
	Providers    []string `json:"providers"`
}

// CLITokenInput is the request body for the CLI token exchange (POST
// /auth/cli/token): a one-time code (proof the browser login completed)
// traded for a wl_ token.
type CLITokenInput struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// MintedToken is the response body of both token exchanges (POST
// /auth/cli/token and POST /auth/oidc/token): the freshly minted wl_ token,
// who it belongs to, and when it expires. ExpiresAt is RFC 3339 text rather
// than a time.Time because the CLI stores and prints it verbatim.
type MintedToken struct {
	Token     string `json:"token"`
	ActorID   string `json:"actor_id"`
	ExpiresAt string `json:"expires_at"`
}

// OIDCConfig is the response body of GET /auth/oidc/config: what a CLI needs
// to run the auth-code flow itself.
type OIDCConfig struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
}
