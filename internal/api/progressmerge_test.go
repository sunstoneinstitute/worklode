// progressmerge_test.go exercises POST /projects/{id}/progress/merge
// (WL-SPEC-66 §3.6) against an httptest GitHub, white-box like the other
// Progress writes: a web route takes its actor from a session, and these
// tests configure no login provider.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const mergeRepo = "acme/app"

// fakeMergeGitHub serves what one merge or enqueue touches. beforeCall runs
// on the first call that acts (the PR read, the mutation, or the merge), so a
// test can assert what the backbone already recorded by then.
type fakeMergeGitHub struct {
	mergeStatus int  // status for PUT .../merge (0 means 200)
	graphQLFail bool // /graphql answers 200 with an errors array

	calls      []string
	beforeCall func()
}

func (f *fakeMergeGitHub) start(t *testing.T) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/repos/acme/app/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/app/pulls/7":
			if f.beforeCall != nil {
				f.beforeCall()
			}
			json.NewEncoder(w).Encode(map[string]any{"node_id": "PR_node7"})
		case "/graphql":
			if f.graphQLFail {
				json.NewEncoder(w).Encode(map[string]any{
					"errors": []map[string]any{{"message": "Pull request is in a draft state"}},
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		case "/repos/acme/app/pulls/7/merge":
			if f.beforeCall != nil {
				f.beforeCall()
			}
			if f.mergeStatus != 0 {
				w.WriteHeader(f.mergeStatus)
				json.NewEncoder(w).Encode(map[string]any{"message": "Required status check \"build\" is expected."})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"merged": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &githubauth.AppAuth{AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}
}

func (f *fakeMergeGitHub) called(method, path string) bool {
	for _, c := range f.calls {
		if c == method+" "+path {
			return true
		}
	}
	return false
}

// newMergeServer is newProbeServer plus a task, an open PR carrying it, and
// the repo's stored branch rules. queue is what §6.3 knows about the base
// branch; auth may be nil, which is an instance with no App.
func newMergeServer(t *testing.T, auth *githubauth.AppAuth, queue bool) (*server, string) {
	t.Helper()
	s := newProbeServer(t)
	s.appAuth = auth

	var taskID string
	err := s.recordEvent(t.Context(), "cli", "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := store.CreateTask(tx, s.st.Now(), store.TaskInput{
				ProjectID: "p", Title: "queue for merge", Priority: "medium", Kind: "feature",
			}, eventID)
			if err != nil {
				return err
			}
			taskID = task.ID
			return nil
		})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	err = s.st.Tx(t.Context(), func(tx *sql.Tx) error {
		if _, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: mergeRepo, Number: 7, Title: "the change", State: "open",
			HeadRef: taskID + "-queue-for-merge", HeadSHA: "abc123",
			URL:      "https://github.com/acme/app/pull/7",
			OpenedAt: s.st.Now(), UpdatedAt: s.st.Now(), Author: "alice",
		}, ""); err != nil {
			return err
		}
		return store.UpsertBranchRules(tx, mergeRepo, "main", queue, s.st.Now())
	})
	if err != nil {
		t.Fatalf("seed PR and branch rules: %v", err)
	}
	return s, taskID
}

// mergePost runs one POST /projects/p/progress/merge as probeActor.
func mergePost(t *testing.T, s *server, task string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(model.ProgressMergeInput{
		Task: task, PR: model.ProgressPR{Repo: mergeRepo, Number: 7},
	})
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := goodPost(string(body))
	req.SetPathValue("id", "p")
	req = withSubject(req, Subject{ActorID: probeActor})
	rr := httptest.NewRecorder()
	s.progressMerge(rr, req)
	return rr
}

// mergeEvents are the merge events recorded so far, in log order.
func mergeEvents(t *testing.T, s *server) []store.Event {
	t.Helper()
	all, err := s.st.ListEvents(context.Background(), store.EventFilter{After: 0, Limit: 200})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var out []store.Event
	for _, e := range all {
		if strings.HasPrefix(e.Type, "pr.merge_") {
			out = append(out, e)
		}
	}
	return out
}

