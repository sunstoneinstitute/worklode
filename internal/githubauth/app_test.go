package githubauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// testKey is generated once per test binary: RSA keygen is slow enough that
// per-test generation dominates the run.
var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

// appFixture is a fake GitHub App API: it serves the installation lookup, the
// token mint, and the two discovery endpoints, recording the Authorization
// header seen on each path.
type appFixture struct {
	environments []string          // names returned by GET .../environments
	envStatus    int               // 0 means 200
	releaseCode  int               // status for GET .../releases/latest (0 means 404)
	tokenCode    int               // status for the token mint (0 means 201)
	tarball      []byte            // body for GET .../tarball/<ref>
	tarballCode  int               // status for the tarball (0 means 200)
	tarballTo    string            // when set, the tarball 302s here (as codeload does)
	branchSHAs   map[string]string // branch name -> head sha for .../git/ref/heads/<branch>; unlisted branches 404

	mu      sync.Mutex
	auth    map[string]string
	calls   []string
	escaped []string
}

func (f *appFixture) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.auth == nil {
		f.auth = map[string]string{}
	}
	f.auth[r.URL.Path] = r.Header.Get("Authorization")
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.escaped = append(f.escaped, r.URL.EscapedPath())
}

// lastEscapedPath returns the still-percent-encoded path of the most recent
// request. The server decodes r.URL.Path, so only this form shows how the
// client escaped the ref.
func (f *appFixture) lastEscapedPath() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.escaped) == 0 {
		return ""
	}
	return f.escaped[len(f.escaped)-1]
}

func (f *appFixture) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *appFixture) called(method, path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == method+" "+path {
			return true
		}
	}
	return false
}

// envPageSize mirrors GitHub's pagination on the environments endpoint: 30 per
// page unless the caller asks for more.
func envPageSize(r *http.Request) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && n > 0 {
		return n
	}
	return 30
}

