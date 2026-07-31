package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

var appTestKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

func appTestKeyPEM(t *testing.T) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(appTestKey())}))
}

// fakeGitHubApp serves the discovery endpoints for repo acme/widgets. envs is
// the environment list; events is the App's subscribed-event list served from
// GET /app (nil serves an empty list, which existing envs-based tests never
// hit); fail makes every discovery call 500.
type fakeGitHubApp struct {
	envs   []string
	events []string
	fail   bool

	mu             sync.Mutex
	calls          int
	discoveryCalls int // calls to done-state discovery endpoints, i.e. all but GET /app
}

func (f *fakeGitHubApp) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// discoveryCount reports calls to the done-state discovery endpoints only,
// excluding the event-subscription check's GET /app — the two features run
// independently, so a test asserting "done-state discovery did not run" must
// not also be tripped by the unrelated subscription check.
func (f *fakeGitHubApp) discoveryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discoveryCalls
}

func (f *fakeGitHubApp) start(t *testing.T) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		if r.URL.Path != "/app" {
			f.discoveryCalls++
		}
		f.mu.Unlock()
		if f.fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/repos/acme/widgets/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/widgets/environments":
			envs := make([]map[string]string, 0, len(f.envs))
			for _, n := range f.envs {
				envs = append(envs, map[string]string{"name": n})
			}
			json.NewEncoder(w).Encode(map[string]any{"environments": envs})
		case "/repos/acme/widgets/releases/latest":
			http.NotFound(w, r)
		case "/app":
			json.NewEncoder(w).Encode(map[string]any{"events": f.events})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &githubauth.AppAuth{AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}
}

