package hookrun

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// allEvents is the guarded event set (unknown events are handled separately).
var allEvents = []string{"session-start", "session-end", "pre-commit",
	"worktree-create", "worktree-remove", "heartbeat", "worktree-enter", "worktree-exit"}

// recordingServer stands in for the backbone and flags ANY inbound request.
// The guard-NOP tests assert it is never hit.
type recordingServer struct {
	mu       sync.Mutex
	requests []string
}

func (r *recordingServer) hit() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests) > 0
}

// newRecordingServer starts a server that records every request and points
// LODE_SERVER/LODE_TOKEN at it.
func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rec := &recordingServer{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec.mu.Lock()
		rec.requests = append(rec.requests, req.Method+" "+req.URL.Path)
		rec.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	return rec
}

// pathRecorder wraps a handler and records the method+path of every request,
// so a test can assert a specific endpoint was hit.
type pathRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (p *pathRecorder) hitAny(substr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.paths {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func (p *pathRecorder) count(substr string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.paths {
		if strings.Contains(s, substr) {
			n++
		}
	}
	return n
}

// newRealServer starts a real store-backed API server (admin actor "alice")
// and points LODE_SERVER/LODE_TOKEN at it. It returns a client authenticated
// as alice and a recorder of the endpoints hit.
func newRealServer(t *testing.T) (*store.Store, *cli.Client, *pathRecorder) {
	t.Helper()
	st := store.OpenTestStore(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	h, _, err := api.NewServer(st, api.Config{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	rec := &pathRecorder{}
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec.mu.Lock()
		rec.paths = append(rec.paths, req.Method+" "+req.URL.Path)
		rec.mu.Unlock()
		h.ServeHTTP(w, req)
	})
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", token)
	return st, cli.NewClient(cli.Config{ServerURL: ts.URL, Token: token}), rec
}

// initGitRepo creates a fresh git repo with one commit and returns its path
// resolved to git's own toplevel (macOS /var symlink; see the identical helper
// in internal/cmd/lifecycle_test.go).
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	root, ok := worktree.Root(dir)
	if !ok {
		t.Fatalf("worktree.Root(%s): ok = false", dir)
	}
	return root
}

// setupLeasedWorktree creates a project, a task, its wt/<id>-<slug> worktree,
// and a lease bound to that worktree's real identity (mirroring `lode next`).
// Creating the project is idempotent so a test can call this more than once
// against the same server to put several tasks under one project.
func setupLeasedWorktree(t *testing.T, c *cli.Client, root, title string) (taskID, wtDir, identity string) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.GetProject(ctx, "proj"); err != nil {
		if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "PROJ"}); err != nil {
			t.Fatalf("create project: %v", err)
		}
	}
	task, _, err := c.CreateTask(ctx, cli.CreateTaskInput{Project: "proj", Title: title, Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	host, _ := os.Hostname()
	pending := host + ":" + root + "#pending"
	resp, _, err := c.ClaimTask(ctx, task.ID, pending, 0)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	slug := strings.TrimPrefix(resp.Branch, "wl/"+task.ID+"-")
	wtDir = filepath.Join(root, "wt", task.ID+"-"+slug)
	if out, err := exec.Command("git", "-C", root, "worktree", "add", wtDir, "-b", resp.Branch).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	identity, err = worktree.Identity(wtDir)
	if err != nil {
		t.Fatalf("worktree identity: %v", err)
	}
	if _, _, err := c.RebindWorktree(ctx, task.ID, identity); err != nil {
		t.Fatalf("rebind worktree: %v", err)
	}
	return task.ID, wtDir, identity
}

func payloadJSON(t *testing.T, p Payload) []byte {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// --- guard NOP --------------------------------------------------------------

func TestGuardNOPForEveryEvent(t *testing.T) {
	for _, event := range allEvents {
		t.Run(event, func(t *testing.T) {
			rec := newRecordingServer(t)
			root := initGitRepo(t) // plain repo, no wt/ dir
			payload := payloadJSON(t, Payload{Cwd: root, SessionID: "s1"})

			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), Options{
				Event:  event,
				Stdin:  bytes.NewReader(payload),
				Stdout: &stdout,
				Stderr: &stderr,
			})
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
			}
			if rec.hit() {
				t.Fatalf("backbone was called during a guard-NOP: %v", rec.requests)
			}
		})
	}
}

