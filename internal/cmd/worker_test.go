package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// fakeClaimNext serves POST /api/v1/tasks/claim-next, handing each call the
// next body in replies (repeating the last one once exhausted) and recording
// the decoded request bodies. Anything else 404s, so a test fails loudly if
// listen starts asking a different endpoint.
func fakeClaimNext(t *testing.T, replies ...string) (calls *atomic.Int64, bodies *[]map[string]any) {
	t.Helper()
	calls = &atomic.Int64{}
	seen := &[]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks/claim-next" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		*seen = append(*seen, body)
		n := int(calls.Add(1)) - 1
		if n >= len(replies) {
			n = len(replies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, replies[n])
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	return calls, seen
}

const (
	noReadyTask = `{"claimed":false,"reason":"no-ready-task"}`
	dryRunPick  = `{"claimed":false,"dry_run":true,"task":{"id":"WL-7","slug":"fix-it",` +
		`"branch":"WL-7-fix-it","concern":"completeness","priority":"high","project":"worklode"}}`
)

// listen must never claim: a dry-run pick that reserved the task would take
// work away from the loop it is supposed to be waking.
func TestWorkerListenAlwaysAsksDryRun(t *testing.T) {
	_, bodies := fakeClaimNext(t, dryRunPick)

	if _, err := runLode(t, "worker", "listen", "--once", "--project", "worklode"); err != nil {
		t.Fatalf("listen --once: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("claim-next calls = %d, want 1", len(*bodies))
	}
	if got := (*bodies)[0]["dry_run"]; got != true {
		t.Fatalf("dry_run = %v, want true", got)
	}
	if got := (*bodies)[0]["worktree"]; got != "" {
		t.Fatalf("worktree = %q, want empty on a dry run", got)
	}
}

// The filter flags must reach the server unchanged — that is what lets one
// filter string drive both a listener and the loop it wakes.
func TestWorkerListenForwardsFilter(t *testing.T) {
	_, bodies := fakeClaimNext(t, dryRunPick)

	if _, err := runLode(t, "worker", "listen", "--once",
		"--project", "worklode", "--kind", "chore", "--strict-focus"); err != nil {
		t.Fatalf("listen --once: %v", err)
	}
	body := (*bodies)[0]
	if body["project"] != "worklode" || body["kind"] != "chore" || body["strict_focus"] != true {
		t.Fatalf("filter not forwarded: %#v", body)
	}
}

func TestWorkerListenOncePrintsPick(t *testing.T) {
	fakeClaimNext(t, dryRunPick)

	out, err := runLode(t, "worker", "listen", "--once", "--project", "worklode")
	if err != nil {
		t.Fatalf("listen --once: %v", err)
	}
	for _, want := range []string{"ID", "WL-7", "high", "completeness", "WL-7-fix-it"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWorkerListenOnceJSONIsThePick(t *testing.T) {
	fakeClaimNext(t, dryRunPick)

	out, err := runLode(t, "worker", "listen", "--once", "--project", "worklode", "--json")
	if err != nil {
		t.Fatalf("listen --once --json: %v", err)
	}
	var pick map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &pick); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if pick["id"] != "WL-7" || pick["branch"] != "WL-7-fix-it" {
		t.Fatalf("unexpected pick: %#v", pick)
	}
}

// An empty queue must keep the listener waiting rather than exiting 0 with
// nothing — a caller that restarted on every quiet poll would spin.
func TestWorkerListenWaitsThroughEmptyQueue(t *testing.T) {
	calls, _ := fakeClaimNext(t, noReadyTask, noReadyTask, dryRunPick)

	out, err := runLode(t, "worker", "listen", "--once",
		"--project", "worklode", "--interval", "1ms")
	if err != nil {
		t.Fatalf("listen --once: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("claim-next calls = %d, want 3 (two empty, then the pick)", got)
	}
	if !strings.Contains(out, "WL-7") {
		t.Fatalf("output missing the eventual pick:\n%s", out)
	}
}

// A rejected token will be rejected on every retry, so it must end the watch
// instead of being buried in a warning loop.
func TestWorkerListenFailsFastOnRejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")

	if _, err := runLode(t, "worker", "listen", "--project", "worklode", "--interval", "1ms"); err == nil {
		t.Fatal("listen exited 0 on a 401; want the error surfaced")
	}
}

func TestWorkerListenRejectsNonPositiveInterval(t *testing.T) {
	fakeClaimNext(t, noReadyTask)

	if _, err := runLode(t, "worker", "listen", "--project", "worklode", "--interval", "0s"); err == nil {
		t.Fatal("--interval 0s accepted; want an error")
	}
}

// Without --once the same unclaimed pick is returned by every poll. Reprinting
// it each interval would bury the transitions a reader is watching for, so
// only a change is printed — and the pick is reported again after a lull.
func TestWorkerListenPrintsOnlyPickChanges(t *testing.T) {
	fakeClaimNext(t, dryRunPick, dryRunPick, noReadyTask, dryRunPick)

	c, _, err := newAPIClientWithConfig()
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SetContext(ctx)

	// Four polls' worth, then stop: the fake repeats its last reply forever.
	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()
	if err := runWorkerListen(cmd, c, model.ClaimNextInput{DryRun: true}, time.Millisecond, false); err != nil {
		t.Fatalf("listen: %v", err)
	}

	var rows, headers int
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		switch {
		case strings.HasPrefix(line, "WL-7"):
			rows++
		case strings.HasPrefix(line, "ID"):
			headers++
		}
	}
	if rows != 2 {
		t.Fatalf("printed %d rows, want 2 (once, then again after the queue emptied):\n%s",
			rows, out.String())
	}
	if headers != 1 {
		t.Fatalf("printed the header %d times, want 1:\n%s", headers, out.String())
	}
}
