package githubauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// fakeGitHub serves the identity + membership endpoints this package calls.
func fakeGitHub(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(apiBase string) *Client {
	return &Client{
		ClientID:     "cid",
		ClientSecret: "secret",
		APIBase:      apiBase,
		Endpoint:     oauth2.Endpoint{AuthURL: apiBase + "/login/oauth/authorize", TokenURL: apiBase + "/login/oauth/access_token"},
	}
}

func TestAuthCodeURLIncludesState(t *testing.T) {
	c := newTestClient("https://example.test")
	u := c.AuthCodeURL("https://wl/auth/github/callback", "xyz")
	if !strings.Contains(u, "state=xyz") || !strings.Contains(u, "client_id=cid") {
		t.Fatalf("bad authorize url: %s", u)
	}
}

func TestFetchIdentity(t *testing.T) {
	srv := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(map[string]any{"id": 42, "login": "octocat", "name": "The Octocat"})
			return
		}
		http.NotFound(w, r)
	})
	c := newTestClient(srv.URL)
	id, err := c.FetchIdentity(context.Background(), "tok")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if id.ID != 42 || id.Login != "octocat" || id.Name != "The Octocat" {
		t.Fatalf("bad identity: %+v", id)
	}
}
