package api_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// otlpLogsBody builds a one-resource OTLP/JSON log batch carrying two
// records. An empty task leaves out worklode.task.id: the unattributed case
// that is forwarded but never stored (spec 071 §1).
func otlpLogsBody(task string) []byte {
	res := `{"key":"service.name","value":{"stringValue":"claude-code"}},` +
		`{"key":"session.id","value":{"stringValue":"sess-1"}}`
	if task != "" {
		res = `{"key":"worklode.task.id","value":{"stringValue":"` + task + `"}},` + res
	}
	return []byte(`{"resourceLogs":[{"resource":{"attributes":[` + res + `]},` +
		`"scopeLogs":[{"logRecords":[` +
		`{"timeUnixNano":"1700000000000000000","attributes":[` +
		`{"key":"event.name","value":{"stringValue":"claude_code.tool_result"}},` +
		`{"key":"tool_name","value":{"stringValue":"Bash"}},` +
		`{"key":"success","value":{"boolValue":true}},` +
		`{"key":"duration_ms","value":{"intValue":"1234"}}],` +
		`"body":{"stringValue":"tool output that must never be stored"}},` +
		`{"timeUnixNano":"1700000001000000000","attributes":[` +
		`{"key":"event.name","value":{"stringValue":"claude_code.api_error"}},` +
		`{"key":"status_code","value":{"intValue":"500"}}]}]}]}]}`)
}

// postOTLP posts raw bytes to the ingest route. doReq is no use here: the
// route takes a body the test controls verbatim and a content type that is
// part of what is under test.
func postOTLP(t *testing.T, h http.Handler, token, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/otlp/v1/logs", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// seedActivityTask creates a project and one task, returning its id.
func seedActivityTask(t *testing.T, st *store.Store, h http.Handler, token, project string) string {
	t.Helper()
	createProject(t, st, project)
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": project, "title": "T", "priority": "high", "kind": "feature",
	})
	return task["id"].(string)
}

// forwarded is one batch the upstream gateway received.
type forwarded struct {
	body        []byte
	auth        string
	contentType string
}

// newUpstream starts a stand-in otel-gateway and returns it with the channel
// its handler publishes each received batch on.
func newUpstream(t *testing.T) (*httptest.Server, chan forwarded) {
	t.Helper()
	got := make(chan forwarded, 4)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- forwarded{body: b, auth: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type")}
	}))
	t.Cleanup(up.Close)
	return up, got
}

