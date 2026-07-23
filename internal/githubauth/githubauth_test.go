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
		Org:          "sunstoneinstitute",
		AdminTeam:    "worklode-admins",
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

func membershipHandler(t *testing.T, orgState, teamStatus string, teamState string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user/memberships/orgs/sunstoneinstitute":
			if orgState == "" {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"state": orgState})
		case r.URL.Path == "/orgs/sunstoneinstitute/teams/worklode-admins/memberships/octocat":
			if teamStatus == "404" {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"state": teamState})
		default:
			http.NotFound(w, r)
		}
	}
}

func TestRolesMember(t *testing.T) {
	srv := fakeGitHub(t, membershipHandler(t, "active", "200", "active"))
	c := newTestClient(srv.URL)
	roles, err := c.Roles(context.Background(), "tok", "octocat")
	if err != nil {
		t.Fatalf("roles: %v", err)
	}
	if !roles.User || !roles.Admin {
		t.Fatalf("want user+admin, got %+v", roles)
	}
}

func TestRolesMemberNotAdmin(t *testing.T) {
	srv := fakeGitHub(t, membershipHandler(t, "active", "404", ""))
	c := newTestClient(srv.URL)
	roles, _ := c.Roles(context.Background(), "tok", "octocat")
	if !roles.User || roles.Admin {
		t.Fatalf("want user, not admin, got %+v", roles)
	}
}

func TestRolesNonMemberSkipsTeamCall(t *testing.T) {
	var teamCalled bool
	srv := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/teams/") {
			teamCalled = true
			t.Errorf("team membership should not be queried for a non-member")
		}
		http.NotFound(w, r)
	})
	c := newTestClient(srv.URL)
	roles, err := c.Roles(context.Background(), "tok", "octocat")
	if err != nil {
		t.Fatalf("roles: %v", err)
	}
	if roles.User || teamCalled {
		t.Fatalf("want no roles and no team call, got roles=%+v teamCalled=%v", roles, teamCalled)
	}
}

func TestRolesOrgMembershipPending(t *testing.T) {
	srv := fakeGitHub(t, membershipHandler(t, "pending", "404", ""))
	c := newTestClient(srv.URL)
	roles, err := c.Roles(context.Background(), "tok", "octocat")
	if err != nil {
		t.Fatalf("roles: %v", err)
	}
	if roles.User {
		t.Fatalf("pending org membership must not grant user, got %+v", roles)
	}
}
