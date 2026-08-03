package graphserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// FromEnv builds a Client from the environment:
//
//	LODE_GRAPHSERVER_URL            base URL, e.g. https://graph.dev.sunstoneinstitute.ai (required; must be absolute http(s))
//	LODE_GRAPHSERVER_TOKEN_URL      Keycloak token endpoint (client-credentials)
//	LODE_GRAPHSERVER_CLIENT_ID      OAuth2 client id, e.g. dataplatform-svc
//	LODE_GRAPHSERVER_CLIENT_SECRET  OAuth2 client secret
//
// The three auth variables must be set together or not at all; absent, the
// client is unauthenticated (a server without AUTH_ENFORCE).
func FromEnv() (*Client, error) {
	base := os.Getenv("LODE_GRAPHSERVER_URL")
	if base == "" {
		return nil, errors.New("LODE_GRAPHSERVER_URL is not set")
	}
	if err := validateBaseURL(base); err != nil {
		return nil, err
	}
	tokenURL := os.Getenv("LODE_GRAPHSERVER_TOKEN_URL")
	id := os.Getenv("LODE_GRAPHSERVER_CLIENT_ID")
	secret := os.Getenv("LODE_GRAPHSERVER_CLIENT_SECRET")
	if tokenURL == "" && id == "" && secret == "" {
		return New(base, nil), nil
	}
	var missing []string
	for _, kv := range []struct{ k, v string }{
		{"LODE_GRAPHSERVER_TOKEN_URL", tokenURL},
		{"LODE_GRAPHSERVER_CLIENT_ID", id},
		{"LODE_GRAPHSERVER_CLIENT_SECRET", secret},
	} {
		if kv.v == "" {
			missing = append(missing, kv.k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("graph-server client credentials are partly configured; missing %s", strings.Join(missing, ", "))
	}
	cc := clientcredentials.Config{ClientID: id, ClientSecret: secret, TokenURL: tokenURL}
	// The token fetch itself ignores any context passed to oauth2.NewClient
	// (Transport.RoundTrip calls Source.Token with no context); the only way
	// to bound it is to bake httpClient into the ctx the TokenSource closes
	// over here, at construction.
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	return New(base, cc.TokenSource(ctx)), nil
}

// validateBaseURL rejects anything that isn't an absolute http(s) URL with a
// host, so a typo'd LODE_GRAPHSERVER_URL fails loudly at construction rather
// than reading as an empty knowledge graph (every GetGraph/DeleteGraph 404s
// into ErrNotFound).
func validateBaseURL(base string) error {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("LODE_GRAPHSERVER_URL must be an absolute http(s) URL (e.g. https://graph.dev.sunstoneinstitute.ai): %q", base)
	}
	return nil
}
