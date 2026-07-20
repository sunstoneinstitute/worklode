package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sunstoneinstitute/work-tracker/internal/githubauth"
	"github.com/sunstoneinstitute/work-tracker/internal/store"
	"github.com/sunstoneinstitute/work-tracker/internal/tokencrypt"
)

// newGitHubTestStore opens a fresh migrated store in a temp dir. store.Open runs
// migrations internally, so no separate Migrate() call is needed.
func newGitHubTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestProvisionGitHubActorNamespacesID(t *testing.T) {
	st := newGitHubTestStore(t)
	s := &server{st: st, cfg: Config{}}
	id, err := s.provisionGitHubActor(context.Background(),
		&githubauth.Identity{ID: 42, Login: "octocat", Name: "The Octocat"},
		githubauth.Roles{User: true, Admin: true})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if id != "github:42" {
		t.Fatalf("want github:42, got %s", id)
	}
	a, err := st.GetActor(context.Background(), "github:42")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.Kind != "human" || !a.Admin || a.DisplayName != "octocat" {
		t.Fatalf("bad actor: %+v", a)
	}
}

func TestProvisionGitHubActorRejectsNonMember(t *testing.T) {
	st := newGitHubTestStore(t)
	s := &server{st: st, cfg: Config{}}
	_, err := s.provisionGitHubActor(context.Background(),
		&githubauth.Identity{ID: 7, Login: "stranger"},
		githubauth.Roles{User: false})
	if err != errNoUserRole {
		t.Fatalf("want errNoUserRole, got %v", err)
	}
}

func TestGitHubLoginRedirects(t *testing.T) {
	st := newGitHubTestStore(t)
	s := &server{st: st, log: slog.Default(), cfg: Config{PublicURL: "https://wt.test", SessionSecret: "sekret"}}
	s.gh = githubauth.New("cid", "secret", "sunstoneinstitute", "work-tracker-admins")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/github/login", nil)
	s.githubLogin(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("code=%d", rr.Code)
	}
	loc, _ := url.Parse(rr.Header().Get("Location"))
	if !strings.Contains(loc.String(), "client_id=cid") || loc.Query().Get("state") == "" {
		t.Fatalf("bad redirect: %s", loc)
	}
	if len(rr.Result().Cookies()) == 0 {
		t.Fatal("expected oauth-state cookie")
	}
}

func TestGitHubLogin404WhenDisabled(t *testing.T) {
	s := &server{cfg: Config{}}
	rr := httptest.NewRecorder()
	s.githubLogin(rr, httptest.NewRequest("GET", "/auth/github/login", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code=%d", rr.Code)
	}
}

// fakeGitHub serves the token, identity, and membership endpoints the callback
// drives. The admin-team membership 404s, so octocat is a user but not admin.
func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
			w.Write([]byte("access_token=gho_x&token_type=bearer"))
		case "/user":
			json.NewEncoder(w).Encode(map[string]any{"id": 42, "login": "octocat", "name": "The Octocat"})
		case "/user/memberships/orgs/sunstoneinstitute":
			json.NewEncoder(w).Encode(map[string]any{"state": "active"})
		case "/orgs/sunstoneinstitute/teams/work-tracker-admins/memberships/octocat":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubCallbackSetsSessionAndStoresToken(t *testing.T) {
	fake := fakeGitHub(t)

	tc, err := tokencrypt.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	st := newGitHubTestStore(t)
	s := &server{
		st:          st,
		log:         slog.Default(),
		cfg:         Config{PublicURL: "https://wt.test", SessionSecret: "sekret"},
		tokenCipher: tc,
	}
	s.gh = githubauth.New("cid", "secret", "sunstoneinstitute", "work-tracker-admins")
	s.gh.APIBase = fake.URL
	s.gh.Endpoint = oauth2.Endpoint{
		AuthURL:  fake.URL + "/login/oauth/authorize",
		TokenURL: fake.URL + "/login/oauth/access_token",
	}

	const state = "xyz"
	cookie := signOAuthState(s.cfg.SessionSecret, oauthState{
		State: state, Next: "/tasks/WT-1", Exp: s.st.Now().Add(oauthStateMaxAge).Unix(),
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/github/callback?state="+state+"&code=abc", nil)
	req.AddCookie(&http.Cookie{Name: oauthCookieName, Value: cookie})
	s.githubCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("code=%d, body=%s", rr.Code, rr.Body.String())
	}
	var sessionSet bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			sessionSet = true
		}
	}
	if !sessionSet {
		t.Fatal("expected wt_session cookie to be set")
	}

	ct, err := st.GetGitHubUserToken(context.Background(), "github:42")
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	raw, err := s.tokenCipher.Open(ct)
	if err != nil {
		t.Fatalf("open token: %v", err)
	}
	if !strings.Contains(string(raw), "gho_x") {
		t.Fatalf("decrypted payload missing access token: %s", raw)
	}
}
