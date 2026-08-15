//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// pollEventListE2E polls GET /api/v1/events (via the CLI client) until it
// returns at least want events, or fails the test. The commit horizon
// (pg_snapshot_xmin) is cluster-wide, not per-database (see
// internal/store/events_test.go's pollListEvents): a concurrent transaction
// in another package's test binary sharing this Postgres instance can hold a
// read back to fewer events than were actually committed, so "nothing yet"
// must never be read as success.
func pollEventListE2E(t *testing.T, ctx context.Context, c *cli.Client, f cli.EventListFilter, want int) []cli.Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, _, err := c.ListEvents(ctx, f)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(resp.Events) >= want {
			return resp.Events
		}
		if time.Now().After(deadline) {
			t.Fatalf("ListEvents: got %d events after polling, want %d "+
				"(commit horizon held back by a concurrent transaction elsewhere on the instance?)",
				len(resp.Events), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestEventLog proves the ordered event log (spec 025 §15) end to end
// through public surfaces only: a real signed GitHub webhook delivery
// becomes a row a real API client can read back with its vendor type and
// source intact (025 §15.2's "one log, two populations"), the subscriber and
// seek surfaces answer correctly (including the admin-only guard on seek),
// and the live SSE stream (025 §18) delivers a delivery made while it is
// open — admin-only, with a plain 403 for an agent token.
func TestEventLog(t *testing.T) {
	ctx := context.Background()

	// 1. Real stack, same boot sequence as TestFullChain.
	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken:      bootstrapToken,
		GitHubWebhookSecret: githubSecret,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, cli.CreateProjectInput{
		ID: "demo", Name: "Demo", Key: "DEMO",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.AddRepo(ctx, "demo", repo, ""); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, cli.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e events", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// 2. Deliver one signed GitHub push webhook to the default branch, and
	// read it back as the admin: its vendor type ("push", 025 §15.2's own
	// example of a dotted vendor type) and source ("github") must survive
	// the round trip through the shared log.
	deliverGitHub(t, srv.URL, "push", "e2e-events-push-1", map[string]any{
		"ref":        "refs/heads/main",
		"repository": map[string]any{"full_name": repo, "default_branch": "main"},
		"commits": []any{
			map[string]any{"id": headSHA, "message": "e2e events: first push"},
		},
		"head_commit": map[string]any{"id": headSHA, "message": "e2e events: first push"},
	})
	events := pollEventListE2E(t, ctx, admin, cli.EventListFilter{Type: "push"}, 1)
	if events[0].ExternalID != "e2e-events-push-1" || events[0].Source != "github" || events[0].Type != "push" {
		t.Fatalf("first event = %+v, want external_id e2e-events-push-1, source github, type push", events[0])
	}
	firstID := events[0].ID

	// 3. GET /api/v1/event-subscribers: no subscriber is wired until part 2,
	// so the list is empty but the call still succeeds.
	subs, _, err := admin.EventSubscribers(ctx)
	if err != nil {
		t.Fatalf("event subscribers: %v", err)
	}
	if len(subs.Subscribers) != 0 {
		t.Fatalf("event subscribers = %+v, want none wired yet", subs.Subscribers)
	}

	// 4. seek: admin against an unknown name is 404, and an agent token
	// (RoleUser only) is refused before the name is even looked up.
	if _, _, err := admin.SeekEventSubscriber(ctx, "nope", 0); !isClientStatus(err, http.StatusNotFound) {
		t.Fatalf("admin seek of unknown subscriber = %v, want a 404 ClientError", err)
	}
	if _, _, err := agent.SeekEventSubscriber(ctx, "nope", 0); !isClientStatus(err, http.StatusForbidden) {
		t.Fatalf("agent seek = %v, want a 403 ClientError", err)
	}

	// 5. Stream: an admin opens GET /api/v1/events/stream resuming right
	// after the first delivery, a second signed webhook lands while it is
	// open, and it must arrive on the live connection. The request context
	// carries the only deadline — StreamEvents blocks until it, the server,
	// or the callback ends it, so the stream must never be waited on without
	// one.
	streamCtx, cancelStream := context.WithTimeout(ctx, 20*time.Second)
	defer cancelStream()
	errGotIt := errors.New("got the second delivery")
	type streamResult struct {
		event cli.Event
		err   error
	}
	resultCh := make(chan streamResult, 1)
	go func() {
		var got cli.Event
		err := admin.StreamEvents(streamCtx, cli.EventStreamFilter{Type: "push", After: firstID},
			func(e cli.Event) error {
				got = e
				return errGotIt
			})
		resultCh <- streamResult{got, err}
	}()

	deliverGitHub(t, srv.URL, "push", "e2e-events-push-2", map[string]any{
		"ref":        "refs/heads/main",
		"repository": map[string]any{"full_name": repo, "default_branch": "main"},
		"commits": []any{
			map[string]any{"id": mergeSHA, "message": "e2e events: second push"},
		},
		"head_commit": map[string]any{"id": mergeSHA, "message": "e2e events: second push"},
	})

	select {
	case res := <-resultCh:
		if !errors.Is(res.err, errGotIt) {
			t.Fatalf("admin stream ended with %v, want the callback's own error back", res.err)
		}
		if res.event.ExternalID != "e2e-events-push-2" || res.event.Source != "github" || res.event.Type != "push" {
			t.Fatalf("streamed event = %+v, want the second push delivery", res.event)
		}
	case <-streamCtx.Done():
		t.Fatal("admin stream: the second delivery never arrived within the deadline")
	}

	// The same stream is refused for an agent token: a plain 403, not a
	// silently empty connection. Its own bounded deadline ends the attempt
	// even if the refusal never came (which would itself be the bug).
	refuseCtx, cancelRefuse := context.WithTimeout(ctx, 10*time.Second)
	defer cancelRefuse()
	err = agent.StreamEvents(refuseCtx, cli.EventStreamFilter{Type: "push"}, func(cli.Event) error {
		return errors.New("callback must not run for a non-admin token")
	})
	if !isClientStatus(err, http.StatusForbidden) {
		t.Fatalf("agent stream = %v, want a 403 ClientError", err)
	}
}

// isClientStatus reports whether err is a *cli.ClientError carrying the
// given HTTP status.
func isClientStatus(err error, status int) bool {
	var ce *cli.ClientError
	return errors.As(err, &ce) && ce.Status == status
}
