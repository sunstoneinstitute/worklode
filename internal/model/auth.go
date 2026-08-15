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