func payloadField(t *testing.T, e store.Event, key string) string {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(e.Payload, &fields); err != nil {
		t.Fatalf("decode %s payload: %v", e.Type, err)
	}
	v, ok := fields[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// TestProgressMergeEnqueues: a queue-protected branch is enqueued through
// GraphQL, and criterion 19 — the intent event is in the log before the
// GitHub call is made, checked from inside the call itself.
func TestProgressMergeEnqueues(t *testing.T) {
	f := &fakeMergeGitHub{}
	var recordedByCallTime []store.Event
	s, task := newMergeServer(t, f.start(t), true)
	f.beforeCall = func() {
		if recordedByCallTime == nil {
			recordedByCallTime = mergeEvents(t, s)
		}
	}

	rr := mergePost(t, s, task)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	var got model.ProgressMergeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	if (got != model.ProgressMergeResponse{Op: "enqueue", Queued: true}) {
		t.Errorf("reply = %+v; want the enqueue op", got)
	}
	if !f.called("GET", "/repos/acme/app/pulls/7") || !f.called("POST", "/graphql") {
		t.Errorf("calls = %v; want the node id read and the mutation", f.calls)
	}

	// Criterion 19: by the time GitHub was called, the request was recorded.
	if len(recordedByCallTime) != 1 || recordedByCallTime[0].Type != "pr.merge_requested" {
		t.Fatalf("events at call time = %+v; want pr.merge_requested alone", recordedByCallTime)
	}
	if actor := payloadField(t, recordedByCallTime[0], "actor"); actor != probeActor {
		t.Errorf("recorded actor = %q; want the session's %q", actor, probeActor)
	}
	if op := payloadField(t, recordedByCallTime[0], "op"); op != "enqueue" {
		t.Errorf("recorded op = %q; want enqueue", op)
	}
	if got := payloadField(t, recordedByCallTime[0], "task"); got != task {
		t.Errorf("recorded task = %q; want %q", got, task)
	}

	events := mergeEvents(t, s)
	if len(events) != 2 || events[0].Type != "pr.merge_requested" || events[1].Type != "pr.merge_result" {
		t.Fatalf("events = %+v; want the request then the result", events)
	}
	if outcome := payloadField(t, events[1], "outcome"); outcome != "ok" {
		t.Errorf("outcome = %q; want ok", outcome)
	}
	for _, op := range []string{"pr_node_id", "enqueue_pr"} {
		if n := testutil.ToFloat64(s.githubCalls.WithLabelValues(op)); n != 1 {
			t.Errorf("worklode_github_calls_total{op=%s} = %v, want 1", op, n)
		}
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("merge", "ok")); n != 1 {
		t.Errorf("progressWrites{merge,ok} = %v, want 1", n)
	}
}

// TestProgressMergeMerges: a branch with no queue rule goes through the REST
// merge endpoint instead.
func TestProgressMergeMerges(t *testing.T) {
	f := &fakeMergeGitHub{}
	s, task := newMergeServer(t, f.start(t), false)

	rr := mergePost(t, s, task)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	var got model.ProgressMergeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	if got.Op != "merge" || got.Queued {
		t.Errorf("reply = %+v; want the merge op and no queue", got)
	}
	if !f.called("PUT", "/repos/acme/app/pulls/7/merge") {
		t.Errorf("calls = %v; want the REST merge", f.calls)
	}
	if f.called("POST", "/graphql") {
		t.Errorf("calls = %v; want no mutation on an unqueued branch", f.calls)
	}
	if n := testutil.ToFloat64(s.githubCalls.WithLabelValues("merge_pr")); n != 1 {
		t.Errorf("worklode_github_calls_total{op=merge_pr} = %v, want 1", n)
	}
}

// TestProgressMergeRefusedByGitHub: GitHub's 405 becomes a 409 carrying its
// own message, and the result event says the act was refused.
func TestProgressMergeRefusedByGitHub(t *testing.T) {
	f := &fakeMergeGitHub{mergeStatus: http.StatusMethodNotAllowed}
	s, task := newMergeServer(t, f.start(t), false)

	rr := mergePost(t, s, task)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s; want 409", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Required status check") {
		t.Errorf("body = %s; want GitHub's own message", rr.Body.String())
	}
	events := mergeEvents(t, s)
	if len(events) != 2 {
		t.Fatalf("events = %+v; want the request and the result", events)
	}
	if outcome := payloadField(t, events[1], "outcome"); outcome != "refused" {
		t.Errorf("outcome = %q; want refused", outcome)
	}
	if msg := payloadField(t, events[1], "message"); !strings.Contains(msg, "Required status check") {
		t.Errorf("recorded message = %q; want GitHub's", msg)
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("merge", "conflict")); n != 1 {
		t.Errorf("progressWrites{merge,conflict} = %v, want 1", n)
	}
}

// TestProgressMergeWithoutApp: nothing to act with, and nothing is recorded.
func TestProgressMergeWithoutApp(t *testing.T) {
	s, task := newMergeServer(t, nil, true)

	rr := mergePost(t, s, task)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s; want 503", rr.Code, rr.Body.String())
	}
	if events := mergeEvents(t, s); len(events) != 0 {
		t.Errorf("events = %+v; want none", events)
	}
}

