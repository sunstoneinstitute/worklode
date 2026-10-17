package model

// OAuthServerMetadata is the response body of GET
// /.well-known/oauth-authorization-server (RFC 8414): how an MCP client logs
// in to this server. Issuer and TokenEndpoint name the origin the client
// asked on; AuthorizationEndpoint is always on LODE_PUBLIC_URL, because the
// browser leg of the login runs there.
type OAuthServerMetadata struct {
	Issuer                                     string   `json:"issuer"`
	AuthorizationEndpoint                      string   `json:"authorization_endpoint"`
	TokenEndpoint                              string   `json:"token_endpoint"`
	ResponseTypesSupported                     []string `json:"response_types_supported"`
	GrantTypesSupported                        []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported              []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported          []string `json:"token_endpoint_auth_methods_supported"`
	AuthorizationResponseIssParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
}

// OAuthToken is the success body of POST /oauth/token (RFC 6749 §5.1): a wl_
// token for the MCP client, valid for ExpiresIn seconds. There is no refresh
// token; the client logs in again when it expires.
type OAuthToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// OAuthError is the error body of POST /oauth/token (RFC 6749 §5.2). Error is
// one of the RFC's codes (invalid_request, invalid_client, invalid_grant,
// unsupported_grant_type, server_error) or RFC 8707's invalid_target.
type OAuthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}
