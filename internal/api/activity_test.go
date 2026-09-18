package api_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
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
	// Keyed by event, not by position: the store assigns ids per batch and
	// does not promise they follow the batch's own order.
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
