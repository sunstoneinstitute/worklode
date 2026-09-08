package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// progressEventsRecorder wraps httptest.NewRecorder and cancels the request's
// context once an actual "event: progress" frame has been written — not on
// the first write of any kind. ListEvents is commit-horizon bounded (see
// internal/store/events.go), and that horizon is cluster-wide: a concurrent
// test elsewhere on the same Postgres instance can hold it back past the
// stream's heartbeat interval, so the just-recorded event can still be
// invisible on the first few polls. The handler correctly emits a heartbeat
// comment while it waits — normal SSE keep-alive, harmless to any consumer —
// so cancelling on "the first write" raced the heartbeat. Waiting for the
// frame's own text fires exactly when the frame lands, however many
// heartbeats precede it.
type progressEventsRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	done   bool
}

func (w *progressEventsRecorder) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if !w.done && strings.Contains(w.Body.String(), "event: progress") {
		w.done = true
		w.cancel()
	}
	return n, err
}

// openProgressEvents issues one GET .../progress/events?after=0 and returns
// once a progress frame cancels the request, or once ctx itself is done —
// whichever comes first. A caller that expects no frame at all supplies an
// already-timeout-bound ctx; a caller that expects one should still bound
// ctx, so a genuine handler regression fails the test instead of hanging it.
func openProgressEvents(t *testing.T, ctx context.Context, h http.Handler, project string) *progressEventsRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	req := httptest.NewRequest(http.MethodGet, "/projects/"+project+"/progress/events?after=0", nil).WithContext(ctx)
	rec := &progressEventsRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	h.ServeHTTP(rec, req)
	return rec
}

// firstProgressFrame decodes the data: line of the first (only, in these
// tests) SSE frame in body.
func firstProgressFrame(t *testing.T, body string) model.ProgressEventFrame {
	t.Helper()
	_, rest, ok := strings.Cut(body, "data: ")
	if !ok {
		t.Fatalf("no data: line in body %q", body)
	}
	line, _, _ := strings.Cut(rest, "\n")
	var frame model.ProgressEventFrame
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatalf("decode frame data %q: %v", line, err)
	}
	return frame
}

// TestProgressEventsHandler covers the route end to end: a task.transition
// recorded for a task minted from an accepted plan reaches this project's
// stream as a frame naming the spec that plan covers, and the frames-sent
// counter moves; the same event is invisible to a second project's stream,
// which is §5.1's project scoping (drop refs whose Project is not the
// route's).
func TestProgressEventsHandler(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 66, Slug: "066-progress",
		Body: progressSpecBody,
	})
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "066-progress-plan",
		Body: progressPlanBody,
	})
	rr := doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("accept plan status = %d, body %s", rr.Code, rr.Body.String())
	}
	var accepted model.AcceptDocResponse
	decodeInto(t, rr, &accepted)
	if len(accepted.Tasks) == 0 {
		t.Fatalf("accepting the plan minted no tasks")
	}
	taskID := accepted.Tasks[0].ID

	if _, _, err := st.RecordEvent(context.Background(), "system", "progress-events-1",
		"task.transition", []byte(`{"task":"`+taskID+`"}`), nil); err != nil {
		t.Fatalf("record task.transition: %v", err)
	}

	// Bounded, not indefinite: a real handler regression must fail the test,
	// not hang it. 20s matches store.AwaitCommitHorizon's own deadline for
	// the same cluster-wide-horizon wait, since a slow horizon is the one
	// legitimate reason this takes a while (see progressEventsRecorder).
	frameCtx, cancelFrame := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelFrame()
	rec := openProgressEvents(t, frameCtx, h, "proj")
	body := rec.Body.String()
	if !strings.Contains(body, "event: progress") {
		t.Fatalf("no progress frame in body: %q", body)
	}
	frame := firstProgressFrame(t, body)
	if frame.Task != taskID {
		t.Errorf("frame task = %q, want %q", frame.Task, taskID)
	}
	if frame.Event != "task.transition" {
		t.Errorf("frame event = %q, want task.transition", frame.Event)
	}
	found := false
	for _, s := range frame.Specs {
		if s == spec.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("frame specs = %v, want to include spec doc %d", frame.Specs, spec.ID)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil)
	if !strings.Contains(metrics.Body.String(), "worklode_progress_stream_frames_sent_total 1") {
		t.Errorf("worklode_progress_stream_frames_sent_total = 1 not found in /metrics:\n%s", metrics.Body.String())
	}

	// The same event, followed from a project it is not about, yields
	// nothing: no frame reaches "other"'s stream within a bounded wait.
	shortCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	other := openProgressEvents(t, shortCtx, h, "other")
	if body := other.Body.String(); strings.Contains(body, "event: progress") {
		t.Errorf("other project's stream got a frame it should not see: %q", body)
	}
}
