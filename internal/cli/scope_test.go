package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// resolveServer returns a client whose /api/v1/projects/resolve answers with
// the given status and body, and a counter of how many times it was called.
func resolveServer(t *testing.T, status int, body string) (*Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(Config{ServerURL: srv.URL, Token: "wl_test"}), &calls
}

const worklodeJSON = `{"id":"worklode","name":"Worklode","key":"WL","repos":[],"focus":[]}`

func TestResolveScopeConfigBeatsRemote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := initRepo(t, "git@github.com:acme/app.git")
	c, calls := resolveServer(t, http.StatusOK, worklodeJSON)

	cfg := Config{CurrentProject: "from-config", CurrentProjectPath: "/repo/.worklode/config.toml"}
	got := ResolveScope(context.Background(), c, cfg, dir)
	if got.Project != "from-config" {
		t.Fatalf("project = %q; want from-config", got.Project)
	}
	if got.Source != ScopeRepoConfig {
		t.Fatalf("source = %q; want %q", got.Source, ScopeRepoConfig)
	}
	if *calls != 0 {
		t.Fatalf("server called %d times; a configured project must not query", *calls)
	}
}

func TestResolveScopeUserConfigSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c, _ := resolveServer(t, http.StatusOK, worklodeJSON)

	cfg := Config{CurrentProject: "p", CurrentProjectPath: home + "/.config/worklode/config.toml"}
	if got := ResolveScope(context.Background(), c, cfg, home); got.Source != ScopeUserConfig {
		t.Fatalf("source = %q; want %q", got.Source, ScopeUserConfig)
	}
}

func TestResolveScopeFromGitRemote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := initRepo(t, "git@github.com:sunstoneinstitute/worklode.git")
	c, calls := resolveServer(t, http.StatusOK, worklodeJSON)

	got := ResolveScope(context.Background(), c, Config{}, dir)
	if got.Project != "worklode" || got.Key != "WL" {
		t.Fatalf("scope = %+v; want worklode/WL", got)
	}
	if got.Source != ScopeGitRemote || got.Cached {
		t.Fatalf("scope = %+v; want an uncached git-remote source", got)
	}
	if *calls != 1 {
		t.Fatalf("server called %d times; want 1", *calls)
	}

	// Second resolution is served from the cache.
	got = ResolveScope(context.Background(), c, Config{}, dir)
	if got.Project != "worklode" || !got.Cached {
		t.Fatalf("second scope = %+v; want a cached worklode", got)
	}
	if *calls != 1 {
		t.Fatalf("server called %d times; the cache must prevent a second query", *calls)
	}
}

func TestResolveScopeUnmappedRepoIsCachedNegative(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := initRepo(t, "git@github.com:acme/unmapped.git")
	c, calls := resolveServer(t, http.StatusNotFound, `{"error":"not found"}`)

	for i := 0; i < 2; i++ {
		got := ResolveScope(context.Background(), c, Config{}, dir)
		if got.Project != "" || got.Source != ScopeNone {
			t.Fatalf("unmapped repo scope = %+v; want an empty ScopeNone", got)
		}
	}
	if *calls != 1 {
		t.Fatalf("server called %d times; the negative entry must be cached", *calls)
	}
}

func TestResolveScopeCacheIsPerServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := initRepo(t, "git@github.com:acme/app.git")
	a, _ := resolveServer(t, http.StatusOK, `{"id":"on-a","name":"A","key":"AA","repos":[],"focus":[]}`)
	b, bCalls := resolveServer(t, http.StatusOK, `{"id":"on-b","name":"B","key":"BB","repos":[],"focus":[]}`)

	if got := ResolveScope(context.Background(), a, Config{}, dir); got.Project != "on-a" {
		t.Fatalf("scope on server a = %+v; want on-a", got)
	}
	got := ResolveScope(context.Background(), b, Config{}, dir)
	if got.Project != "on-b" || got.Key != "BB" {
		t.Fatalf("scope on server b = %+v; want on-b/BB — server a's answer must not be reused", got)
	}
	if *bCalls != 1 {
		t.Fatalf("server b called %d times; want 1", *bCalls)
	}
}

func TestResolveScopeDegradesSilently(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, `{"error":"boom"}`},
		{"malformed body", http.StatusOK, `not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			dir := initRepo(t, "git@github.com:acme/app.git")
			c, _ := resolveServer(t, tc.status, tc.body)
			if got := ResolveScope(context.Background(), c, Config{}, dir); got.Source != ScopeNone {
				t.Fatalf("scope = %+v; want ScopeNone", got)
			}
		})
	}

	t.Run("not a git repo", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		c, calls := resolveServer(t, http.StatusOK, worklodeJSON)
		if got := ResolveScope(context.Background(), c, Config{}, t.TempDir()); got.Source != ScopeNone {
			t.Fatalf("scope = %+v; want ScopeNone", got)
		}
		if *calls != 0 {
			t.Fatalf("server called %d times; no remote means no query", *calls)
		}
	})

	t.Run("nil client", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		dir := initRepo(t, "git@github.com:acme/app.git")
		if got := ResolveScope(context.Background(), nil, Config{}, dir); got.Source != ScopeNone {
			t.Fatalf("scope = %+v; want ScopeNone", got)
		}
	})
}

func TestProjectKeyCachesLookup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"projects":[`+worklodeJSON+`]}`)
	}))
	defer srv.Close()
	c := NewClient(Config{ServerURL: srv.URL, Token: "wl_test"})

	for i := 0; i < 2; i++ {
		if got := ProjectKey(context.Background(), c, "worklode"); got != "WL" {
			t.Fatalf("ProjectKey = %q; want WL", got)
		}
	}
	if calls != 1 {
		t.Fatalf("server called %d times; want 1 (cached thereafter)", calls)
	}
	if got := ProjectKey(context.Background(), c, ""); got != "" {
		t.Fatalf("ProjectKey(\"\") = %q; want \"\"", got)
	}
}