// TestProgressMergeIdempotence: a PR already in the queue, and one already
// merged, are 409s that call GitHub not at all (§4.2 rule 7).
func TestProgressMergeIdempotence(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		set        func(t *testing.T, s *server)
	}{
		{
			name: "already queued",
			want: "already queued for merge",
			set: func(t *testing.T, s *server) {
				now := s.st.Now()
				if err := s.st.Tx(t.Context(), func(tx *sql.Tx) error {
					return store.SetPRQueued(tx, mergeRepo, 7, &now)
				}); err != nil {
					t.Fatalf("queue the PR: %v", err)
				}
			},
		},
		{
			name: "already merged",
			want: "already merged",
			set: func(t *testing.T, s *server) {
				merged := s.st.Now()
				if err := s.st.Tx(t.Context(), func(tx *sql.Tx) error {
					_, _, err := store.UpsertPR(tx, store.PullRequest{
						Repo: mergeRepo, Number: 7, Title: "the change", State: "merged",
						HeadSHA: "abc123", URL: "https://github.com/acme/app/pull/7",
						OpenedAt: merged, MergedAt: &merged, UpdatedAt: merged.Add(time.Minute),
					}, "")
					return err
				}); err != nil {
					t.Fatalf("merge the PR: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeMergeGitHub{}
			s, task := newMergeServer(t, f.start(t), true)
			tc.set(t, s)

			rr := mergePost(t, s, task)
			if rr.Code != http.StatusConflict {
				t.Fatalf("status = %d body=%s; want 409", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.want) {
				t.Errorf("body = %s; want it to say %q", rr.Body.String(), tc.want)
			}
			if len(f.calls) != 0 {
				t.Errorf("calls = %v; want GitHub untouched", f.calls)
			}
			if events := mergeEvents(t, s); len(events) != 0 {
				t.Errorf("events = %+v; want none", events)
			}
		})
	}
}

// TestProgressMergeWrongTask: the page's facts can go stale between render
// and click. A PR that does not carry the named task is a 409, not a merge.
func TestProgressMergeWrongTask(t *testing.T) {
	f := &fakeMergeGitHub{}
	s, _ := newMergeServer(t, f.start(t), true)

	var other string
	if err := s.recordEvent(t.Context(), "cli", "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := store.CreateTask(tx, s.st.Now(), store.TaskInput{
				ProjectID: "p", Title: "something else", Priority: "medium", Kind: "bug",
			}, eventID)
			if err != nil {
				return err
			}
			other = task.ID
			return nil
		}); err != nil {
		t.Fatalf("seed second task: %v", err)
	}

	rr := mergePost(t, s, other)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s; want 409", rr.Code, rr.Body.String())
	}
	if len(f.calls) != 0 {
		t.Errorf("calls = %v; want GitHub untouched", f.calls)
	}
}