// --- daisy-chain ------------------------------------------------------------

func TestNextChainRunsDownstreamOnGuardNOP(t *testing.T) {
	newRecordingServer(t)
	root := initGitRepo(t)
	marker := filepath.Join(t.TempDir(), "downstream-ran")
	payload := payloadJSON(t, Payload{Cwd: root, SessionID: "s1"})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "pre-commit",
		Next:   []string{"touch", marker},
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("downstream did not run, marker %s missing: %v", marker, err)
	}
}

func TestNextChainReceivesPayloadOnStdin(t *testing.T) {
	newRecordingServer(t)
	root := initGitRepo(t)
	sink := filepath.Join(t.TempDir(), "downstream-stdin")
	payload := payloadJSON(t, Payload{Cwd: root, SessionID: "chain-session"})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "session-end",
		Next:   []string{"sh", "-c", "cat > " + sink},
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	got, err := os.ReadFile(sink)
	if err != nil {
		t.Fatalf("read downstream stdin capture: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downstream stdin = %q, want the original payload %q", got, payload)
	}
}

func TestNextChainPropagatesExitCode(t *testing.T) {
	newRecordingServer(t)
	root := initGitRepo(t)
	payload := payloadJSON(t, Payload{Cwd: root})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "pre-commit",
		Next:   []string{"sh", "-c", "exit 7"},
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 7 {
		t.Fatalf("exit code = %d, want 7 (child's code)", code)
	}
}

// --- pre-commit renews ------------------------------------------------------

func TestPreCommitRenewsInsideWorktree(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Renew me")

	before, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	payload := payloadJSON(t, Payload{Cwd: wtDir, HookEventName: "PreToolUse"})
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "pre-commit",
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !rec.hitAny("/renew") {
		t.Fatalf("renew endpoint was not hit; paths: %v", rec.paths)
	}
	after, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task after renew: %v", err)
	}
	if after.Lease == nil || before.Lease == nil {
		t.Fatalf("lease missing: before=%+v after=%+v", before.Lease, after.Lease)
	}
	if after.Lease.RenewedAt.Before(before.Lease.RenewedAt) {
		t.Fatalf("renewed_at went backwards: %v -> %v", before.Lease.RenewedAt, after.Lease.RenewedAt)
	}
}

// --- session-start emits additionalContext ----------------------------------

func TestSessionStartEmitsAdditionalContext(t *testing.T) {
	_, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Brief context")

	payload := payloadJSON(t, Payload{Cwd: wtDir, SessionID: "s-emit", HookEventName: "SessionStart"})
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "session-start",
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid additionalContext JSON: %v\nstdout: %s", err, stdout.String())
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName = %q, want SessionStart", out.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, taskID) {
		t.Fatalf("additionalContext missing task id %q: %q", taskID, out.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "Brief context") {
		t.Fatalf("additionalContext missing task title: %q", out.HookSpecificOutput.AdditionalContext)
	}

	// The session marker must have been written and read as fresh (our pid).
	if !sessionMarkerFresh(wtDir) {
		t.Fatalf("session marker not written/fresh after session-start")
	}
}

// --- session-end removes the marker -----------------------------------------

func TestSessionEndRemovesMarker(t *testing.T) {
	_, c, _ := newRealServer(t)
	root := initGitRepo(t)
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "End me")

	if err := writeSessionMarker(wtDir, "s-end", time.Now()); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if !sessionMarkerFresh(wtDir) {
		t.Fatalf("precondition: marker should be fresh")
	}

	payload := payloadJSON(t, Payload{Cwd: wtDir})
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "session-end",
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if sessionMarkerFresh(wtDir) {
		t.Fatalf("session marker still present after session-end")
	}
}

// --- worktree-remove releases the lease -------------------------------------

