// GitHub App (installation) authentication and repo delivery-profile
// discovery, alongside the user-authorization flow in githubauth.go.
// Optional: when the app id/key are not configured the server builds no
// AppAuth, discovery is skipped, and a repo mapping keeps its default
// done_state. Discovery never gates a request — see the delivery-lifecycle
// design spec.

package githubauth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// AppAuth signs GitHub App JWTs and mints installation tokens.
type AppAuth struct {
	AppID   string
	Key     *rsa.PrivateKey
	BaseURL string // https://api.github.com, overridable in tests
}

// appJWTLifetime is well under GitHub's 10-minute ceiling; the JWT is minted
// per call and used immediately.
const appJWTLifetime = 9 * time.Minute

// ParseAppPrivateKey parses the PEM private key GitHub issues for an App,
// accepting both the PKCS#1 form GitHub hands out and PKCS#8. Errors describe
// the failure only — they never echo the key material.
func ParseAppPrivateKey(pemData []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("github app key: no PEM block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app key: not an RSA key")
	}
	return key, nil
}

// appJWT mints the short-lived RS256 assertion that authenticates worklode as
// the App itself (as opposed to one of its installations). iat is backdated a
// minute to tolerate clock skew, as GitHub recommends.
func (a *AppAuth) appJWT() (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: a.Key}, nil)
	if err != nil {
		return "", fmt.Errorf("github app jwt signer: %w", err)
	}
	now := time.Now()
	s, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   a.AppID,
		IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)),
		Expiry:   jwt.NewNumericDate(now.Add(appJWTLifetime)),
	}).Serialize()
	if err != nil {
		return "", fmt.Errorf("github app jwt: %w", err)
	}
	return s, nil
}

// SubscribedEvents returns the event names this App is subscribed to, read
// from GET /app under the App JWT. The Apps settings page shows permissions
// and event subscriptions separately, so an App can hold issues:write and
// still never receive an issues event — this is what surfaces that.
func (a *AppAuth) SubscribedEvents(ctx context.Context) ([]string, error) {
	jwtStr, err := a.appJWT()
	if err != nil {
		return nil, err
	}
	var app struct {
		Events []string `json:"events"`
	}
	code, err := githubJSON(ctx, http.MethodGet, a.BaseURL+"/app", "Bearer "+jwtStr, &app)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("get app: status %d", code)
	}
	return app.Events, nil
}

// repoPath renders "owner/name" as an escaped URL path segment pair, rejecting
// anything that is not exactly two non-empty segments so a malformed mapping
// cannot reshape the request URL.
func repoPath(repo string) (string, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("repo %q is not owner/name", repo)
	}
	return url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

// InstallationToken mints a short-lived installation token scoped to the
// installation that owns repo ("owner/name").
func (a *AppAuth) InstallationToken(ctx context.Context, repo string) (string, error) {
	path, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	jwtStr, err := a.appJWT()
	if err != nil {
		return "", err
	}
	appAuth := "Bearer " + jwtStr

	var inst struct {
		ID int64 `json:"id"`
	}
	code, err := githubJSON(ctx, http.MethodGet, a.BaseURL+"/repos/"+path+"/installation", appAuth, &inst)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("github app installation for %s: status %d", repo, code)
	}

	var tok struct {
		Token string `json:"token"`
	}
	mintURL := fmt.Sprintf("%s/app/installations/%d/access_tokens", a.BaseURL, inst.ID)
	code, err = githubJSON(ctx, http.MethodPost, mintURL, appAuth, &tok)
	if err != nil {
		return "", err
	}
	if code != http.StatusCreated && code != http.StatusOK {
		return "", fmt.Errorf("mint installation token for %s: status %d", repo, code)
	}
	if tok.Token == "" {
		return "", fmt.Errorf("mint installation token for %s: empty token", repo)
	}
	return tok.Token, nil
}

// DiscoverDoneState inspects a repo's GitHub environments and releases and
// returns the done_state they imply: a prod-ish environment → deployed_prod;
// releases without one → released; neither → merged. Only a 404 from the
// release endpoint means "no releases"; any other failure is reported so the
// caller keeps the default rather than seeding a guess.
func (a *AppAuth) DiscoverDoneState(ctx context.Context, repo string) (string, error) {
	path, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return "", err
	}
	auth := "Bearer " + token

	var envs struct {
		Environments []struct {
			Name string `json:"name"`
		} `json:"environments"`
	}
	// per_page=100 is GitHub's maximum; the default of 30 would hide a prod
	// environment past the first page and mis-seed done_state permanently.
	envURL := a.BaseURL + "/repos/" + path + "/environments?per_page=100"
	code, err := githubJSON(ctx, http.MethodGet, envURL, auth, &envs)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("list environments for %s: status %d", repo, code)
	}
	for _, e := range envs.Environments {
		if store.NormalizeEnvironment(e.Name) == "prod" {
			return "deployed_prod", nil
		}
	}

	code, err = githubJSON(ctx, http.MethodGet, a.BaseURL+"/repos/"+path+"/releases/latest", auth, nil)
	if err != nil {
		return "", err
	}
	switch code {
	case http.StatusOK:
		return "released", nil
	case http.StatusNotFound:
		return "merged", nil
	default:
		return "", fmt.Errorf("latest release for %s: status %d", repo, code)
	}
}
