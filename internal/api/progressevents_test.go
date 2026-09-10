package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// progressEventsRecorder wraps httptest.NewRecorder and cancels the request's
// context once the awaited text has been written — not on the first write of
// any kind. ListEvents is commit-horizon bounded (see
// internal/store/events.go), and that horizon is cluster-wide: a concurrent
// test elsewhere on the same Postgres instance can hold it back past the
// stream's heartbeat interval, so the just-recorded event can still be
// invisible on the first few polls. The handler correctly emits a heartbeat
// comment while it waits — normal SSE keep-alive, harmless to any consumer —
// so cancelling on "the first write" raced the heartbeat.
//
// await is a substring of the frame the caller is waiting for, because one
// poll can carry several frames and the awaited one is rarely the first: a
// project's log holds a doc.created and a wl:DocumentAccepted before anything
// a task did. Cancelling on "any frame" would end the stream on a frame the
// caller is not asking about, whenever the horizon happens to split the poll.
type progressEventsRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	await  string
	done   bool
}

func (w *progressEventsRecorder) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if !w.done && strings.Contains(w.Body.String(), w.await) {
		w.done = true
		w.cancel()
	}
	return n, err
}

// openProgressEvents issues one GET .../progress/events?after=0 and returns
// once a frame containing await cancels the request, or once ctx itself is
// done — whichever comes first. await "" waits for any progress frame. A
// caller that expects no frame at all supplies an already-timeout-bound ctx;
// a caller that expects one should still bound ctx, so a genuine handler
// regression fails the test instead of hanging it.
func openProgressEvents(t *testing.T, ctx context.Context, h http.Handler, project, await string) *progressEventsRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	if await == "" {
		await = "event: progress"
	}
	req := httptest.NewRequest(http.MethodGet, "/projects/"+project+"/progress/events?after=0", nil).WithContext(ctx)
	rec := &progressEventsRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, await: await}
	h.ServeHTTP(rec, req)
	return rec
}

// progressFramesOfType decodes every data: line in body and returns the
// frames whose event type is typ, in the order the stream wrote them.
func progressFramesOfType(t *testing.T, body, typ string) []model.ProgressEventFrame {
	t.Helper()
	var out []model.ProgressEventFrame
	rest := body
	for {
		_, after, ok := strings.Cut(rest, "data: ")
		if !ok {
			return out
		}
		line, tail, _ := strings.Cut(after, "\n")
		rest = tail
		var frame model.ProgressEventFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("decode frame data %q: %v", line, err)
		}
		if frame.Event == typ {
			out = append(out, frame)
		}
	}
}

// progressFrameFor is progressFramesOfType narrowed to the plan a frame
// names — plan 0 meaning "whatever it names" — for a caller that expects
// exactly one such frame. One poll carries a frame per document it touched,
// and only one of them is the frame under test.
func progressFrameFor(t *testing.T, body, typ string, plan int64) model.ProgressEventFrame {
	t.Helper()
	var frames []model.ProgressEventFrame
	for _, f := range progressFramesOfType(t, body, typ) {
		if plan == 0 || f.Plan == plan {
			frames = append(frames, f)
		}
	}
	if len(frames) != 1 {
		t.Fatalf("got %d %s frames for plan %d, want 1, in body %q", len(frames), typ, plan, body)
	}
	return frames[0]
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
	rec := openProgressEvents(t, frameCtx, h, "proj", "task.transition")
	body := rec.Body.String()
	if !strings.Contains(body, "event: progress") {
		t.Fatalf("no progress frame in body: %q", body)
	}
	frame := progressFrameFor(t, body, "task.transition", 0)
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

	// The counter is the number of frames this one stream wrote, which is
	// every "data: " line in its body — the document events on the spec and
	// the plan among them (WL-768), not the task transition alone.
	want := fmt.Sprintf("worklode_progress_stream_frames_sent_total %d", strings.Count(body, "data: "))
	metrics := doReq(t, admin, "GET", "/metrics", "", nil)
	if !strings.Contains(metrics.Body.String(), want) {
		t.Errorf("%q not found in /metrics:\n%s", want, metrics.Body.String())
	}

	// The same event, followed from a project it is not about, yields
	// nothing: no frame reaches "other"'s stream within a bounded wait.
	shortCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	other := openProgressEvents(t, shortCtx, h, "other", "")
	if body := other.Body.String(); strings.Contains(body, "event: progress") {
		t.Errorf("other project's stream got a frame it should not see: %q", body)
	}
}