func TestWorktreeRemoveReleasesLease(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Remove me")

	// tool_input carries the removed path; parse it out defensively.
	toolInput, _ := json.Marshal(map[string]string{"path": wtDir})
	payload := payloadJSON(t, Payload{Cwd: root, ToolInput: toolInput})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "worktree-remove",
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !rec.hitAny("/release") {
		t.Fatalf("release endpoint was not hit; paths: %v", rec.paths)
	}
	detail, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Lease != nil {
		t.Fatalf("lease after worktree-remove = %+v, want nil", detail.Lease)
	}
}

// --- worktree-create auto-resumes an abandoned worktree ---------------------

func TestWorktreeCreateAutoResumesExpiredLease(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Adopt me")

	// Sweep the lease so the task is back in ready with no lease.
	if _, err := st.ExpireLeases(context.Background(), time.Now().Add(3*time.Hour)); err != nil {
		t.Fatalf("expire leases: %v", err)
	}
	detail, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Lease != nil {
		t.Fatalf("precondition: lease should be swept, got %+v", detail.Lease)
	}

	toolInput, _ := json.Marshal(map[string]string{"path": wtDir})
	payload := payloadJSON(t, Payload{Cwd: root, ToolInput: toolInput})
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "worktree-create",
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !rec.hitAny("/claim") {
		t.Fatalf("claim endpoint was not hit for auto-resume; paths: %v", rec.paths)
	}
	after, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task after auto-resume: %v", err)
	}
	if after.State != "in_progress" || after.Lease == nil {
		t.Fatalf("task after auto-resume = state %q lease %+v, want in_progress/non-nil", after.State, after.Lease)
	}
}

func TestSessionMarkerHeartbeat(t *testing.T) {
	root := initGitRepo(t)
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	if err := writeSessionMarker(root, "sess-1", base); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	id, ok := markerSessionID(root)
	if !ok || id != "sess-1" {
		t.Fatalf("markerSessionID: got %q, %v", id, ok)
	}

	// Within the debounce window: no heartbeat is due.
	if heartbeatDue(root, base.Add(30*time.Second)) {
		t.Fatal("heartbeat due 30s after the last one; want debounced")
	}
	// Past the window: due again.
	if !heartbeatDue(root, base.Add(2*time.Minute)) {
		t.Fatal("heartbeat not due 2m after the last one")
	}

	// Recording a heartbeat moves the window without disturbing the session id.
	if err := recordHeartbeat(root, base.Add(2*time.Minute)); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if heartbeatDue(root, base.Add(2*time.Minute+30*time.Second)) {
		t.Fatal("heartbeat due 30s after a recorded heartbeat; want debounced")
	}
	if id, ok := markerSessionID(root); !ok || id != "sess-1" {
		t.Fatalf("session id after heartbeat: got %q, %v", id, ok)
	}

	// No marker at all: nothing to heartbeat, and no session id.
	empty := initGitRepo(t)
	if heartbeatDue(empty, base) {
		t.Fatal("heartbeat due with no marker file")
	}
	if _, ok := markerSessionID(empty); ok {
		t.Fatal("markerSessionID found an id with no marker file")
	}
}

// runHook drives one hook invocation the way the existing tests do inline,
// and fails the test on a non-zero exit code.
func runHook(t *testing.T, event string, p Payload) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  event,
		Stdin:  bytes.NewReader(payloadJSON(t, p)),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("%s exit code = %d, want 0 (stderr: %s)", event, code, stderr.String())
	}
}

func TestHeartbeatReportsAgentSession(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Heartbeat task")

	// session-start opens the session and writes the marker.
	runHook(t, "session-start", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if !rec.hitAny("/agent-session") {
		t.Fatal("session-start did not report the agent session")
	}

	// A heartbeat inside the debounce window makes no backbone call.
	before := rec.count("/agent-session")
	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if rec.count("/agent-session") != before {
		t.Fatal("heartbeat inside the debounce window still called the backbone")
	}

	// The session is recorded against the task's lease.
	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	sess, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session: %v", err)
	}
	if sess.EndedAt != nil {
		t.Fatal("session should still be open")
	}

	// session-end closes it.
	runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1"})
	sess, err = st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session after end: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatal("session-end did not close the session")
	}
}

