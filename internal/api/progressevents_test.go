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
// context the moment the first frame is written. The handler's poll loop
// checks ctx.Done() only after a write, so this makes one ServeHTTP call
// return synchronously right after its first frame — no goroutine, no sleep,
// and no dependency on the poll interval.
type progressEventsRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	wrote  bool
}

func (w *progressEventsRecorder) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if !w.wrote {
		w.wrote = true
		w.cancel()
	}
	return n, err
}

// openProgressEvents issues one GET .../progress/events?after=0 and returns
// once the handler's first write (a frame) cancels the request, or once ctx
// itself is done — whichever comes first. A caller that expects no frame at
// all supplies an already-timeout-bound ctx.
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

	rec := openProgressEvents(t, context.Background(), h, "proj")
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