func TestIngestOTLPLogsStoresAttributedRecords(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	id := seedActivityTask(t, st, h, token, "proj")

	rr := postOTLP(t, h, token, "application/json", otlpLogsBody(id))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := strings.TrimSpace(rr.Body.String()); got != "{}" {
		t.Fatalf("body = %q, want {} (an empty ExportLogsServiceResponse)", got)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q, want application/json", ct)
	}

	rows, err := st.TaskActivity(t.Context(), id, 0, 10)
	if err != nil {
		t.Fatalf("TaskActivity: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("stored %d rows, want 2", len(rows))
	}
	// Keyed by event so the assertions below read as what they check,
	// rather than by a position in the batch.
	byEvent := map[string]model.TaskActivity{}
	for _, row := range rows {
		byEvent[row.Event] = row
	}
	if ae := byEvent["claude_code.api_error"]; ae.Attrs.StatusCode != 500 {
		t.Fatalf("api_error row = %+v, want status_code 500", ae)
	}
	tr := byEvent["claude_code.tool_result"]
	if tr.Attrs.ToolName != "Bash" || tr.Attrs.DurationMS != 1234 {
		t.Fatalf("tool_result row = %+v, want tool_name Bash and duration_ms 1234", tr)
	}
	if tr.Attrs.Success == nil || !*tr.Attrs.Success {
		t.Fatalf("success = %v, want true", tr.Attrs.Success)
	}
	if tr.Task != id || tr.Actor != "alice" || tr.Agent != "claude-code" || tr.Session != "sess-1" {
		t.Fatalf("attribution = %+v, want task %s, actor alice, agent claude-code, session sess-1", tr, id)
	}
}

func TestIngestOTLPLogsForwardsRawBody(t *testing.T) {
	t.Parallel()
	up, got := newUpstream(t)
	st, h, token := newTestServerWithConfig(t, api.Config{
		WebOpen:           true,
		OTLPUpstream:      up.URL,
		OTLPUpstreamToken: "gw-token",
		BackgroundCtx:     t.Context(),
	})
	id := seedActivityTask(t, st, h, token, "proj")

	body := otlpLogsBody(id)
	if rr := postOTLP(t, h, token, "application/json", body); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}

	select {
	case f := <-got:
		if !bytes.Equal(f.body, body) {
			t.Fatalf("upstream body = %s, want the request body verbatim", f.body)
		}
		if f.auth != "Bearer gw-token" {
			t.Fatalf("upstream authorization = %q, want Bearer gw-token", f.auth)
		}
		if f.contentType != "application/json" {
			t.Fatalf("upstream content type = %q, want application/json", f.contentType)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream received nothing")
	}
}

func TestIngestOTLPLogsUnattributedRecordsAreForwardedNotStored(t *testing.T) {
	t.Parallel()
	up, got := newUpstream(t)
	st, h, token := newTestServerWithConfig(t, api.Config{
		WebOpen:           true,
		OTLPUpstream:      up.URL,
		OTLPUpstreamToken: "gw-token",
		BackgroundCtx:     t.Context(),
	})
	id := seedActivityTask(t, st, h, token, "proj")

	body := otlpLogsBody("")
	if rr := postOTLP(t, h, token, "application/json", body); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	select {
	case f := <-got:
		if !bytes.Equal(f.body, body) {
			t.Fatalf("upstream body = %s, want the request body verbatim", f.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream received nothing; an unattributed batch is still forwarded")
	}

	rows, err := st.TaskActivity(t.Context(), id, 0, 10)
	if err != nil {
		t.Fatalf("TaskActivity: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("stored %d rows, want 0 for a batch with no task id", len(rows))
	}
}

func TestIngestOTLPLogsRefusesForeignTaskOnScopedToken(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	mine := seedActivityTask(t, st, h, token, "proj")
	other := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Other", "priority": "high", "kind": "feature",
	})["id"].(string)

	scoped := mintTaskToken(t, h, token, mine, nil).Token
	rr := postOTLP(t, h, scoped, "application/json", otlpLogsBody(other))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body %s; want 403", rr.Code, rr.Body.String())
	}
	rows, err := st.TaskActivity(t.Context(), other, 0, 10)
	if err != nil {
		t.Fatalf("TaskActivity: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("stored %d rows against the foreign task, want 0", len(rows))
	}
}

func TestIngestOTLPLogsAcceptsOwnTaskOnScopedToken(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	id := seedActivityTask(t, st, h, token, "proj")

	scoped := mintTaskToken(t, h, token, id, nil)
	if rr := postOTLP(t, h, scoped.Token, "application/json", otlpLogsBody(id)); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	rows, err := st.TaskActivity(t.Context(), id, 0, 10)
	if err != nil {
		t.Fatalf("TaskActivity: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("stored %d rows, want 2", len(rows))
	}
	if rows[0].Actor != scoped.Actor {
		t.Fatalf("actor = %q, want the token's actor %q", rows[0].Actor, scoped.Actor)
	}
}

func TestIngestOTLPLogsRefusesProtobuf(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	id := seedActivityTask(t, st, h, token, "proj")

	rr := postOTLP(t, h, token, "application/x-protobuf", otlpLogsBody(id))
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, body %s; want 415", rr.Code, rr.Body.String())
	}
}

func TestIngestOTLPLogsRefusesUndecodableBody(t *testing.T) {
	t.Parallel()
	up, got := newUpstream(t)
	st, h, token := newTestServerWithConfig(t, api.Config{
		WebOpen:           true,
		OTLPUpstream:      up.URL,
		OTLPUpstreamToken: "gw-token",
		BackgroundCtx:     t.Context(),
	})
	id := seedActivityTask(t, st, h, token, "proj")

	if rr := postOTLP(t, h, token, "application/json", []byte(`{"resourceLogs":`)); rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	// A good batch after it: the first thing upstream sees is that batch,
	// so the undecodable one was dropped rather than forwarded.
	good := otlpLogsBody(id)
	if rr := postOTLP(t, h, token, "application/json", good); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	select {
	case f := <-got:
		if !bytes.Equal(f.body, good) {
			t.Fatalf("upstream body = %s, want the decodable batch", f.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream received nothing")
	}
}

func TestIngestOTLPLogsRefusesOversizedBody(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	id := seedActivityTask(t, st, h, token, "proj")

	// A record attribute padded past the 4 MiB cap.
	pad := strings.Repeat("x", 5<<20)
	body := bytes.Replace(otlpLogsBody(id), []byte(`"Bash"`), []byte(`"`+pad+`"`), 1)
	rr := postOTLP(t, h, token, "application/json", body)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body %s; want 413", rr.Code, rr.Body.String())
	}
}

// --- the task page's Activity card and its stream (spec 071 §4) -------------

// activityRecorder cancels the request's context once the stream body holds
// the awaited text. A follow never ends on its own, so a synchronous
// ServeHTTP needs the response itself to say when the test has what it asked
// for — the same shape progressEventsRecorder uses, and for the same reason:
// cancelling on "the first write of any kind" would race the heartbeat the
// handler correctly sends while it has nothing to say.
type activityRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	await  string
	done   bool
}

func (w *activityRecorder) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if !w.done && strings.Contains(w.Body.String(), w.await) {
		w.done = true
		w.cancel()
	}
	return n, err
}

// openActivityStream follows one task's activity until await appears in the
// body or ctx is done. lastEventID, when set, resumes through the header a
// reconnecting EventSource sends; otherwise the follow starts at ?after=0,
// which replays what the task already has.
func openActivityStream(t *testing.T, ctx context.Context, h http.Handler, task, lastEventID, await string) *activityRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	url := "/tasks/" + task + "/activity/events"
	if lastEventID == "" {
		url += "?after=0"
	}
	req := httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	rec := &activityRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, await: await}
	h.ServeHTTP(rec, req)
	return rec
}

