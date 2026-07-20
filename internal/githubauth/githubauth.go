// Package githubauth wraps the GitHub App user-authorization (OAuth) flow for
// work-tracker's web login: it builds the authorize URL, exchanges the code for
// a user-to-server token, and reads the user's identity plus org/team
// membership. It parallels internal/oidc and never touches it. A Client is
// built only when the GitHub App client id and secret are configured.
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
	Org          string
	AdminTeam    string
	APIBase      string          // e.g. https://api.github.com
	Endpoint     oauth2.Endpoint // authorize/token endpoints
}

// New builds a Client for the public GitHub.
func New(clientID, clientSecret, org, adminTeam string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Org:          org,
		AdminTeam:    adminTeam,
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

// Exchange redeems an authorization code for a user-to-server token.
func (c *Client) Exchange(ctx context.Context, redirectURL, code string) (*Token, error) {
	tok, err := c.oauthConfig(redirectURL).Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("github code exchange: %w", err)
	}
	return &Token{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, Expiry: tok.Expiry}, nil
}

// Identity is the subset of GET /user work-tracker consumes.
type Identity struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

// httpClient carries an explicit timeout so a hung GitHub API call cannot block
// the login callback forever, matching the codebase convention.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// get performs an authenticated GET and decodes JSON into out. It returns the
// HTTP status so callers can distinguish 404 (not a member) from real errors.
func (c *Client) get(ctx context.Context, token, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+path, nil)
	if err != nil {
		return 0, fmt.Errorf("build github request for %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("github GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode github %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
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

// Roles is the authorization derived from GitHub membership.
type Roles struct {
	User  bool // active member of Org
	Admin bool // active member of AdminTeam
}

type membershipResp struct {
	State string `json:"state"`
}

// activeMembership returns true when the endpoint returns 200 with state
// "active". A 404 means "not a member" and yields false, nil.
func (c *Client) activeMembership(ctx context.Context, token, path string) (bool, error) {
	var m membershipResp
	code, err := c.get(ctx, token, path, &m)
	if err != nil {
		return false, err
	}
	switch code {
	case http.StatusOK:
		return m.State == "active", nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("github GET %s: status %d", path, code)
	}
}

// Roles evaluates org membership (→ User) and admin-team membership (→ Admin)
// for login, using the user-to-server token.
func (c *Client) Roles(ctx context.Context, token, login string) (Roles, error) {
	user, err := c.activeMembership(ctx, token, "/user/memberships/orgs/"+c.Org)
	if err != nil {
		return Roles{}, err
	}
	if !user {
		return Roles{}, nil
	}
	admin, err := c.activeMembership(ctx, token,
		fmt.Sprintf("/orgs/%s/teams/%s/memberships/%s", c.Org, c.AdminTeam, login))
	if err != nil {
		return Roles{}, err
	}
	return Roles{User: true, Admin: admin}, nil
}