// TestProgressEventsDocumentFrames is WL-SPEC-66 §5.1's other half: a
// document event on a plan reaches the stream as a frame with task empty and
// plan set. Both events a plan's life starts with are checked, because before
// WL-768 each failed to name its document for a different reason —
// doc.created recorded id 0 (the row does not exist until apply runs), and
// the typed wl:DocumentAccepted names its subject by IRI and nothing else.
// progressFrames drops a touch it cannot resolve to an id, so neither event
// produced a frame at all.
//
// Two streams, because one document's events collapse into one frame per
// poll: the second is opened only after the accept, so each event is the
// latest one naming the plan when its stream reads it.
func TestProgressEventsDocumentFrames(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	st, h, _, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")

	spec := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 66, Slug: "066-progress",
		Body: progressSpecBody,
	})
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "066-progress-plan",
		Body: progressPlanBody,
	})

	// The plan's own doc.created. Awaited by the plan id rather than the
	// event type, because the spec's doc.created is in the same poll and is
	// not the frame under test. See TestProgressEventsHandler on why these
	// waits are bounded at 20s.
	created := progressDocFrame(t, h, "proj", fmt.Sprintf(`"plan":%d`, plan.ID), "doc.created", plan.ID)
	if created.Task != "" {
		t.Errorf("doc.created frame task = %q, want empty", created.Task)
	}
	if created.Plan != plan.ID {
		t.Errorf("doc.created frame plan = %d, want the plan %d", created.Plan, plan.ID)
	}
	if !slices.Contains(created.Specs, spec.ID) {
		t.Errorf("doc.created frame specs = %v, want to include spec doc %d", created.Specs, spec.ID)
	}

	if rr := doReq(t, h, "POST", docPath(plan.ID, "/accept"), token, nil); rr.Code != http.StatusOK {
		t.Fatalf("accept plan status = %d, body %s", rr.Code, rr.Body.String())
	}

	accepted := progressDocFrame(t, h, "proj", "wl:DocumentAccepted", "wl:DocumentAccepted", plan.ID)
	if accepted.Task != "" {
		t.Errorf("wl:DocumentAccepted frame task = %q, want empty", accepted.Task)
	}
	if accepted.Plan != plan.ID {
		t.Errorf("wl:DocumentAccepted frame plan = %d, want the plan %d", accepted.Plan, plan.ID)
	}
	if !slices.Contains(accepted.Specs, spec.ID) {
		t.Errorf("wl:DocumentAccepted frame specs = %v, want to include spec doc %d",
			accepted.Specs, spec.ID)
	}
}

// TestProgressEventsDeliveryFansOut is WL-775: a delivery event transitions a
// set of tasks at once, so its payload names them in "tasks" and the stream
// must emit one frame per task. Before the fix the touch carried a kind and
// no id, contributed no frame at all, and §5.3's per-cell landed pulse never
// fired on the delivery path.
func TestProgressEventsDeliveryFansOut(t *testing.T) {
	t.Parallel()
	api.SetStreamPollInterval(t, 20*time.Millisecond)
	api.SetStreamHeartbeatInterval(t, 50*time.Millisecond)
	st, h, _, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")

	createDocViaAPI(t, h, token, model.CreateDocInput{
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
	if len(accepted.Tasks) < 2 {
		t.Fatalf("accepting the plan minted %d tasks, want at least 2", len(accepted.Tasks))
	}
	moved := []string{accepted.Tasks[0].ID, accepted.Tasks[1].ID}

	payload, err := json.Marshal(map[string]any{"tasks": moved})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RecordEvent(context.Background(), "system", "progress-delivery-1",
		"deployment_status.success", payload, nil); err != nil {
		t.Fatalf("record deployment_status.success: %v", err)
	}

	// Bounded for the same reason as TestProgressEventsHandler's wait.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rec := openProgressEvents(t, ctx, h, "proj", moved[1])
	body := rec.Body.String()

	var got []string
	for _, f := range progressFramesOfType(t, body, "deployment_status.success") {
		got = append(got, f.Task)
	}
	slices.Sort(got)
	want := slices.Clone(moved)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("delivery frames named tasks %v, want one frame per transitioned task %v, in body %q",
			got, want, body)
	}
}

// progressDocFrame opens one stream, waits for a frame containing await, and
// returns the single frame of type typ in what it read.
func progressDocFrame(t *testing.T, h http.Handler, project, await, typ string, plan int64) model.ProgressEventFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rec := openProgressEvents(t, ctx, h, project, await)
	return progressFrameFor(t, rec.Body.String(), typ, plan)
}
