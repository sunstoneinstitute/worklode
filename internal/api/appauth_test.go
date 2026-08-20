package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/model"
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

// installationProbe is a GitHub stand-in for the doctor's App-install check:
// it serves GET /repos/{owner}/{name}/installation after delay, and records
// how many calls it saw and how many were in flight at once.
type installationProbe struct {
	delay    time.Duration
	notFound map[string]bool // repos the App is "not installed" on

	mu       sync.Mutex
	calls    int
	paths    []string
	inFlight int
	peak     int
}

func (p *installationProbe) enter(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.paths = append(p.paths, path)
	p.inFlight++
	if p.inFlight > p.peak {
		p.peak = p.inFlight
	}
}

func (p *installationProbe) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight--
}

func (p *installationProbe) start(t *testing.T) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.enter(r.URL.Path)
		defer p.leave()
		if p.delay > 0 {
			select {
			case <-time.After(p.delay):
			case <-r.Context().Done():
				return
			}
		}
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/repos/"), "/installation")
		if p.notFound[repo] {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 7})
	}))
	t.Cleanup(srv.Close)
	return &githubauth.AppAuth{AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}
}

// doctorServer maps repos under project "proj" and returns a helper that runs
// GET /api/v1/repos/doctor against a server holding the given App auth.
func doctorServer(t *testing.T, app *githubauth.AppAuth, repos []string) func() model.ReposDoctorResponse {
	t.Helper()
	ctx := context.Background()
	st := store.OpenTestStore(t)
	if err := st.CreateProject(ctx, "proj", "Proj", "PR"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	for _, repo := range repos {
		if err := st.AddRepo(ctx, "proj", repo); err != nil {
			t.Fatalf("map %s: %v", repo, err)
		}
	}
	s := &server{st: st, cfg: Config{}, log: slog.Default(), appAuth: app}
	return func() model.ReposDoctorResponse {
		t.Helper()
		rr := httptest.NewRecorder()
		s.reposDoctor(rr, httptest.NewRequest("GET", "/api/v1/repos/doctor", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("repos doctor: status %d, body %s", rr.Code, rr.Body.String())
		}
		var resp model.ReposDoctorResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %q: %v", rr.Body.String(), err)
		}
		return resp
	}
}

func doctorRepoNames(n int) []string {
	repos := make([]string, n)
	for i := range repos {
		repos[i] = fmt.Sprintf("acme/repo%02d", i)
	}
	return repos
}

// The App-install check costs one GitHub call per repo — not two — and the
// calls run concurrently under appCheckConcurrency, so wall clock tracks the
// number of waves rather than the number of repos. Sequentially these would
// take repos×delay; the ceiling asserts they did not.
func TestReposDoctorAppCheckIsConcurrentAndSingleCall(t *testing.T) {
	const repos = 24
	const delay = 100 * time.Millisecond
	probe := &installationProbe{delay: delay}
	doctor := doctorServer(t, probe.start(t), doctorRepoNames(repos))

	start := time.Now()
	resp := doctor()
	elapsed := time.Since(start)

	if len(resp.Repos) != repos {
		t.Fatalf("reported %d repos, want %d", len(resp.Repos), repos)
	}
	for _, r := range resp.Repos {
		if r.AppInstalled == nil || !*r.AppInstalled {
			t.Fatalf("%s: app_installed = %v (%s), want true", r.Repo, r.AppInstalled, r.AppError)
		}
	}

	probe.mu.Lock()
	calls, peak, paths := probe.calls, probe.peak, probe.paths
	probe.mu.Unlock()

	if calls != repos {
		t.Errorf("GitHub calls = %d, want %d (one per repo — no token mint)", calls, repos)
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "/installation") {
			t.Errorf("unexpected GitHub call %q; the check needs only the installation lookup", p)
		}
	}
	if peak < 2 {
		t.Errorf("peak in-flight calls = %d; the checks ran sequentially", peak)
	}
	if peak > appCheckConcurrency {
		t.Errorf("peak in-flight calls = %d, want <= %d; the fan-out is unbounded", peak, appCheckConcurrency)
	}
	// Sequential would be repos×delay = 2.4s; the bounded fan-out needs
	// ceil(24/8)=3 waves ≈ 300ms. Half the sequential cost is a ceiling loose
	// enough for a loaded CI box and still failing on a serial regression.
	if ceiling := repos * delay / 2; elapsed > ceiling {
		t.Errorf("doctor took %s, want < %s; wall clock is growing with repo count", elapsed, ceiling)
	}
}

// GitHub's 404 is the one answer that means "not installed"; anything else
// leaves the question open and must report unchecked, not absent.
func TestReposDoctorAppCheckDistinguishesNotInstalledFromUnchecked(t *testing.T) {
	probe := &installationProbe{notFound: map[string]bool{"acme/missing": true}}
	doctor := doctorServer(t, probe.start(t), []string{"acme/ok", "acme/missing"})

	byRepo := map[string]model.RepoDoctor{}
	for _, r := range doctor().Repos {
		byRepo[r.Repo] = r
	}
	if r := byRepo["acme/ok"]; r.AppInstalled == nil || !*r.AppInstalled {
		t.Errorf("acme/ok: app_installed = %v, want true", r.AppInstalled)
	}
	r := byRepo["acme/missing"]
	if r.AppInstalled == nil || *r.AppInstalled {
		t.Fatalf("acme/missing: app_installed = %v, want false — GitHub answered 404", r.AppInstalled)
	}
	if r.AppError == "" {
		t.Error("acme/missing: app_error is empty; the report must say why it is not installed")
	}
}

// A GitHub that never answers must not hold the doctor response open for
// repo-count × per-call timeout: the whole check phase shares one budget, and
// repos it does not reach report unchecked (nil) rather than not-installed.
func TestReposDoctorAppCheckBudgetBoundsHang(t *testing.T) {
	prev := appCheckBudget
	appCheckBudget = 300 * time.Millisecond
	t.Cleanup(func() { appCheckBudget = prev })

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

	doctor := doctorServer(t, &githubauth.AppAuth{
		AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}, doctorRepoNames(40))

	done := make(chan model.ReposDoctorResponse, 1)
	go func() { done <- doctor() }()

	select {
	case resp := <-done:
		if len(resp.Repos) != 40 {
			t.Fatalf("reported %d repos, want 40 — a stuck check must not drop repos", len(resp.Repos))
		}
		for _, r := range resp.Repos {
			if r.AppInstalled != nil {
				t.Fatalf("%s: app_installed = %v, want null — the check never got an answer",
					r.Repo, *r.AppInstalled)
			}
			if r.AppError == "" {
				t.Fatalf("%s: app_error is empty; unchecked must say why", r.Repo)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("repos doctor did not return while GitHub hung; the App check is not bounded")
	}
}
