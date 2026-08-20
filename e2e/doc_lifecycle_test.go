//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// docLifecycleSubscriberName is the subscriber the server runs the two
// doc-lifecycle rules under (025 §15.4). It is deliberately spelled out here
// rather than imported: the name is part of the operator-facing surface this
// test exercises through GET /api/v1/event-subscribers, so the test must fail
// if it changes, not follow it.
const docLifecycleSubscriberName = "doc-lifecycle"

// tasksAboutDoc reads GET /api/v1/tasks?about_doc=<id>[&kind=<kind>] over raw
// HTTP. An empty kind does not filter. The task list is read this way rather
// than through cli.Client because TaskListFilter carries no about_doc field
// yet, and this suite drives public surfaces, not the client's convenience.
func tasksAboutDoc(t *testing.T, baseURL, token string, docID int64, kind string) []model.Task {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/tasks?about_doc=%d", baseURL, docID)
	if kind != "" {
		url += "&kind=" + kind
	}
	status, body := getAuthed(t, url, token)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200; body: %s", url, status, body)
	}
	var resp model.TaskListResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode task list %q: %v", body, err)
	}
	return resp.Tasks
}

// describeTasks renders a task list compactly for a failure message.
func describeTasks(tasks []model.Task) string {
	if len(tasks) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(tasks))
	for _, task := range tasks {
		parts = append(parts, fmt.Sprintf("%s(kind=%s state=%s about_doc=%d title=%q)",
			task.ID, task.Kind, task.State, task.AboutDoc, task.Title))
	}
	return strings.Join(parts, ", ")
}

// pollTasksAboutDoc polls the about_doc listing until exactly want tasks of
// the given kind reference the document, and returns them. Everything the
// watcher does is asynchronous behind the commit horizon, so this is a poll
// and never a sleep. On timeout it reports the last task list it saw and the
// subscriber's own status — a bare "timed out" tells a CI reader nothing
// about whether the loop was stalled, unlocked, or simply minted the wrong
// thing.
func pollTasksAboutDoc(
	t *testing.T, ctx context.Context, c *cli.Client, baseURL, token string,
	docID int64, kind string, want int, why string,
) []model.Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last []model.Task
	for {
		last = tasksAboutDoc(t, baseURL, token, docID, kind)
		if len(last) == want {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %d %s tasks about doc %d after 10s, want %d; saw: %s; subscriber: %s",
				why, len(last), kind, docID, want, describeTasks(last),
				describeSubscriber(t, ctx, c))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// docLifecycleStatus returns the doc-lifecycle row of GET
// /api/v1/event-subscribers, failing the test if the server never ensured it.
func docLifecycleStatus(t *testing.T, ctx context.Context, c *cli.Client) model.EventSubscriberStatus {
	t.Helper()
	resp, _, err := c.EventSubscribers(ctx)
	if err != nil {
		t.Fatalf("GET /api/v1/event-subscribers: %v", err)
	}
	for _, sub := range resp.Subscribers {
		if sub.Name == docLifecycleSubscriberName {
			return sub
		}
	}
	t.Fatalf("event subscribers = %+v, want one named %q", resp.Subscribers, docLifecycleSubscriberName)
	return model.EventSubscriberStatus{}
}

// describeSubscriber renders the doc-lifecycle status for a failure message.
// It never fails the test itself — it is only ever called from a path that is
// already failing, and a second Fatalf there would hide the first.
func describeSubscriber(t *testing.T, ctx context.Context, c *cli.Client) string {
	t.Helper()
	resp, _, err := c.EventSubscribers(ctx)
	if err != nil {
		return fmt.Sprintf("unreadable (%v)", err)
	}
	for _, sub := range resp.Subscribers {
		if sub.Name == docLifecycleSubscriberName {
			return fmt.Sprintf("read=%d acked=%d lag=%d holder_pid=%d",
				sub.LastReadOffset, sub.LastAckedOffset, sub.Lag, sub.HolderPID)
		}
	}
	return fmt.Sprintf("no %q row (subscribers: %+v)", docLifecycleSubscriberName, resp.Subscribers)
}

// pollDocLifecycleCaughtUp polls until the doc-lifecycle subscriber's lag is
// 0 — it has acked every event below the commit horizon — and returns that
// status. Lag is a moving target (the watcher's own action events extend the
// log it consumes), so this is the only honest way to read "the loop has
// nothing left to do"; asserting lag == 0 once would be a coin flip.
func pollDocLifecycleCaughtUp(t *testing.T, ctx context.Context, c *cli.Client, why string) model.EventSubscriberStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := docLifecycleStatus(t, ctx, c)
		if st.Lag == 0 {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: doc-lifecycle lag = %d after 10s (read=%d acked=%d holder_pid=%d), want 0",
				why, st.Lag, st.LastReadOffset, st.LastAckedOffset, st.HolderPID)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// eventByID fetches one event through GET /api/v1/events using the exclusive
// id cursor — there is no by-id route, and a one-row window on the cursor is
// the public surface that answers the same question.
func eventByID(t *testing.T, ctx context.Context, c *cli.Client, id int64) model.Event {
	t.Helper()
	resp, _, err := c.ListEvents(ctx, cli.EventListFilter{After: id - 1, Limit: 1})
	if err != nil {
		t.Fatalf("GET /api/v1/events after %d: %v", id-1, err)
	}
	if len(resp.Events) != 1 || resp.Events[0].ID != id {
		t.Fatalf("GET /api/v1/events after %d = %+v, want exactly event %d", id-1, resp.Events, id)
	}
	return resp.Events[0]
}

// eventPayload decodes an event payload into a map, failing on anything else.
func eventPayload(t *testing.T, ev model.Event) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("decode payload of event %d (%s): %v", ev.ID, ev.Type, err)
	}
	return payload
}

