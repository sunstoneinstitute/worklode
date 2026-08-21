package derive_test

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

// TestGitHubReaderMapsNotFoundAndCachesClient exercises GitHubReader against
// a real httptest server built the way AppAuth's own tests build one
// (newFakeGitHub is unexported to internal/githubauth, so this constructs
// its own): confirms githubauth.ErrContentNotFound surfaces as
// derive.ErrNotFound, and that a second call for the same repo reuses the
// cached RepoClient instead of minting a fresh installation token.
func TestGitHubReaderMapsNotFoundAndCachesClient(t *testing.T) {
	var installLookups int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/app/installation":
			atomic.AddInt32(&installLookups, 1)
			io.WriteString(w, `{"id": 42}`)
		case "/app/installations/42/access_tokens":
			io.WriteString(w, `{"token": "inst-token"}`)
		case "/repos/acme/app/contents/missing.yaml":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"not found"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"not found"}`)
		}
	}))
	defer srv.Close()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	auth := &githubauth.AppAuth{AppID: "1", Key: key, BaseURL: srv.URL}
	gr := &derive.GitHubReader{Auth: auth}

	if _, err := gr.FileAt(t.Context(), "acme/app", "missing.yaml"); !errors.Is(err, derive.ErrNotFound) {
		t.Fatalf("FileAt error = %v, want derive.ErrNotFound", err)
	}
	if _, err := gr.FileAt(t.Context(), "acme/app", "missing.yaml"); !errors.Is(err, derive.ErrNotFound) {
		t.Fatalf("FileAt (2nd call) error = %v, want derive.ErrNotFound", err)
	}
	if got := atomic.LoadInt32(&installLookups); got != 1 {
		t.Fatalf("installation lookups = %d, want 1 — the RepoClient must be cached per repo", got)
	}
}