// TestTaskPageShowsActivityCard covers the read side of §4 end to end: a
// batch ingested through the OTLP route reaches the task page as rows in the
// Activity card, each carrying the summary internal/api derives from the
// allowlisted attributes and the event name with its claude_code. prefix
// stripped.
func TestTaskPageShowsActivityCard(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	id := seedActivityTask(t, st, h, token, "proj")

	if rr := postOTLP(t, h, token, "application/json", otlpLogsBody(id)); rr.Code != http.StatusOK {
		t.Fatalf("ingest status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, h, "GET", "/tasks/"+id, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`id="activity"`,
		`<ol class="activity">`,
		`>tool_result<`,
		`Bash ok 1.2s`,
		`>api_error<`,
		`sess-1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("task page missing %q:\n%s", want, body)
		}
	}
	// The event name is the row's own cell, so the summary never repeats it.
	if strings.Contains(body, "claude_code.tool_result") {
		t.Fatalf("task page shows the raw event name; the claude_code. prefix should be stripped:\n%s", body)
	}
}

// TestTaskPageOmitsActivityCardWhenThereIsNothing pins the honest empty
// state: no rows and no session means no card at all, rather than an empty
// one implying the agent did nothing.
func TestTaskPageOmitsActivityCardWhenThereIsNothing(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	id := seedActivityTask(t, st, h, token, "proj")

	rr := doReq(t, h, "GET", "/tasks/"+id, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); strings.Contains(body, `id="activity"`) {
		t.Fatalf("task page rendered an Activity card for a task with no rows and no session:\n%s", body)
	}
}

// TestTaskActivityStreamSendsRenderedRows covers the stream: each frame's id
// is the row id a reconnecting client sends back, and its data is the same
// <li> the page renders, so the script inserts markup it never built.
func TestTaskActivityStreamSendsRenderedRows(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	st, h, admin, token := newTestServerWithAdmin(t)
	id := seedActivityTask(t, st, h, token, "proj")

	if rr := postOTLP(t, h, token, "application/json", otlpLogsBody(id)); rr.Code != http.StatusOK {
		t.Fatalf("ingest status = %d, body %s", rr.Code, rr.Body.String())
	}
	rows, err := st.TaskActivity(t.Context(), id, 0, 10)
	if err != nil {
		t.Fatalf("TaskActivity: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("stored %d rows, want 2", len(rows))
	}
	newest, oldest := rows[0], rows[1] // newest first

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	rec := openActivityStream(t, ctx, h, id, "", fmt.Sprintf("id: %d", newest.ID))
	body := rec.Body.String()

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q, want text/event-stream", ct)
	}
	for _, want := range []string{
		fmt.Sprintf("id: %d\nevent: activity\n", oldest.ID),
		fmt.Sprintf("id: %d\nevent: activity\n", newest.ID),
		fmt.Sprintf(`data: <li data-id="%d"`, oldest.ID),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream body missing %q:\n%s", want, body)
		}
	}
	// Oldest first: the page prepends each frame, so a burst arriving in id
	// order ends up newest-first on the card.
	if strings.Index(body, fmt.Sprintf("id: %d", oldest.ID)) > strings.Index(body, fmt.Sprintf("id: %d", newest.ID)) {
		t.Fatalf("stream sent the newer row first:\n%s", body)
	}

	want := fmt.Sprintf("worklode_activity_stream_frames_sent_total %d", strings.Count(body, "event: activity"))
	metrics := doReq(t, admin, "GET", "/metrics", "", nil)
	if !strings.Contains(metrics.Body.String(), want) {
		t.Errorf("%q not found in /metrics:\n%s", want, metrics.Body.String())
	}
}

// TestTaskActivityStreamResumesFromLastEventID is what makes an EventSource
// reconnect lossless and duplicate-free: the row the client already has is
// behind the cursor it sends back, and only what followed it is replayed.
func TestTaskActivityStreamResumesFromLastEventID(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	st, h, token := newTestServer(t)
	id := seedActivityTask(t, st, h, token, "proj")

	if rr := postOTLP(t, h, token, "application/json", otlpLogsBody(id)); rr.Code != http.StatusOK {
		t.Fatalf("ingest status = %d, body %s", rr.Code, rr.Body.String())
	}
	rows, err := st.TaskActivity(t.Context(), id, 0, 10)
	if err != nil {
		t.Fatalf("TaskActivity: %v", err)
	}
	newest, oldest := rows[0], rows[1]

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	rec := openActivityStream(t, ctx, h, id, strconv.FormatInt(oldest.ID, 10), fmt.Sprintf("id: %d", newest.ID))
	body := rec.Body.String()

	if strings.Contains(body, fmt.Sprintf("id: %d\n", oldest.ID)) {
		t.Fatalf("stream replayed the row the client already had (id %d):\n%s", oldest.ID, body)
	}
}

// TestTaskActivityStreamUnknownTaskIs404 keeps the stream from answering
// "does this task exist" differently from every other task route.
func TestTaskActivityStreamUnknownTaskIs404(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	seedActivityTask(t, st, h, token, "proj")

	rr := doReq(t, h, "GET", "/tasks/WL-NOPE/activity/events", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body %s", rr.Code, rr.Body.String())
	}
}