// TestDocLifecycleWatcher drives spec 025 §15.4's doc-lifecycle subscriber
// end to end over public surfaces only, against the real subscriber loop
// started by api.NewServer off cfg.BackgroundCtx — no store writes, no
// handler called directly, no lode serve process.
//
// It proves the whole chain: submitting a spec mints exactly one ready
// `review` task about that document; submitting it again mints nothing;
// accepting it mints exactly one ready `design` task; and the minted task's
// timeline reaches, through its task.created event's prov:wasInformedBy, the
// wl:DocumentAccepted event that caused it. The subscriber surface
// (GET /api/v1/event-subscribers) shows the loop holding its advisory lock
// and caught up at the end.
func TestDocLifecycleWatcher(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)

	// The loop runs until this context is cancelled. It holds a dedicated
	// pooled connection for its advisory lock, so it must stop before the
	// database goes away: cleanups run LIFO and OpenTestStore registered the
	// database drop first, so the cleanup registered below runs before it.
	loopCtx, cancelLoop := context.WithCancel(context.Background())

	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		BackgroundCtx:  loopCtx,
		EventPoll:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	t.Cleanup(func() {
		cancelLoop()
		// Wait for the loop to drop its advisory lock rather than racing the
		// teardown. DROP DATABASE ... WITH (FORCE) would kill the session
		// anyway, but only after the loop has spent a poll interval logging
		// errors against a database being torn out from under it.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			resp, _, err := admin.EventSubscribers(context.Background())
			if err != nil {
				break
			}
			released := true
			for _, sub := range resp.Subscribers {
				if sub.Name == docLifecycleSubscriberName && sub.HolderPID != 0 {
					released = false
				}
			}
			if released {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		srv.Close()
	})

	// 1. A project, two actors with their own tokens, and a spec draft
	// assigned to the actor that will accept it. The other actor submits it,
	// which needs no assignee relationship — submission is an observation
	// anyone may make, acceptance is the assignee's deliberate act (025 §7).
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "doclife", Name: "Doc Lifecycle", Key: "DL",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	for _, id := range []string{"owner", "submitter"} {
		if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
			ID: id, Kind: "human", DisplayName: id,
		}); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	ownerTok, _, err := admin.CreateToken(ctx, "owner", "e2e doc lifecycle watcher", nil)
	if err != nil {
		t.Fatalf("create token for owner: %v", err)
	}
	submitterTok, _, err := admin.CreateToken(ctx, "submitter", "e2e doc lifecycle watcher", nil)
	if err != nil {
		t.Fatalf("create token for submitter: %v", err)
	}
	owner := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: ownerTok.Token})
	submitter := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: submitterTok.Token})

	doc, _, err := owner.CreateDoc(ctx, model.CreateDocInput{
		Project: "doclife", Kind: "spec", Number: 1, Slug: "watched-spec",
		Body: specSourceBody, Assignee: "owner",
	})
	if err != nil {
		t.Fatalf("create spec: %v", err)
	}
	if doc.Title != "Test Spec" || doc.Status != "draft" || doc.Assignee != "owner" {
		t.Fatalf("created doc = %+v, want a draft titled \"Test Spec\" assigned to owner", doc)
	}
	// Nothing references the document before anything happens to it: this is
	// the baseline the two mints below are measured against.
	if tasks := tasksAboutDoc(t, srv.URL, ownerTok.Token, doc.ID, ""); len(tasks) != 0 {
		t.Fatalf("tasks about doc %d before submit = %s, want none", doc.ID, describeTasks(tasks))
	}

	// 2. Submit. The document itself does not move — submission is an event,
	// not a status (025 §3) — and the watcher turns that event into exactly
	// one ready review task.
	submitted, _, err := submitter.SubmitDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("submit doc: %v", err)
	}
	if submitted.Status != "draft" || submitted.Version != doc.Version {
		t.Fatalf("doc after submit = status %q version %d, want draft version %d "+
			"(submission moves no document column)", submitted.Status, submitted.Version, doc.Version)
	}

	reviewTasks := pollTasksAboutDoc(t, ctx, admin, srv.URL, ownerTok.Token,
		doc.ID, "review", 1, "after the first submit")
	review := reviewTasks[0]
	if review.State != "ready" {
		t.Fatalf("review task %s state = %q, want ready", review.ID, review.State)
	}
	if review.Project != "doclife" {
		t.Fatalf("review task %s project = %q, want doclife (the document's project)", review.ID, review.Project)
	}
	if review.AboutDoc != doc.ID {
		t.Fatalf("review task %s about_doc = %d, want %d", review.ID, review.AboutDoc, doc.ID)
	}
	if want := "Review: " + doc.Title; review.Title != want {
		t.Fatalf("review task %s title = %q, want %q", review.ID, review.Title, want)
	}
	if review.CreatedBy != "watcher" {
		t.Fatalf("review task %s created_by = %q, want watcher (the mechanism, not the submitter)",
			review.ID, review.CreatedBy)
	}
	if tasks := tasksAboutDoc(t, srv.URL, ownerTok.Token, doc.ID, ""); len(tasks) != 1 {
		t.Fatalf("tasks about doc %d after submit = %s, want only the review task", doc.ID, describeTasks(tasks))
	}

	// 3. Submit the same version again. Two independent things must hold, and
	// asserting only the second would be worthless:
	//
	//   a) The log absorbed it. wl:DocumentSubmitted's external id is derived
	//      from the document IRI and version, so the resubmit collides on
	//      (source, external_id) and inserts nothing — layer 1 of §15.4's
	//      idempotency, visible here as the submitted-event count staying 1.
	//   b) The loop then had a full pass at the log and still minted nothing.
	//      A second task listing that returns immediately would prove nothing
	//      (the loop may simply not have run yet), so the wait is on the
	//      subscriber's own lag reaching 0: lag is horizon-max-id minus acked,
	//      so lag == 0 after the resubmit means the subscriber has read and
	//      acked every event visible at that point — there is no unprocessed
	//      event left that could still mint a second task. Only then is the
	//      count re-read.
	if _, _, err := submitter.SubmitDoc(ctx, doc.ID); err != nil {
		t.Fatalf("second submit: %v", err)
	}
	submits := pollEventListE2E(t, ctx, admin, cli.EventListFilter{Type: eventbus.TypeDocumentSubmitted}, 1)
	if len(submits) != 1 {
		t.Fatalf("%s events = %d, want 1 (the resubmit is absorbed by the deterministic external id)",
			eventbus.TypeDocumentSubmitted, len(submits))
	}
	pollDocLifecycleCaughtUp(t, ctx, admin, "after the second submit")
	if tasks := tasksAboutDoc(t, srv.URL, ownerTok.Token, doc.ID, ""); len(tasks) != 1 || tasks[0].ID != review.ID {
		t.Fatalf("tasks about doc %d after the second submit = %s, want only %s",
			doc.ID, describeTasks(tasks), review.ID)
	}

	// 4. Close the review, then accept the spec as its assignee. With no open
	// design task referencing the document, the second rule mints one.
	abandoned, _, err := owner.AbandonTask(ctx, review.ID)
	if err != nil {
		t.Fatalf("abandon review task %s: %v", review.ID, err)
	}
	if abandoned.State != "abandoned" {
		t.Fatalf("review task %s state = %q, want abandoned", review.ID, abandoned.State)
	}
	accept, _, err := owner.AcceptDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("accept doc as its assignee: %v", err)
	}
	if accept.Doc.Status != "accepted" {
		t.Fatalf("doc status after accept = %q, want accepted", accept.Doc.Status)
	}

	designTasks := pollTasksAboutDoc(t, ctx, admin, srv.URL, ownerTok.Token,
		doc.ID, "design", 1, "after accepting the spec")
	design := designTasks[0]
	if design.State != "ready" {
		t.Fatalf("design task %s state = %q, want ready", design.ID, design.State)
	}
	if design.Project != "doclife" {
		t.Fatalf("design task %s project = %q, want doclife (the document's project)", design.ID, design.Project)
	}
	if design.AboutDoc != doc.ID {
		t.Fatalf("design task %s about_doc = %d, want %d", design.ID, design.AboutDoc, doc.ID)
	}
	if want := "Plan: decompose " + doc.Title + " into plans"; design.Title != want {
		t.Fatalf("design task %s title = %q, want %q", design.ID, design.Title, want)
	}
	if design.CreatedBy != "watcher" {
		t.Fatalf("design task %s created_by = %q, want watcher", design.ID, design.CreatedBy)
	}

	// 5. Provenance, reachable from the task and nowhere else needed: the
	// design task's timeline carries one state entry (its mint), attributed
	// to an event id; that event is the watcher's task.created; and its
	// payload's prov:wasInformedBy names the wl:DocumentAccepted event that
	// caused it. That is §15.4's chain, walked entirely over HTTP.
	tl, _, err := owner.Timeline(ctx, design.ID)
	if err != nil {
		t.Fatalf("timeline of %s: %v", design.ID, err)
	}
	var mintEntry *model.TimelineEntry
	for i := range tl.Timeline {
		if tl.Timeline[i].Type == "state" {
			if mintEntry != nil {
				t.Fatalf("timeline of %s has more than one state entry: %+v", design.ID, tl.Timeline)
			}
			mintEntry = &tl.Timeline[i]
		}
	}
	if mintEntry == nil {
		t.Fatalf("timeline of %s = %+v, want a state entry for the mint", design.ID, tl.Timeline)
	}
	if mintEntry.EventID == 0 {
		t.Fatalf("timeline state entry of %s carries no event_id: %+v", design.ID, *mintEntry)
	}

	accepts := pollEventListE2E(t, ctx, admin, cli.EventListFilter{Type: eventbus.TypeDocumentAccepted}, 1)
	if len(accepts) != 1 {
		t.Fatalf("%s events = %d, want exactly 1", eventbus.TypeDocumentAccepted, len(accepts))
	}
	acceptEvent := accepts[0]

	mintEvent := eventByID(t, ctx, owner, mintEntry.EventID)
	if mintEvent.Type != "task.created" {
		t.Fatalf("mint event %d type = %q, want task.created", mintEvent.ID, mintEvent.Type)
	}
	if mintEvent.Source != "watcher" {
		t.Fatalf("mint event %d source = %q, want watcher", mintEvent.ID, mintEvent.Source)
	}
	// The external id is the (event_id, subscriber) idempotency key of §15.4:
	// it ends in the id of the event that caused the mint, which is the same
	// event prov:wasInformedBy names below.
	if want := "doc-lifecycle:plan-on-accept:" + strconv.FormatInt(acceptEvent.ID, 10); mintEvent.ExternalID != want {
		t.Fatalf("mint event %d external_id = %q, want %q", mintEvent.ID, mintEvent.ExternalID, want)
	}
	mintPayload := eventPayload(t, mintEvent)
	if got := mintPayload["rule"]; got != "plan-on-accept" {
		t.Fatalf("mint event %d rule = %v, want plan-on-accept", mintEvent.ID, got)
	}
	if want := "wlid:event/" + strconv.FormatInt(acceptEvent.ID, 10); mintPayload["prov:wasInformedBy"] != want {
		t.Fatalf("mint event %d prov:wasInformedBy = %v, want %q (the wl:DocumentAccepted event)",
			mintEvent.ID, mintPayload["prov:wasInformedBy"], want)
	}
	// Both ends of the chain must name the same document, or the provenance
	// links a task to an unrelated acceptance.
	acceptPayload := eventPayload(t, acceptEvent)
	if mintPayload["doc"] != acceptPayload["wl:subject"] {
		t.Fatalf("mint event doc = %v, wl:DocumentAccepted subject = %v, want the same document IRI",
			mintPayload["doc"], acceptPayload["wl:subject"])
	}

	// 6. The operator surface: the loop is holding its advisory lock in this
	// process and has consumed the whole log.
	status := pollDocLifecycleCaughtUp(t, ctx, admin, "at the end of the run")
	if status.HolderPID == 0 {
		t.Fatalf("doc-lifecycle holder_pid = 0, want the pid of the session holding the advisory lock (%+v)", status)
	}
	if status.LastAckedOffset < mintEvent.ID {
		t.Fatalf("doc-lifecycle acked offset = %d, want at least the mint event %d (%+v)",
			status.LastAckedOffset, mintEvent.ID, status)
	}
}