func TestHeartbeatOutsideWorktreeIsNOP(t *testing.T) {
	rec := newRecordingServer(t)
	runHook(t, "heartbeat", Payload{Cwd: t.TempDir(), SessionID: "sess-1"})
	if rec.hit() {
		t.Fatal("heartbeat outside a Worklode worktree called the backbone")
	}
}

// TestWorktreeEnterExitSwitchesLease drives one session across two leased
// worktrees: entering the second opens a row under its lease with the same
// session id, and exiting stamps ended_at on that row while the first
// worktree's row stays open.
func TestWorktreeEnterExitSwitchesLease(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskA, wtDirA, _ := setupLeasedWorktree(t, c, root, "Task A")
	taskB, wtDirB, _ := setupLeasedWorktree(t, c, root, "Task B")

	// Start a session in the first worktree.
	runHook(t, "session-start", Payload{Cwd: wtDirA, SessionID: "sess-1"})

	leaseA, err := st.ActiveLease(t.Context(), taskA)
	if err != nil {
		t.Fatalf("active lease A: %v", err)
	}
	sessA, err := st.AgentSession(t.Context(), leaseA.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session A: %v", err)
	}
	if sessA.EndedAt != nil {
		t.Fatal("session A should still be open before entering B")
	}

	// Enter the second worktree: a new row opens under B's lease.
	toolInput, _ := json.Marshal(map[string]string{"path": wtDirB})
	runHook(t, "worktree-enter", Payload{Cwd: wtDirA, SessionID: "sess-1", ToolInput: toolInput})

	leaseB, err := st.ActiveLease(t.Context(), taskB)
	if err != nil {
		t.Fatalf("active lease B: %v", err)
	}
	sessB, err := st.AgentSession(t.Context(), leaseB.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session B: %v", err)
	}
	if sessB.EndedAt != nil {
		t.Fatal("session B should be open after worktree-enter")
	}

	// worktree-enter must also write B's marker — symmetric with
	// session-start — or heartbeats there debounce off forever and B looks
	// abandoned to offerScan/handleWorktreeCreate.
	if id, ok := markerSessionID(wtDirB); !ok || id != "sess-1" {
		t.Fatalf("markerSessionID(B) after enter = %q, %v, want sess-1, true", id, ok)
	}
	if !sessionMarkerFresh(wtDirB) {
		t.Fatal("worktree B marker not fresh after worktree-enter")
	}

	// A heartbeat past the debounce window in the entered worktree reaches
	// the backbone (proving the marker written above is actually usable).
	before := rec.count("/agent-session")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "heartbeat",
		Stdin:  bytes.NewReader(payloadJSON(t, Payload{Cwd: wtDirB, SessionID: "sess-1"})),
		Stdout: &stdout,
		Stderr: &stderr,
		Now:    func() time.Time { return time.Now().Add(2 * time.Minute) },
	})
	if code != 0 {
		t.Fatalf("heartbeat in entered worktree exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if rec.count("/agent-session") != before+1 {
		t.Fatalf("heartbeat in entered worktree did not reach the backbone: count before=%d after=%d",
			before, rec.count("/agent-session"))
	}

	// Exit the second worktree: B's row closes, A's stays open.
	runHook(t, "worktree-exit", Payload{Cwd: wtDirA, SessionID: "sess-1", ToolInput: toolInput})

	sessB, err = st.AgentSession(t.Context(), leaseB.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session B after exit: %v", err)
	}
	if sessB.EndedAt == nil {
		t.Fatal("worktree-exit did not close session B")
	}

	sessA, err = st.AgentSession(t.Context(), leaseA.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session A after exit: %v", err)
	}
	if sessA.EndedAt != nil {
		t.Fatal("session A should remain open after exiting B")
	}

	// worktree-exit must remove B's marker — symmetric with session-end.
	if _, ok := markerSessionID(wtDirB); ok {
		t.Fatal("worktree B marker still present after worktree-exit")
	}
}
