package storederive_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/storederive"
)

// importsManifest mirrors internal/derive/imports_test.go's fixture — the
// manifest shape GitHubReader's callers parse.
const importsManifest = `
repo: github.com/sunstoneinstitute/research-stack
components:
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest
    name: ingest
    paths: ["cmd/ingest/**", "internal/ingest/**"]
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/graphsrv
    name: graphsrv
    paths: ["cmd/graph-server/**", "internal/graph/**"]
`

// fakeGitHubApp starts an httptest server serving the app-auth routes for
// every repo in installed, plus the caller's own routes, and returns an
// AppAuth pointed at it together with a counter of installation lookups
// (one per minted RepoClient). A repo absent from installed 404s its
// /installation route, which is exactly how GitHub reports "the App is not
// installed here". newFakeGitHub is unexported to internal/githubauth, so
// the derive tests build their own.
func fakeGitHubApp(t *testing.T, installed map[string]bool, routes map[string]http.HandlerFunc) (*githubauth.AppAuth, *int32) {
	t.Helper()
	var installLookups int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if h, ok := routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		for repo := range installed {
			if r.URL.Path == "/repos/"+repo+"/installation" {
				atomic.AddInt32(&installLookups, 1)
				io.WriteString(w, `{"id": 42}`)
				return
			}
		}
		if r.URL.Path == "/app/installations/42/access_tokens" {
			io.WriteString(w, `{"token": "inst-token"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"not found"}`)
	}))
	t.Cleanup(srv.Close)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &githubauth.AppAuth{AppID: "1", Key: key, BaseURL: srv.URL}, &installLookups
}

// TestGitHubReaderMapsNotFoundAndCachesClient confirms
// githubauth.ErrContentNotFound surfaces as derive.ErrNotFound, and that a
// second call for the same repo inside the freshness window reuses the
// cached RepoClient instead of minting a fresh installation token.
func TestGitHubReaderMapsNotFoundAndCachesClient(t *testing.T) {
	auth, installLookups := fakeGitHubApp(t, map[string]bool{"acme/app": true}, nil)
	gr := &storederive.GitHubReader{Auth: auth}

	if _, err := gr.FileAt(t.Context(), "acme/app", "missing.yaml"); !errors.Is(err, derive.ErrNotFound) {
		t.Fatalf("FileAt error = %v, want derive.ErrNotFound", err)
	}
	if _, err := gr.FileAt(t.Context(), "acme/app", "missing.yaml"); !errors.Is(err, derive.ErrNotFound) {
		t.Fatalf("FileAt (2nd call) error = %v, want derive.ErrNotFound", err)
	}
	if got := atomic.LoadInt32(installLookups); got != 1 {
		t.Fatalf("installation lookups = %d, want 1 — the RepoClient must be cached per repo", got)
	}
}

// TestGitHubReaderRemintsExpiredClient pins the cache's freshness window: the
// installation token NewRepoClient bakes into the client expires after an
// hour, so an entry older than storederive.RepoClientTTL must be re-minted rather
// than reused forever by a GitHubReader that lives as long as the server.
func TestGitHubReaderRemintsExpiredClient(t *testing.T) {
	auth, installLookups := fakeGitHubApp(t, map[string]bool{"acme/app": true}, nil)

	base := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := base
	gr := &storederive.GitHubReader{Auth: auth}
	gr.SetClock(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	read := func(step string) {
		t.Helper()
		if _, err := gr.FileAt(t.Context(), "acme/app", "missing.yaml"); !errors.Is(err, derive.ErrNotFound) {
			t.Fatalf("FileAt (%s) error = %v, want derive.ErrNotFound", step, err)
		}
	}

	read("first")
	// Just inside the window: the cached client is still good.
	advance(storederive.RepoClientTTL - time.Minute)
	read("inside the window")
	if got := atomic.LoadInt32(installLookups); got != 1 {
		t.Fatalf("installation lookups inside the window = %d, want 1", got)
	}
	// Past it: the token is close enough to its one-hour expiry to re-mint.
	advance(2 * time.Minute)
	read("past the window")
	if got := atomic.LoadInt32(installLookups); got != 2 {
		t.Fatalf("installation lookups past the window = %d, want 2 — a stale client must be re-minted", got)
	}
}

// TestGitHubReaderConcurrentSameRepo runs the race detector over the shared
// cache: `lode serve` builds one GitHubReader at boot and captures it in a
// long-lived handler closure, so overlapping requests write g.clients at the
// same time. Unguarded that is a fatal concurrent map write, which no HTTP
// recover middleware catches.
func TestGitHubReaderConcurrentSameRepo(t *testing.T) {
	auth, _ := fakeGitHubApp(t, map[string]bool{"acme/app": true, "acme/other": true}, nil)
	gr := &storederive.GitHubReader{Auth: auth}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		for _, repo := range []string{"acme/app", "acme/other"} {
			wg.Add(1)
			go func(repo string) {
				defer wg.Done()
				if _, err := gr.FileAt(context.Background(), repo, "missing.yaml"); !errors.Is(err, derive.ErrNotFound) {
					t.Errorf("FileAt(%s) error = %v, want derive.ErrNotFound", repo, err)
				}
			}(repo)
		}
	}
	wg.Wait()
}

// TestPRAffectsSkipsUninstalledRepo is the end-to-end shape of the mapping:
// GitHubReader turns githubauth.ErrAppNotInstalled into derive.ErrNotFound,
// so PRAffectsTriples reports the repo in skippedRepos instead of aborting
// the org-global run for every other repo. Before the mapping, one repo the
// App had been uninstalled from produced no document at all.
func TestPRAffectsSkipsUninstalledRepo(t *testing.T) {
	const repo = "sunstoneinstitute/research-stack"
	auth, _ := fakeGitHubApp(t, map[string]bool{repo: true}, map[string]http.HandlerFunc{
		"/repos/" + repo + "/contents/.worklode/components.yaml": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"content": "`+b64(importsManifest)+`", "encoding": "base64"}`)
		},
		"/repos/" + repo + "/pulls/1/files": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[{"filename":"internal/ingest/x.go"}]`)
		},
	})
	gr := &storederive.GitHubReader{Auth: auth}

	prs := []derive.PRRef{
		{Repo: repo, Number: 1, TaskID: "WL-7"},
		// acme/uninstalled has no /installation route: GitHub 404s it, which
		// githubauth reports as ErrAppNotInstalled.
		{Repo: "acme/uninstalled", Number: 2, TaskID: "WL-8"},
	}
	doc, skipped, err := derive.PRAffectsTriples(t.Context(), prs, gr)
	if err != nil {
		t.Fatalf("PRAffectsTriples: %v — a repo the App is not installed on must skip, not fail the run", err)
	}
	if len(skipped) != 1 || skipped[0] != "acme/uninstalled" {
		t.Fatalf("skipped = %v, want the uninstalled repo reported", skipped)
	}
	if got := string(doc); !strings.Contains(got, "WL-7") || strings.Contains(got, "WL-8") {
		t.Fatalf("doc = %q; want the installed repo's edge and nothing from the uninstalled one", got)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
