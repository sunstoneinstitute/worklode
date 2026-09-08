package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const branchRulesRepo = "acme/widgets"

// fakeRulesApp serves the four endpoints one refresh pass needs for
// acme/widgets: the installation lookup, the token mint, the repo (for its
// default branch), and the branch rules. queue is what the rules endpoint
// reports, and it can be flipped mid-test to prove a refresh happened again.
type fakeRulesApp struct {
	mu    sync.Mutex
	queue bool
}

func (f *fakeRulesApp) setQueue(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = on
}

func (f *fakeRulesApp) start(t *testing.T) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/widgets":
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
		case "/repos/acme/widgets/rules/branches/main":
			f.mu.Lock()
			queue := f.queue
			f.mu.Unlock()
			rules := []map[string]any{{"type": "pull_request"}}
			if queue {
				rules = append(rules, map[string]any{"type": "merge_queue"})
			}
			json.NewEncoder(w).Encode(rules)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &githubauth.AppAuth{AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}
}

// branchRulesServer builds a server with acme/widgets mapped and the loop's
// kick channel in place, but does not start the loop.
func branchRulesServer(t *testing.T, auth *githubauth.AppAuth) *server {
	t.Helper()
	st := store.OpenTestStore(t)
	if err := st.CreateProject(context.Background(), "proj", "Proj", "PR"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(context.Background(), "proj", branchRulesRepo); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	return &server{
		st: st, cfg: Config{}, log: slog.Default(), appAuth: auth,
		branchRulesKick: make(chan struct{}, 1),
		githubCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_github_calls_total", Help: "test",
		}, []string{"op"}),
	}
}

// waitForMergeQueue blocks until the stored fact for acme/widgets' main branch
// is known and equals want. It waits on the condition itself, with a bounded
// timeout, so a slow CI machine costs time rather than a flake.
func waitForMergeQueue(t *testing.T, st *store.Store, want bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, known, err := st.BranchRules(context.Background(), branchRulesRepo, "main")
		if err != nil {
			t.Fatalf("read branch rules: %v", err)
		}
		if known && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("branch rules for %s@main = (%v, known=%v), want (%v, known=true)",
				branchRulesRepo, got, known, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The loop records every mapped repo's default-branch merge-queue rule on
// start, and a kick — what the repository_ruleset webhook does — makes it run
// once more rather than waiting out the day-long tick.
func TestBranchRulesLoopUpsertsAndRefreshesOnKick(t *testing.T) {
	f := &fakeRulesApp{queue: true}
	s := branchRulesServer(t, f.start(t))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.branchRulesLoop(ctx)

	waitForMergeQueue(t, s.st, true)

	// The queue is turned off at GitHub; only a fresh read can see it.
	f.setQueue(false)
	s.kickBranchRules()
	waitForMergeQueue(t, s.st, false)

	if n := testutil.ToFloat64(s.githubCalls.WithLabelValues("branch_rules")); n < 2 {
		t.Errorf("worklode_github_calls_total{op=branch_rules} = %v, want at least 2", n)
	}
}

// With no GitHub App configured there is nothing to read: the pass writes no
// row, so the fact stays unknown and readers keep treating it as "no queue".
func TestBranchRulesWithoutAppWritesNothing(t *testing.T) {
	s := branchRulesServer(t, nil)

	s.refreshBranchRules(context.Background())

	_, known, err := s.st.BranchRules(context.Background(), branchRulesRepo, "main")
	if err != nil {
		t.Fatalf("read branch rules: %v", err)
	}
	if known {
		t.Error("a server with no GitHub App recorded a branch-rules fact")
	}
}
