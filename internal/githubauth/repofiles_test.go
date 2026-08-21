package githubauth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

// recordedReq is one request a fakeRepoFileServer observed, capturing enough
// to assert path, query parameters and the installation-token Authorization
// header together.
type recordedReq struct {
	path  string
	query url.Values
	auth  string
}

// repoFileRecorder collects requests in arrival order.
type repoFileRecorder struct {
	mu   sync.Mutex
	reqs []recordedReq
}

func (r *repoFileRecorder) record(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, recordedReq{path: req.URL.Path, query: req.URL.Query(), auth: req.Header.Get("Authorization")})
}

func (r *repoFileRecorder) all() []recordedReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedReq(nil), r.reqs...)
}

func (r *repoFileRecorder) last() recordedReq {
	all := r.all()
	return all[len(all)-1]
}

// newRepoFileClient starts a fake GitHub serving the app-auth routes plus
// routes, keyed by exact URL path, and returns a RepoClient bound to
// "acme/app" plus the recorder every request lands in. Mirrors
// newFakeGitHub (repoclient_test.go) but also records the Authorization
// header and query string, which FileAt/PRFiles tests need to assert on.
func newRepoFileClient(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) (*githubauth.RepoClient, *repoFileRecorder) {
	t.Helper()
	rec := &repoFileRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/app/installation":
			io.WriteString(w, `{"id": 42}`)
		case "/app/installations/42/access_tokens":
			io.WriteString(w, `{"token": "inst-token"}`)
		default:
			if h, ok := routes[r.URL.Path]; ok {
				h(w, r)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"not found"}`)
		}
	}))
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	app := &githubauth.AppAuth{AppID: "1", Key: key, BaseURL: srv.URL}
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	return rc, rec
}

// wrapBase64 mimics GitHub's contents API, which wraps the base64 payload at
// 60 characters — the exact wrapping FileAt must tolerate.
func wrapBase64(s string, width int) string {
	var b strings.Builder
	for i := 0; i < len(s); i += width {
		end := i + width
		if end > len(s) {
			end = len(s)
		}
		b.WriteString(s[i:end])
		b.WriteByte('\n')
	}
	return b.String()
}

func TestFileAtDecodesWrappedBase64AndSendsAuth(t *testing.T) {
	raw := []byte("repo: github.com/acme/app\ncomponents: []\n")
	wrapped := wrapBase64(base64.StdEncoding.EncodeToString(raw), 60)
	rc, rec := newRepoFileClient(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/repos/acme/app/contents/.worklode/components.yaml": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"content": %q, "encoding": "base64"}`, wrapped)
		},
	})
	got, err := rc.FileAt(t.Context(), ".worklode/components.yaml")
	if err != nil {
		t.Fatalf("FileAt: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("FileAt = %q, want %q", got, raw)
	}
	if auth := rec.last().auth; auth != "Bearer inst-token" {
		t.Fatalf("Authorization = %q, want %q", auth, "Bearer inst-token")
	}
}

func TestFileAtNotFound(t *testing.T) {
	rc, _ := newRepoFileClient(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/repos/acme/app/contents/missing.yaml": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"not found"}`)
		},
	})
	_, err := rc.FileAt(t.Context(), "missing.yaml")
	if !errors.Is(err, githubauth.ErrContentNotFound) {
		t.Fatalf("FileAt error = %v, want ErrContentNotFound", err)
	}
}

func TestFileAtEncodingNone(t *testing.T) {
	rc, _ := newRepoFileClient(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/repos/acme/app/contents/big.bin": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"content": "", "encoding": "none"}`)
		},
	})
	_, err := rc.FileAt(t.Context(), "big.bin")
	if err == nil {
		t.Fatal("FileAt = nil error, want an error for encoding=none (file too large for the contents API)")
	}
	if errors.Is(err, githubauth.ErrContentNotFound) {
		t.Fatalf("FileAt encoding=none error = %v, must not be ErrContentNotFound", err)
	}
}

func TestPRFilesPaginatesUntilShortPage(t *testing.T) {
	page1 := make([]string, 100)
	for i := range page1 {
		page1[i] = fmt.Sprintf(`{"filename":"f%d.go"}`, i)
	}
	rc, rec := newRepoFileClient(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/repos/acme/app/pulls/7/files": func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("page") {
			case "1":
				fmt.Fprintf(w, "[%s]", strings.Join(page1, ","))
			case "2":
				io.WriteString(w, `[{"filename":"last.go"}]`)
			default:
				io.WriteString(w, `[]`)
			}
		},
	})
	files, err := rc.PRFiles(t.Context(), 7)
	if err != nil {
		t.Fatalf("PRFiles: %v", err)
	}
	if len(files) != 101 {
		t.Fatalf("len(files) = %d, want 101", len(files))
	}
	if files[100] != "last.go" {
		t.Fatalf("files[100] = %q, want %q", files[100], "last.go")
	}

	var pages, perPages []string
	for _, r := range rec.all() {
		if r.path == "/repos/acme/app/pulls/7/files" {
			pages = append(pages, r.query.Get("page"))
			perPages = append(perPages, r.query.Get("per_page"))
		}
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("pages requested = %v, want [1 2] — pagination must stop at the short page", pages)
	}
	for _, pp := range perPages {
		if pp != "100" {
			t.Fatalf("per_page = %q, want 100", pp)
		}
	}
}
