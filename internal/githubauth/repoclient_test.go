package githubauth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

// newFakeGitHub serves the app-auth routes plus per-path canned JSON bodies.
func newFakeGitHub(t *testing.T, routes map[string]string) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/app/installation":
			io.WriteString(w, `{"id": 42}`)
		case "/app/installations/42/access_tokens":
			io.WriteString(w, `{"token": "inst-token"}`)
		default:
			body, ok := routes[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"message":"not found"}`)
				return
			}
			io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &githubauth.AppAuth{AppID: "1", Key: key, BaseURL: srv.URL}
}

func TestRepoClientPR(t *testing.T) {
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app/pulls/12": `{
			"number": 12, "title": "fix", "state": "closed", "merged": true,
			"body": "", "html_url": "u",
			"merge_commit_sha": "2222222222222222222222222222222222222222",
			"merged_at": "2026-07-20T10:00:00Z",
			"created_at": "2026-07-19T09:00:00Z",
			"head": {"ref": "lode/WL-1-fix", "sha": "1111111111111111111111111111111111111111"}
		}`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	pr, err := rc.PR(t.Context(), 12)
	if err != nil {
		t.Fatalf("PR: %v", err)
	}
	if !pr.Merged || pr.MergeCommitSHA == nil || *pr.MergeCommitSHA != "2222222222222222222222222222222222222222" {
		t.Fatalf("PR = %+v; want merged with a merge sha", pr)
	}
	if pr.HeadRef() != "lode/WL-1-fix" || pr.MergedAt == nil {
		t.Fatalf("PR = %+v; want head ref and merged_at", pr)
	}
}

func TestRepoClientDefaultBranch(t *testing.T) {
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app": `{"default_branch": "main"}`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	branch, err := rc.DefaultBranch(t.Context())
	if err != nil || branch != "main" {
		t.Fatalf("DefaultBranch = %q, %v; want main", branch, err)
	}
}

func TestRepoClientCommitOnBranch(t *testing.T) {
	sha, off := "2222222222222222222222222222222222222222", "3333333333333333333333333333333333333333"
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app/compare/" + sha + "...main": `{"status": "ahead"}`,
		"/repos/acme/app/compare/" + off + "...main": `{"status": "diverged"}`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	on, err := rc.CommitOnBranch(t.Context(), "main", sha)
	if err != nil || !on {
		t.Fatalf("CommitOnBranch(ancestor) = %v, %v; want true", on, err)
	}
	on, err = rc.CommitOnBranch(t.Context(), "main", off)
	if err != nil || on {
		t.Fatalf("CommitOnBranch(diverged) = %v, %v; want false", on, err)
	}
	// An unknown sha 404s: not on the branch, not an error.
	on, err = rc.CommitOnBranch(t.Context(), "main", "4444444444444444444444444444444444444444")
	if err != nil || on {
		t.Fatalf("CommitOnBranch(unknown) = %v, %v; want false, nil", on, err)
	}
}

func TestRepoClientReleases(t *testing.T) {
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app/releases": `[
			{"tag_name": "v2", "target_commitish": "2222222222222222222222222222222222222222", "published_at": "2026-07-21T00:00:00Z"},
			{"tag_name": "v1", "target_commitish": "main", "published_at": "2026-07-01T00:00:00Z"}
		]`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	rels, err := rc.Releases(t.Context())
	if err != nil || len(rels) != 2 || rels[0].TagName != "v2" {
		t.Fatalf("Releases = %+v, %v; want 2 releases, v2 first", rels, err)
	}
}