// addRepoServer builds a server with a project "proj" and the given discovery
// client, plus a helper that POSTs to /api/v1/projects/proj/repos.
func addRepoServer(t *testing.T, app *githubauth.AppAuth) (*store.Store, func(body map[string]any) *httptest.ResponseRecorder) {
	t.Helper()
	st := store.OpenTestStore(t)
	if err := st.CreateProject(context.Background(), "proj", "Proj", "PR"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	s := &server{st: st, cfg: Config{}, log: slog.Default(), appAuth: app}
	return st, func(body map[string]any) *httptest.ResponseRecorder {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := httptest.NewRequest("POST", "/api/v1/projects/proj/repos", bytes.NewReader(b))
		req.SetPathValue("id", "proj")
		rr := httptest.NewRecorder()
		s.addRepo(rr, req)
		return rr
	}
}

// storedDoneState returns the done_state persisted for repo, or "" if the repo
// is not mapped.
func storedDoneState(t *testing.T, st *store.Store, repo string) string {
	t.Helper()
	repos, err := st.ListRepos(context.Background(), "proj")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	for _, m := range repos {
		if m.Repo == repo {
			return m.DoneState
		}
	}
	return ""
}

func TestAddRepoDiscoversDoneState(t *testing.T) {
	f := &fakeGitHubApp{envs: []string{"dev", "production"}}
	st, post := addRepoServer(t, f.start(t))

	rr := post(map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if resp["done_state"] != "deployed_prod" {
		t.Errorf("response done_state = %v, want deployed_prod", resp["done_state"])
	}
	if got := storedDoneState(t, st, "acme/widgets"); got != "deployed_prod" {
		t.Fatalf("stored done_state = %q, want deployed_prod", got)
	}
}

// An explicit done_state is the caller's decision; discovery must not run at
// all, let alone overwrite it.
func TestAddRepoExplicitDoneStateSkipsDiscovery(t *testing.T) {
	f := &fakeGitHubApp{envs: []string{"production"}}
	st, post := addRepoServer(t, f.start(t))

	rr := post(map[string]any{"repo": "acme/widgets", "done_state": "released"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if got := storedDoneState(t, st, "acme/widgets"); got != "released" {
		t.Fatalf("stored done_state = %q, want released", got)
	}
	if n := f.discoveryCount(); n != 0 {
		t.Fatalf("done-state discovery made %d GitHub calls despite an explicit done_state", n)
	}
}

// Discovery never gates the mapping: a broken GitHub leaves the repo mapped at
// the default terminal state and the request successful.
func TestAddRepoDiscoveryFailureStillMapsRepo(t *testing.T) {
	f := &fakeGitHubApp{fail: true}
	st, post := addRepoServer(t, f.start(t))

	rr := post(map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if resp["done_state"] != store.DefaultDoneState {
		t.Errorf("response done_state = %v, want %s", resp["done_state"], store.DefaultDoneState)
	}
	if got := storedDoneState(t, st, "acme/widgets"); got != store.DefaultDoneState {
		t.Fatalf("stored done_state = %q, want %s", got, store.DefaultDoneState)
	}
	if f.count() == 0 {
		t.Error("discovery was never attempted")
	}
}

// A GitHub that never answers must not hold the addRepo response open:
// discoveryTimeout bounds the round trips. The ceiling is hardcoded so raising
// discoveryTimeout fails this test instead of moving it, and sits below
// githubauth's own 10s per-request client timeout so that outer bound cannot
// stand in for this one.
func TestAddRepoDiscoveryTimeoutBoundsHang(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	// Cleanups run LIFO: unblock the handler before Close waits on it.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	st, post := addRepoServer(t, &githubauth.AppAuth{
		AppID: "12345", Key: appTestKey(), BaseURL: srv.URL})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- post(map[string]any{"repo": "acme/widgets"}) }()

	select {
	case rr := <-done:
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
		}
		if got := storedDoneState(t, st, "acme/widgets"); got != store.DefaultDoneState {
			t.Fatalf("stored done_state = %q, want %s", got, store.DefaultDoneState)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("addRepo did not return while GitHub hung; discovery is not bounded")
	}
}

// Without an app configured, addRepo behaves exactly as before.
func TestAddRepoWithoutAppAuthKeepsDefault(t *testing.T) {
	st, post := addRepoServer(t, nil)
	rr := post(map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if got := storedDoneState(t, st, "acme/widgets"); got != store.DefaultDoneState {
		t.Fatalf("stored done_state = %q, want %s", got, store.DefaultDoneState)
	}
}

func TestNewAppAuthDisabledWhenUnconfigured(t *testing.T) {
	for name, cfg := range map[string]Config{
		"nothing set": {},
		"id only":     {GitHubAppID: "12345"},
		"key only":    {GitHubAppPrivateKey: appTestKeyPEM(t)},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := newAppAuth(cfg)
			if err != nil {
				t.Fatalf("newAppAuth: %v", err)
			}
			if got != nil {
				t.Fatal("want nil AppAuth when the app is not fully configured")
			}
		})
	}
}

func TestNewAppAuthConfigured(t *testing.T) {
	got, err := newAppAuth(Config{GitHubAppID: "12345", GitHubAppPrivateKey: appTestKeyPEM(t)})
	if err != nil {
		t.Fatalf("newAppAuth: %v", err)
	}
	if got == nil {
		t.Fatal("want an AppAuth")
	}
	if got.AppID != "12345" || got.BaseURL != githubAPIBase {
		t.Fatalf("bad AppAuth: appID=%q baseURL=%q", got.AppID, got.BaseURL)
	}
	if got.Key == nil || !got.Key.Equal(appTestKey()) {
		t.Fatal("AppAuth key does not match the configured PEM")
	}
}

// A bad key is a startup error, and the message must not echo the key itself.
func TestNewAppAuthBadKeyErrorDoesNotLeakKey(t *testing.T) {
	secret := "-----BEGIN RSA PRIVATE KEY-----\nc3VwZXItc2VjcmV0LW1hdGVyaWFs\n-----END RSA PRIVATE KEY-----\n"
	_, err := newAppAuth(Config{GitHubAppID: "12345", GitHubAppPrivateKey: secret})
	if err == nil {
		t.Fatal("want an error for an unparseable key")
	}
	if strings.Contains(err.Error(), "c3VwZXItc2VjcmV0") || strings.Contains(err.Error(), "BEGIN RSA") {
		t.Fatalf("error leaks key material: %v", err)
	}
}

// NewServer must refuse to start with an unusable app key rather than silently
// running without discovery.
func TestNewServerRejectsBadAppKey(t *testing.T) {
	st := store.OpenTestStore(t)
	_, _, err := NewServer(st, Config{GitHubAppID: "12345", GitHubAppPrivateKey: "not a pem"})
	if err == nil {
		t.Fatal("want an error from NewServer for an unusable app key")
	}
}

func TestAddRepoWarnsOnMissingEventSubscription(t *testing.T) {
	app := (&fakeGitHubApp{events: []string{"push", "pull_request"}}).start(t)
	_, post := addRepoServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — the check must never gate the mapping", rr.Code)
	}
	var got struct {
		Warnings []string `json:"warnings"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Warnings) == 0 {
		t.Fatal("no warnings; want one naming the unsubscribed events")
	}
	if !strings.Contains(got.Warnings[0], "issues") {
		t.Errorf("warning = %q, want it to name the missing issues event", got.Warnings[0])
	}
}

// A GitHub that fails the subscription check must not gate the mapping: same
// posture as discoverDoneState. No warnings is the correct, silent outcome.
func TestAddRepoSubscriptionCheckFailureStillMapsRepoNoWarnings(t *testing.T) {
	app := (&fakeGitHubApp{fail: true}).start(t)
	_, post := addRepoServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — the check must never gate the mapping", rr.Code)
	}
	var got struct {
		Warnings []string `json:"warnings"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none when GitHub fails the check", got.Warnings)
	}
}
