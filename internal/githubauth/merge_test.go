package githubauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mergeFixture serves the endpoints a merge or an enqueue touches: the
// installation lookup, the token mint, the pull request, its REST merge, and
// GraphQL. mergeStatus and graphQLError make it refuse.
type mergeFixture struct {
	mergeStatus  int    // status for PUT .../merge (0 means 200)
	graphQLError string // when set, /graphql answers 200 with this error

	mu     sync.Mutex
	calls  []string
	auth   map[string]string
	bodies map[string]string
}

func (f *mergeFixture) start(t *testing.T) *AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		if f.auth == nil {
			f.auth, f.bodies = map[string]string{}, map[string]string{}
		}
		f.auth[r.URL.Path] = r.Header.Get("Authorization")
		f.bodies[r.URL.Path] = string(body)
		f.mu.Unlock()

		switch r.URL.Path {
		case "/repos/acme/app/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 42})
		case "/app/installations/42/access_tokens":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/app/pulls/7":
			json.NewEncoder(w).Encode(map[string]any{"number": 7, "node_id": "PR_node7"})
		case "/repos/acme/app/pulls/7/merge":
			if f.mergeStatus != 0 {
				w.WriteHeader(f.mergeStatus)
				json.NewEncoder(w).Encode(map[string]any{"message": "Pull Request is not mergeable"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"merged": true, "sha": "deadbeef"})
		case "/graphql":
			if f.graphQLError != "" {
				json.NewEncoder(w).Encode(map[string]any{
					"errors": []map[string]any{{"message": f.graphQLError}},
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"enqueuePullRequest": map[string]any{"clientMutationId": nil}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &AppAuth{AppID: "12345", Key: testKey(), BaseURL: srv.URL}
}

func (f *mergeFixture) called(method, path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == method+" "+path {
			return true
		}
	}
	return false
}

func (f *mergeFixture) body(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[path]
}

// The node id comes from the REST pull request, read with the installation
// token: it is what the GraphQL mutation takes and REST is the only place it
// is published.
func TestPRNodeID(t *testing.T) {
	t.Parallel()
	f := &mergeFixture{}
	a := f.start(t)

	id, err := a.PRNodeID(context.Background(), "acme/app", 7)
	if err != nil {
		t.Fatalf("PRNodeID: %v", err)
	}
	if id != "PR_node7" {
		t.Errorf("PRNodeID = %q, want PR_node7", id)
	}
	f.mu.Lock()
	auth := f.auth["/repos/acme/app/pulls/7"]
	f.mu.Unlock()
	if auth != "Bearer ghs_test" {
		t.Errorf("Authorization = %q, want the installation token", auth)
	}
}

// Enqueuing posts the mutation to /graphql with the node id as its variable.
func TestEnqueuePR(t *testing.T) {
	t.Parallel()
	f := &mergeFixture{}
	a := f.start(t)

	if err := a.EnqueuePR(context.Background(), "acme/app", "PR_node7"); err != nil {
		t.Fatalf("EnqueuePR: %v", err)
	}
	if !f.called("POST", "/graphql") {
		t.Fatal("/graphql was never called")
	}
	sent := f.body("/graphql")
	for _, want := range []string{"enqueuePullRequest", "PR_node7"} {
		if !strings.Contains(sent, want) {
			t.Errorf("mutation body = %s, want it to name %s", sent, want)
		}
	}
}

// GraphQL explains a refusal in a 200's body. It is still a refusal, so it
// comes back typed, with GitHub's message and a 4xx status.
func TestEnqueuePRGraphQLError(t *testing.T) {
	t.Parallel()
	f := &mergeFixture{graphQLError: "Pull request is in a draft state"}
	a := f.start(t)

	err := a.EnqueuePR(context.Background(), "acme/app", "PR_node7")
	var ghErr *GitHubError
	if !errors.As(err, &ghErr) {
		t.Fatalf("EnqueuePR error = %v, want a *GitHubError", err)
	}
	if ghErr.Message != "Pull request is in a draft state" {
		t.Errorf("message = %q, want GitHub's", ghErr.Message)
	}
	if ghErr.Status < 400 || ghErr.Status >= 500 {
		t.Errorf("status = %d, want a 4xx so the route answers 409", ghErr.Status)
	}
}

// Merging is the REST endpoint, with the method the caller named.
func TestMergePR(t *testing.T) {
	t.Parallel()
	f := &mergeFixture{}
	a := f.start(t)

	if err := a.MergePR(context.Background(), "acme/app", 7, "squash"); err != nil {
		t.Fatalf("MergePR: %v", err)
	}
	if !f.called("PUT", "/repos/acme/app/pulls/7/merge") {
		t.Fatal("the merge endpoint was never called")
	}
	if sent := f.body("/repos/acme/app/pulls/7/merge"); !strings.Contains(sent, `"merge_method":"squash"`) {
		t.Errorf("merge body = %s, want the method", sent)
	}
}

// GitHub's 405 on an unmergeable PR keeps its message: the page shows it.
func TestMergePRRefused(t *testing.T) {
	t.Parallel()
	f := &mergeFixture{mergeStatus: http.StatusMethodNotAllowed}
	a := f.start(t)

	err := a.MergePR(context.Background(), "acme/app", 7, "")
	var ghErr *GitHubError
	if !errors.As(err, &ghErr) {
		t.Fatalf("MergePR error = %v, want a *GitHubError", err)
	}
	if ghErr.Status != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", ghErr.Status)
	}
	if ghErr.Message != "Pull Request is not mergeable" {
		t.Errorf("message = %q, want GitHub's", ghErr.Message)
	}
}
