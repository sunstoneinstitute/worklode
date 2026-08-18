package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// syncBuffer is a bytes.Buffer safe to read from the test while the follow
// goroutine writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// pollTailBacklog waits until the events recorded by a test are readable past
// the cluster-wide commit horizon, the same accommodation as
// internal/cli's pollClientEvents.
func pollTailBacklog(t *testing.T, c *cli.Client, typ string, want int) []model.Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, _, err := c.ListEvents(context.Background(), cli.EventListFilter{Type: typ})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(resp.Events) >= want {
			return resp.Events
		}
		if time.Now().After(deadline) {
			t.Fatalf("ListEvents(type=%s): got %d after polling, want %d "+
				"(commit horizon held back by a concurrent transaction elsewhere on the instance?)",
				typ, len(resp.Events), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// followCmd builds a bare command carrying ctx and the --json flag
// followEvents reads, writing to out.
func followCmd(ctx context.Context, out *syncBuffer, asJSON bool) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	cmd.SetOut(out)
	cmd.Flags().Bool("json", asJSON, "")
	return cmd
}

// waitFor polls out until it contains want, or fails.
func waitFor(t *testing.T, out *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if strings.Contains(out.String(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("follow output never contained %q; got:\n%s", want, out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestEventTailFollow covers `lode event tail --follow`: the bounded backlog
// prints first, the stream resumes from the last id printed so nothing is
// repeated or missed across the seam, and a cancelled context (Ctrl-C) is a
// clean exit rather than an error.
func TestEventTailFollow(t *testing.T) {
	st, c := lifecycleTestServer(t)
	recordTailEvent(t, st, "tf-1", "tail.follow")
	backlog := pollTailBacklog(t, c, "tail.follow", 1)

	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- followEvents(followCmd(ctx, out, false), c, "tail.follow", backlog) }()
	defer cancel()

	waitFor(t, out, "tf-1")
	recordTailEvent(t, st, "tf-2", "tail.follow")
	waitFor(t, out, "tf-2")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("followEvents after cancel = %v, want nil (Ctrl-C exits 0)", err)
	}

	got := out.String()
	if !strings.Contains(got, "EXTERNAL_ID") {
		t.Fatalf("follow output has no column header:\n%s", got)
	}
	if n := strings.Count(got, "tf-1"); n != 1 {
		t.Fatalf("backlog event printed %d times, want once (the stream resumes after it):\n%s", n, got)
	}
}

// TestEventTailFollowJSON covers the --json form: NDJSON, one event object
// per line, because a stream has no closing bracket for an array to have.
func TestEventTailFollowJSON(t *testing.T) {
	st, c := lifecycleTestServer(t)
	recordTailEvent(t, st, "tj-1", "tail.json")
	backlog := pollTailBacklog(t, c, "tail.json", 1)

	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- followEvents(followCmd(ctx, out, true), c, "tail.json", backlog) }()
	defer cancel()

	waitFor(t, out, "tj-1")
	recordTailEvent(t, st, "tj-2", "tail.json")
	waitFor(t, out, "tj-2")

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("followEvents after cancel = %v, want nil", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("NDJSON lines = %d, want 2:\n%s", len(lines), out.String())
	}
	for i, line := range lines {
		var e model.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is not one JSON event (%v): %s", i, err, line)
		}
		if e.Type != "tail.json" {
			t.Fatalf("line %d event type = %q, want tail.json", i, e.Type)
		}
	}
}

func recordTailEvent(t *testing.T, st *store.Store, extID, typ string) {
	t.Helper()
	if _, _, err := st.RecordEvent(context.Background(), "system", extID, typ, nil, nil); err != nil {
		t.Fatalf("RecordEvent %s: %v", extID, err)
	}
}

// TestEventTailFollowServerClose covers the other way a follow ends: the
// server closes the connection. Ctrl-C exits 0, but this must not — with
// reconnect deferred, a silent exit 0 would tell the user their view of the
// log is current when it stopped advancing at the restart.
func TestEventTailFollowServerClose(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "id: 1\nevent: t.one\ndata: {\"id\":1,\"type\":\"t.one\"}\n\n")
	}))
	t.Cleanup(ts.Close)
	c := cli.NewClient(cli.Config{ServerURL: ts.URL, Token: "wl_" + strings.Repeat("a", 40)})

	out := &syncBuffer{}
	err := followEvents(followCmd(context.Background(), out, false), c, "", nil)
	if !errors.Is(err, cli.ErrStreamEnded) {
		t.Fatalf("followEvents on a server-closed stream = %v, want ErrStreamEnded", err)
	}
	if !strings.Contains(err.Error(), "rerun") {
		t.Errorf("error does not tell the user what to do: %v", err)
	}
	if !strings.Contains(out.String(), "t.one") {
		t.Errorf("the event received before the close was not printed:\n%s", out.String())
	}
}
