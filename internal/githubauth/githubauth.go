// Package githubauth wraps the GitHub App user-authorization (OAuth) flow:
// it builds the authorize URL, exchanges the code for a user-to-server
// token, and reads the user's identity. Keycloak is worklode's only login
// provider (spec 023 §3.1); these primitives are dormant raw material for
// the deferred account-link flow (spec 023 §3.3). It parallels internal/oidc
// and never touches it. A Client is built only when the GitHub App client id
// and secret are configured.
package githubauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	githuboauth "golang.org/x/oauth2/github"
)

// Client holds GitHub App OAuth config. APIBase and Endpoint default to the
// public GitHub in New but are overridable in tests.
type Client struct {
	ClientID     string
	ClientSecret string
	APIBase      string          // e.g. https://api.github.com
	Endpoint     oauth2.Endpoint // authorize/token endpoints
}

// New builds a Client for the public GitHub.
func New(clientID, clientSecret string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		APIBase:      "https://api.github.com",
		Endpoint:     githuboauth.Endpoint,
	}
}

// oauthConfig builds the oauth2 config for the given redirect URL. No scopes:
// a GitHub App's user-to-server access is governed by the App's permissions,
// not OAuth scopes.
func (c *Client) oauthConfig(redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Endpoint:     c.Endpoint,
		RedirectURL:  redirectURL,
	}
}

// AuthCodeURL returns the GitHub authorize URL carrying state.
func (c *Client) AuthCodeURL(redirectURL, state string) string {
	return c.oauthConfig(redirectURL).AuthCodeURL(state)
}

// Token is the user-to-server token pair returned by Exchange.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// Exchange redeems an authorization code for a user-to-server token. It routes
// the token-endpoint request through httpClient (with its timeout) so a hung
// GitHub token endpoint cannot block the login callback indefinitely.
func (c *Client) Exchange(ctx context.Context, redirectURL, code string) (*Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	tok, err := c.oauthConfig(redirectURL).Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("github code exchange: %w", err)
	}
	return &Token{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, Expiry: tok.Expiry}, nil
}

// Identity is the subset of GET /user worklode consumes.
type Identity struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

// httpClient carries an explicit timeout so a hung GitHub API call cannot block
// the login callback forever, matching the codebase convention.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// githubJSON performs an authenticated request against the GitHub API and,
// on a 2xx with a non-nil out, decodes the JSON body into it. It returns the
// HTTP status so callers can treat a specific code (404 = not a member, no
// releases) as a fact rather than an error.
func githubJSON(ctx context.Context, method, url, auth string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build github request %s %s: %w", method, url, err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("github %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode github %s %s: %w", method, url, err)
		}
	}
	return resp.StatusCode, nil
}

// get performs an authenticated GET against the client's API base.
func (c *Client) get(ctx context.Context, token, path string, out any) (int, error) {
	return githubJSON(ctx, http.MethodGet, c.APIBase+path, "Bearer "+token, out)
}

// FetchIdentity reads GET /user with the user-to-server token.
func (c *Client) FetchIdentity(ctx context.Context, token string) (*Identity, error) {
	var id Identity
	code, err := c.get(ctx, token, "/user", &id)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("github GET /user: status %d", code)
	}
	return &id, nil
}