func (f *appFixture) start(t *testing.T) *AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		switch r.URL.Path {
		case "/repos/acme/app/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 42})
		case "/app/installations/42/access_tokens":
			if f.tokenCode != 0 {
				w.WriteHeader(f.tokenCode)
				json.NewEncoder(w).Encode(map[string]any{"message": "Bad credentials"})
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/app/environments":
			if f.envStatus != 0 {
				w.WriteHeader(f.envStatus)
				return
			}
			names := f.environments
			if page := envPageSize(r); len(names) > page {
				names = names[:page]
			}
			envs := make([]map[string]string, 0, len(names))
			for _, n := range names {
				envs = append(envs, map[string]string{"name": n})
			}
			json.NewEncoder(w).Encode(map[string]any{"environments": envs})
		case "/repos/acme/app/releases/latest":
			code := f.releaseCode
			if code == 0 {
				code = http.StatusNotFound
			}
			w.WriteHeader(code)
			if code == http.StatusOK {
				json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1"})
			}
		default:
			// The branch ref is matched by prefix so a test can pick any
			// branch name and the fixture 404s ones it wasn't told about.
			if strings.HasPrefix(r.URL.Path, "/repos/acme/app/git/ref/heads/") {
				branch := strings.TrimPrefix(r.URL.Path, "/repos/acme/app/git/ref/heads/")
				sha, ok := f.branchSHAs[branch]
				if !ok {
					http.NotFound(w, r)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{
					"ref":    "refs/heads/" + branch,
					"object": map[string]any{"sha": sha, "type": "commit"},
				})
				return
			}
			// The tarball ref is matched by prefix so a test can pick any ref
			// and inspect how it was escaped.
			if strings.HasPrefix(r.URL.Path, "/repos/acme/app/tarball/") {
				if f.tarballTo != "" {
					http.Redirect(w, r, f.tarballTo, http.StatusFound)
					return
				}
				if f.tarballCode != 0 {
					w.WriteHeader(f.tarballCode)
					w.Write([]byte("No commit found for the ref main"))
					return
				}
				w.Write(f.tarball)
				return
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &AppAuth{AppID: "12345", Key: testKey(), BaseURL: srv.URL}
}

// assertAppJWT checks that path was authenticated with an RS256 JWT signed by
// the app key and issued by the app id.
func (f *appFixture) assertAppJWT(t *testing.T, path string) {
	t.Helper()
	f.mu.Lock()
	got := f.auth[path]
	f.mu.Unlock()
	raw, ok := strings.CutPrefix(got, "Bearer ")
	if !ok || raw == "" {
		t.Fatalf("%s Authorization = %q, want a Bearer JWT", path, got)
	}
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatalf("%s: parse app jwt: %v", path, err)
	}
	var cl jwt.Claims
	if err := tok.Claims(&testKey().PublicKey, &cl); err != nil {
		t.Fatalf("%s: app jwt not signed by the app key: %v", path, err)
	}
	if cl.Issuer != "12345" {
		t.Errorf("%s: app jwt issuer = %q, want 12345", path, cl.Issuer)
	}
	if cl.Expiry == nil || cl.IssuedAt == nil {
		t.Fatalf("%s: app jwt missing iat/exp: %+v", path, cl)
	}
	if d := cl.Expiry.Time().Sub(time.Now()); d <= 0 || d > 10*time.Minute {
		t.Errorf("%s: app jwt expires in %v, want (0, 10m]", path, d)
	}
	// iat is backdated to tolerate clock skew; without it GitHub rejects the
	// assertion whenever our clock runs ahead of theirs.
	if age := time.Since(cl.IssuedAt.Time()); age < time.Minute {
		t.Errorf("%s: app jwt iat is %v old, want backdated at least a minute", path, age)
	}
}

func (f *appFixture) assertTokenAuth(t *testing.T, path string) {
	t.Helper()
	f.mu.Lock()
	got := f.auth[path]
	f.mu.Unlock()
	if got != "Bearer ghs_test" {
		t.Errorf("%s Authorization = %q, want the installation token", path, got)
	}
}

func TestDiscoverDoneStateProdEnvironment(t *testing.T) {
	f := &appFixture{environments: []string{"dev", "prod", "copilot"}}
	a := f.start(t)

	got, err := a.DiscoverDoneState(context.Background(), "acme/app")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got != "deployed_prod" {
		t.Fatalf("done_state = %q, want deployed_prod", got)
	}
	f.assertAppJWT(t, "/repos/acme/app/installation")
	f.assertAppJWT(t, "/app/installations/42/access_tokens")
	f.assertTokenAuth(t, "/repos/acme/app/environments")
	if f.called("GET", "/repos/acme/app/releases/latest") {
		t.Error("releases were queried even though a prod environment exists")
	}
}

func TestDiscoverDoneStateProdAlias(t *testing.T) {
	for _, name := range []string{"Production", "PROD", "production"} {
		t.Run(name, func(t *testing.T) {
			f := &appFixture{environments: []string{"lint", name}}
			got, err := f.start(t).DiscoverDoneState(context.Background(), "acme/app")
			if err != nil {
				t.Fatalf("discover: %v", err)
			}
			if got != "deployed_prod" {
				t.Fatalf("done_state for environment %q = %q, want deployed_prod", name, got)
			}
		})
	}
}

// GitHub returns 30 environments per page by default. A prod environment past
// that first page must still be found: a miss seeds the wrong done_state, and
// there is no re-discovery path to correct it.
func TestDiscoverDoneStateProdBeyondFirstPage(t *testing.T) {
	envs := make([]string, 0, 31)
	for i := 0; i < 30; i++ {
		envs = append(envs, fmt.Sprintf("env-%d", i))
	}
	envs = append(envs, "prod")

	f := &appFixture{environments: envs}
	got, err := f.start(t).DiscoverDoneState(context.Background(), "acme/app")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got != "deployed_prod" {
		t.Fatalf("done_state = %q, want deployed_prod (prod is environment 31)", got)
	}
}

func TestDiscoverDoneStateReleased(t *testing.T) {
	f := &appFixture{releaseCode: http.StatusOK}
	got, err := f.start(t).DiscoverDoneState(context.Background(), "acme/app")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got != "released" {
		t.Fatalf("done_state = %q, want released", got)
	}
	f.assertTokenAuth(t, "/repos/acme/app/releases/latest")
}

func TestDiscoverDoneStateMerged(t *testing.T) {
	f := &appFixture{}
	got, err := f.start(t).DiscoverDoneState(context.Background(), "acme/app")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got != "merged" {
		t.Fatalf("done_state = %q, want merged", got)
	}
}

// Environments that name no delivery stage must not be read as prod; the repo
// falls through to the release check.
func TestDiscoverDoneStateIgnoresNonDeliveryEnvironments(t *testing.T) {
	f := &appFixture{environments: []string{"copilot", "github-pages", "pypi", "dev"}}
	got, err := f.start(t).DiscoverDoneState(context.Background(), "acme/app")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got != "merged" {
		t.Fatalf("done_state = %q, want merged", got)
	}
	if !f.called("GET", "/repos/acme/app/releases/latest") {
		t.Error("releases were not queried after finding no prod environment")
	}
}

// A broken releases endpoint is an error, not evidence that the repo has no
// releases: reading a 500 as "merged" would silently mis-seed the mapping.
func TestDiscoverDoneStateReleasesServerError(t *testing.T) {
	f := &appFixture{releaseCode: http.StatusInternalServerError}
	got, err := f.start(t).DiscoverDoneState(context.Background(), "acme/app")
	if err == nil {
		t.Fatalf("want error for a 500 from releases/latest, got %q", got)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should name the status: %v", err)
	}
}

func TestDiscoverDoneStateEnvironmentsError(t *testing.T) {
	f := &appFixture{envStatus: http.StatusForbidden}
	if got, err := f.start(t).DiscoverDoneState(context.Background(), "acme/app"); err == nil {
		t.Fatalf("want error for a 403 from environments, got %q", got)
	}
}

// A failed token mint must report the status, not decode the error body into
// an empty token.
func TestInstallationTokenRejectsErrorStatus(t *testing.T) {
	f := &appFixture{tokenCode: http.StatusUnauthorized}
	_, err := f.start(t).InstallationToken(context.Background(), "acme/app")
	if err == nil {
		t.Fatal("want error for a 401 from the token mint")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should name the status: %v", err)
	}
}

func TestInstallationTokenUnknownRepo(t *testing.T) {
	f := &appFixture{}
	if _, err := f.start(t).InstallationToken(context.Background(), "acme/nosuch"); err == nil {
		t.Fatal("want error when the repo has no installation")
	}
}

func TestBranchSHA(t *testing.T) {
	f := &appFixture{branchSHAs: map[string]string{
		"release-1.2": "abc1230000000000000000000000000000000000",
	}}
	a := f.start(t)

	sha, err := a.BranchSHA(context.Background(), "acme/app", "release-1.2")
	if err != nil {
		t.Fatalf("BranchSHA: %v", err)
	}
	if sha != "abc1230000000000000000000000000000000000" {
		t.Fatalf("sha = %q", sha)
	}
}

// The branch is escaped whole, so a slashed release branch reaches GitHub as
// one %2F segment — which it resolves — and a ref like "../../x" stays inert
// instead of emitting dot segments a server would normalize out of the path.
// Same rule as TestTarballEscapesRefAsOneSegment.
func TestBranchSHAEscapesBranchAsOneSegment(t *testing.T) {
	for _, tc := range []struct{ branch, want string }{
		{"release/1.2", "/repos/acme/app/git/ref/heads/release%2F1.2"},
		{"../../x", "/repos/acme/app/git/ref/heads/..%2F..%2Fx"},
	} {
		t.Run(tc.branch, func(t *testing.T) {
			f := &appFixture{branchSHAs: map[string]string{
				tc.branch: "abc1230000000000000000000000000000000000",
			}}
			sha, err := f.start(t).BranchSHA(context.Background(), "acme/app", tc.branch)
			if err != nil {
				t.Fatalf("BranchSHA: %v", err)
			}
			if sha != "abc1230000000000000000000000000000000000" {
				t.Fatalf("sha = %q, want the fixture's head", sha)
			}
			if got := f.lastEscapedPath(); got != tc.want {
				t.Errorf("requested %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBranchSHAUnknownBranchIsEmpty(t *testing.T) {
	f := &appFixture{} // no branches registered, so every ref 404s
	a := f.start(t)

	sha, err := a.BranchSHA(context.Background(), "acme/app", "nope")
	if err != nil {
		t.Fatalf("BranchSHA: %v", err)
	}
	if sha != "" {
		t.Fatalf("sha = %q, want empty for a 404", sha)
	}
}

// A mapping that is not "owner/name" is rejected before it can reshape a
// request URL, so no call reaches GitHub at all.
func TestDiscoverDoneStateRejectsMalformedRepo(t *testing.T) {
	for _, repo := range []string{"", "app", "acme/app/extra", "/app", "acme/", "acme/../../x"} {
		t.Run(repo, func(t *testing.T) {
			f := &appFixture{}
			_, err := f.start(t).DiscoverDoneState(context.Background(), repo)
			if err == nil {
				t.Fatalf("DiscoverDoneState(%q) = nil error, want a rejection", repo)
			}
			if !strings.Contains(err.Error(), "owner/name") {
				t.Errorf("error should name the expected shape: %v", err)
			}
			if n := f.callCount(); n != 0 {
				t.Errorf("%d request(s) reached GitHub for a malformed repo", n)
			}
		})
	}
}

func TestParseAppPrivateKey(t *testing.T) {
	key := testKey()
	pkcs1 := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes})

	for name, data := range map[string][]byte{"pkcs1": pkcs1, "pkcs8": pkcs8} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseAppPrivateKey(data)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !got.Equal(key) {
				t.Fatal("parsed key differs from the original")
			}
		})
	}

	for name, data := range map[string][]byte{
		"not pem":   []byte("hunter2"),
		"empty":     nil,
		"bad block": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("garbage")}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAppPrivateKey(data); err == nil {
				t.Fatal("want error")
			}
		})
	}
}
