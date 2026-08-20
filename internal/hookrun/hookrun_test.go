package hookrun

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
	"github.com/sunstoneinstitute/worklode/internal/skillhash"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// TestMain isolates the package from the developer's real machine before any
// test runs: a temp LODE_SKILLS_DIR (so no brief can reach ~/.worklode/skills)
// and a temp HOME plus a mock keyring (so no worktree-remove can reach a real
// keychain). Isolation is structural here rather than resting on "no test
// happens to carry skills" or "no test id happens to be live": worktree-remove
// purges from ~/.cache/worklode/secrets/<id>.json and the OS keystore, and the
// ids these tests use (WL-1..WL-4) are exactly the ones a developer's own
// tasks have. Individual tests still set their own (scoped and auto-restored);
// this is the backstop for any that forget.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "worklode-hookrun-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: create temp dir:", err)
		os.Exit(1)
	}
	home, skills := filepath.Join(dir, "home"), filepath.Join(dir, "skills")
	for _, d := range []string{home, skills} {
		if err := os.Mkdir(d, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "TestMain: create", d, err)
			os.Exit(1)
		}
	}
	os.Setenv("LODE_SKILLS_DIR", skills)
	os.Setenv("HOME", home)
	keyring.MockInit()
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// allEvents is the guarded event set (unknown events are handled separately),
// taken from the exported listing so a new event cannot skip these tests.
var allEvents = EventNames()

// pathRecorder records the method+path of every request a fake backbone
// receives, so a test can assert which endpoints were hit — or, for the
// guard-NOP tests, that none were.
type pathRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (p *pathRecorder) record(req *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = append(p.paths, req.Method+" "+req.URL.Path)
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

// list returns everything recorded so far, for failure messages.
func (p *pathRecorder) list() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.paths)
}

func (p *pathRecorder) hit() bool { return p.count("") > 0 }

func (p *pathRecorder) hitAny(substr string) bool { return p.count(substr) > 0 }

// reset drops everything recorded so far, so a test can set up state through
// the API and then assert that the code under test made no calls at all.
func (p *pathRecorder) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = nil
}

// newRecordingServer starts a server that records every request, answers
// everything with "{}", and points LODE_SERVER/LODE_TOKEN at it.
func newRecordingServer(t *testing.T) *pathRecorder {
	t.Helper()
	rec := &pathRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec.record(req)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	return rec
}

// testLayout is the default (.worktrees) layout, for tests that need to ask
// the guard the same question the handlers do.
func testLayout(t *testing.T) worktree.Layout {
	t.Helper()
	l, err := worktree.NewLayout("")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	return l
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
		rec.record(req)
		h.ServeHTTP(w, req)
	})
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", token)
	return st, cli.NewClient(cli.Config{ServerURL: ts.URL, Token: token}), rec
}

// gitRepo is a throwaway git repo driven through git itself. Every command
// runs with commit.gpgsign=false and a fixed identity, so a test never depends
// on (or is broken by) the developer's global git config.
type gitRepo struct {
	t    *testing.T
	root string
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	c := exec.Command("git", append([]string{"-C", r.root, "-c", "commit.gpgsign=false"}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := c.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *gitRepo) commit(name, content, msg string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.root, name), []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", name, err)
	}
	r.git("add", name)
	r.git("commit", "-m", msg)
}

// initGitRepo creates a fresh git repo with one commit and returns its path
// resolved to git's own toplevel (macOS /var symlink; see the identical helper
// in internal/cmd/lifecycle_test.go).
func initGitRepo(t *testing.T) string {
	t.Helper()
	r := &gitRepo{t: t, root: t.TempDir()}
	r.git("init")
	r.commit("README.md", "test\n", "initial commit")

	root, ok := worktree.Root(r.root)
	if !ok {
		t.Fatalf("worktree.Root(%s): ok = false", r.root)
	}
	return root
}

// setupLeasedWorktree creates a project, a task, its .worktrees/<branch>
// worktree, and a lease bound to that worktree's real identity (mirroring
// `lode next`).
// Creating the project is idempotent so a test can call this more than once
// against the same server to put several tasks under one project.
func setupLeasedWorktree(t *testing.T, c *cli.Client, root, title string) (taskID, wtDir, identity string) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.GetProject(ctx, "proj"); err != nil {
		if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "PROJ"}); err != nil {
			t.Fatalf("create project: %v", err)
		}
	}
	task, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: title, Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	host, _ := os.Hostname()
	pending := host + ":" + root + "#pending"
	resp, _, err := c.ClaimTask(ctx, task.ID, pending, 0)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	wtDir = filepath.Join(root, ".worktrees", resp.Branch)
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
			root := initGitRepo(t) // plain repo, no worktree dir

			runHook(t, event, Payload{Cwd: root, SessionID: "s1"})
			if rec.hit() {
				t.Fatalf("backbone was called during a guard-NOP: %v", rec.list())
			}
		})
	}
}

// Every listed event must reach a handler: `lode hook --list` advertises this
// set, so an event that only dispatch's default branch answers would be a
// documented no-op. Run outside any repo, where every handler is a guard NOP
// and the only possible output is the unknown-event warning.
func TestEventsAreDispatched(t *testing.T) {
	newRecordingServer(t)
	run := func(event string) string {
		_, stderr := runHookOutput(t, event, Payload{Cwd: t.TempDir()})
		return stderr
	}
	for _, event := range allEvents {
		if got := run(event); strings.Contains(got, "unknown hook event") {
			t.Errorf("listed event %q is not dispatched: %s", event, got)
		}
	}
	if got := run("not-an-event"); !strings.Contains(got, "unknown hook event") {
		t.Errorf("unlisted event: stderr = %q, want an unknown-event warning", got)
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

	runHook(t, "pre-commit", Payload{Cwd: wtDir, HookEventName: "PreToolUse"})
	if !rec.hitAny("/renew") {
		t.Fatalf("renew endpoint was not hit; paths: %v", rec.list())
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

// TestPreCommitWithoutLeaseIsSilent: committing in a worktree that holds no
// lease — swept, released, or never claimed — is ordinary. The hook must not
// renew, must not report a session (both would 404), must not warn, and must
// not block the commit.
func TestPreCommitWithoutLeaseIsSilent(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "No lease here")
	if _, err := c.ReleaseLease(context.Background(), taskID); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	if err := writeSessionMarker(wtDir, "s-nolease", time.Time{}); err != nil {
		t.Fatalf("write marker: %v", err) // a zero heartbeat makes one due
	}

	_, stderr := runHookOutput(t, "pre-commit", Payload{Cwd: wtDir, HookEventName: "PreToolUse"})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty (a missing lease is not a warning)", stderr)
	}
	if rec.hitAny("/renew") {
		t.Fatalf("renew endpoint was hit with no lease held; paths: %v", rec.list())
	}
	if rec.hitAny("/agent-session") {
		t.Fatalf("agent-session endpoint was hit with no lease held; paths: %v", rec.list())
	}
}

// TestPreCommitResolvesTaskIDFromGitConfigAfterWorktreeRename covers the case
// the explicit worklode.task-id field exists for: a worktree renamed to a
// directory name that carries no task id. It still sits one level below the
// base, so it clears spec 008 §5.2's guard; only the stamped git config can
// then say which task it belongs to.
func TestPreCommitResolvesTaskIDFromGitConfigAfterWorktreeRename(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Explicit id")

	if err := worktree.EnableWorktreeConfigExtension(root); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension: %v", err)
	}
	if err := worktree.SetTaskID(wtDir, taskID); err != nil {
		t.Fatalf("SetTaskID: %v", err)
	}

	renamed := filepath.Join(root, worktree.DefaultBase, "no-id-in-this-name")
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, renamed).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}
	// Guard the guard: if the new name still carried an id, the fallback
	// would resolve it and the test would prove nothing about the config.
	if id, ok := testLayout(t).ParseDir(renamed); ok {
		t.Fatalf("ParseDir(%s) = (%q, true), want ok=false — the renamed directory must not carry an id", renamed, id)
	}

	_, stderr := runHookOutput(t, "pre-commit", Payload{Cwd: renamed, HookEventName: "PreToolUse"})
	// Reaching the brief is the assertion: the handler only gets that far
	// once the task id resolved, and the directory name can no longer supply
	// it.
	if !rec.hitAny("/tasks/" + taskID + "/brief") {
		t.Fatalf("brief for %s was not fetched, so the task id did not resolve; paths: %v", taskID, rec.list())
	}
	// The lease is not renewed, and that is correct rather than a gap here:
	// lease ownership is keyed on worktree.Identity, which is the worktree's
	// absolute path, so a moved worktree no longer holds the lease it claimed.
	// Resolving the task id and owning the lease are separate questions.
	if rec.hitAny("/renew") {
		t.Fatalf("renew endpoint was hit for a lease bound to the pre-move path; paths: %v", rec.list())
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty (a lease bound elsewhere is not a warning)", stderr)
	}
}

// TestPreCommitIgnoresStampedWorktreeOutsideTheBase pins the other half of
// §3.2's split: the guard is a pure string question, and a stamped worktree
// that fails it stays invisible to Worklode. Without this, the git-config
// lookup would quietly widen the guard the spec deliberately keeps cheap.
func TestPreCommitIgnoresStampedWorktreeOutsideTheBase(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Outside the base")

	if err := worktree.EnableWorktreeConfigExtension(root); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension: %v", err)
	}
	if err := worktree.SetTaskID(wtDir, taskID); err != nil {
		t.Fatalf("SetTaskID: %v", err)
	}

	outside := filepath.Join(root, "elsewhere", taskID+"-outside-the-base")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, outside).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}

	rec.reset()
	_, stderr := runHookOutput(t, "pre-commit", Payload{Cwd: outside, HookEventName: "PreToolUse"})
	if paths := rec.list(); len(paths) != 0 {
		t.Fatalf("backbone was called for a path outside %s; paths: %v", worktree.DefaultBase, paths)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty (a path outside the base is a silent NOP)", stderr)
	}
}

// --- session-start emits additionalContext ----------------------------------

func TestSessionStartEmitsAdditionalContext(t *testing.T) {
	_, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Brief context")

	stdout, _ := runSessionStart(t, wtDir, "s-emit")

	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not valid additionalContext JSON: %v\nstdout: %s", err, stdout)
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

// --- offer scan ---------------------------------------------------------

// expireLease force-expires taskID's lease via the store, mirroring the
// sweeper (see TestWorktreeCreateAutoResumesExpiredLease), so a later Brief
// call returns Lease == nil.
func expireLease(t *testing.T, st *store.Store, taskID string) {
	t.Helper()
	if _, err := st.ExpireLeases(context.Background(), time.Now().Add(3*time.Hour)); err != nil {
		t.Fatalf("expire lease on %s: %v", taskID, err)
	}
}

// offerScan runs when session-start fires OUTSIDE a worktree. A worktree whose
// lease has expired and whose marker is absent is offered for adoption.
func TestOfferScanOffersAbandonedWorktree(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Abandoned worktree")
	expireLease(t, st, taskID)

	ctx := offerScanContext(t, root)
	if !strings.Contains(ctx, ".worktrees/"+filepath.Base(wtDir)) {
		t.Fatalf("additionalContext does not offer the worktree: %q", ctx)
	}
	if !strings.Contains(ctx, taskID) {
		t.Fatalf("additionalContext missing task id %q: %q", taskID, ctx)
	}
}

// The layout is flat (spec 008 §5.1): offerScan reads one level below the base
// and nothing deeper, so a worktree re-homed into a subdirectory is not a
// worktree root any more and is not offered. This pins the flat scan — the
// pre-flat code walked to depth 3 and would have found it.
func TestOfferScanIgnoresNestedWorktree(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Nested worktree")

	nested := filepath.Join(root, ".worktrees", "team", filepath.Base(wtDir))
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, nested).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}
	expireLease(t, st, taskID)

	if ctx := offerScanContext(t, root); ctx != "" {
		t.Fatalf("nested worktree was offered for adoption: %q", ctx)
	}
}

// offerScan only ever walks the configured base directory (hookrun.go's
// offerScan), so this proves that narrower property — the scan never reaches
// outside its base — not that ParseDir has stopped accepting wt/: the scan
// would skip anything under wt/ even if ParseDir still recognised it. The
// genuine "legacy wt/ is gone" coverage (spec 008 §7) is
// TestLayoutParseDir's "legacy wt is gone" case in
// internal/worktree/worktree_test.go.
func TestOfferScanIgnoresLegacyWtDir(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Legacy worktree")

	legacy := filepath.Join(root, "wt", filepath.Base(wtDir))
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, legacy).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}
	expireLease(t, st, taskID)

	if ctx := offerScanContext(t, root); ctx != "" {
		t.Fatalf("legacy wt/ worktree was offered for adoption: %q", ctx)
	}
}

// offerScanContext runs session-start from dir and returns the
// additionalContext it emitted ("" when it emitted nothing).
func offerScanContext(t *testing.T, dir string) string {
	t.Helper()
	stdout, _ := runSessionStart(t, dir, "s-scan")
	if stdout == "" {
		return ""
	}
	return additionalContext(t, stdout)
}

// --- worktree_dir resolution -------------------------------------------------

// A non-default worktree_dir (here via LODE_WORKTREE_DIR, the env override
// spec 008 §5.1 gives) must be honoured, not just tolerated: the guard has to
// find a worktree that isn't under the default .worktrees at all.
func TestLayoutCustomBaseHonored(t *testing.T) {
	rec := newRecordingServer(t)
	root := initGitRepo(t)
	t.Setenv("LODE_WORKTREE_DIR", "custom-base")
	wtDir := addWorktreeAt(t, root, "custom-base", "PROJ-1", "custom")

	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "s-custom-base"})
	if !rec.hit() {
		t.Fatalf("guard did not fire for a worktree under the configured custom base dir")
	}
}

// A malformed worktree_dir (NewLayout rejects an absolute path) must degrade
// to the default .worktrees layout with a warning, never fail the event or
// leave the guard permanently blind.
func TestLayoutMalformedWorktreeDirDegradesToDefault(t *testing.T) {
	rec := newRecordingServer(t)
	root := initGitRepo(t)
	t.Setenv("LODE_WORKTREE_DIR", "/not/relative/to/the/repo")
	wtDir := addWorktree(t, root, "PROJ-1", "degrade")

	_, stderr := runHookOutput(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "s-degrade"})
	if !rec.hit() {
		t.Fatalf("malformed worktree_dir should degrade to the default .worktrees layout, not NOP (stderr: %s)", stderr)
	}
	if !strings.Contains(stderr, "resolve worktree layout") {
		t.Fatalf("expected a warning about the malformed worktree_dir, got stderr=%q", stderr)
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

	runHook(t, "session-end", Payload{Cwd: wtDir})
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
	runHook(t, "worktree-remove", Payload{Cwd: root, ToolInput: pathToolInput(t, wtDir)})
	if !rec.hitAny("/release") {
		t.Fatalf("release endpoint was not hit; paths: %v", rec.list())
	}
	detail, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Lease != nil {
		t.Fatalf("lease after worktree-remove = %+v, want nil", detail.Lease)
	}
}

// TestWorktreeRemovePurgesSecrets exercises the real-server path: a
// worktree-remove that also successfully releases the lease must purge the
// task's materialized secrets (spec 017: materialized lifetime equals
// worktree lifetime).
func TestWorktreeRemovePurgesSecrets(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())

	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Purge me")

	if err := secrets.Put(taskID, "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: taskID, Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	runHook(t, "worktree-remove", Payload{Cwd: root, ToolInput: pathToolInput(t, wtDir)})
	if !rec.hitAny("/release") {
		t.Fatalf("release endpoint was not hit; paths: %v", rec.list())
	}
	if _, err := secrets.Fetch(taskID, "A_TOKEN"); err == nil {
		t.Fatal("secret survived worktree removal")
	}
	if _, ok := secrets.LoadManifest(taskID); ok {
		t.Fatal("manifest survived worktree removal")
	}
}

// TestWorktreeRemovePurgesSecretsRegardlessOfBackbone proves the purge is
// unconditional on the backbone call's outcome: hooks never fail the event,
// and the local purge must happen even when there is no reachable backbone
// at all. Uses addWorktree (no real server needed) rather than
// setupLeasedWorktree so the worktree path parses under the layout
// (exactly one level below the base) without requiring a claim.
func TestWorktreeRemovePurgesSecretsRegardlessOfBackbone(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())

	const taskID = "WL-3"
	if err := secrets.Put(taskID, "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: taskID, Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	root := initGitRepo(t)
	wtDir := addWorktree(t, root, taskID, "fix")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "worktree-remove",
		Stdin:  bytes.NewReader(payloadJSON(t, Payload{Cwd: root, ToolInput: pathToolInput(t, wtDir)})),
		Stdout: &stdout,
		Stderr: &stderr,
		// No backbone: the release call will warn, but the local purge must
		// have happened regardless.
		NewClient: func() (*cli.Client, error) { return nil, errors.New("no config") },
	})
	if code != 0 {
		t.Fatalf("hook exit = %d; hooks never fail the event", code)
	}
	if _, err := secrets.Fetch(taskID, "A_TOKEN"); err == nil {
		t.Fatal("secret survived worktree removal")
	}
	if _, ok := secrets.LoadManifest(taskID); ok {
		t.Fatal("manifest survived worktree removal")
	}
}

// TestWorktreeExitKeepsSecrets is the converse of the two above, and the
// reason purgeSecrets is bound to removal rather than to a session leaving.
// A session that exits a worktree still holds that task's lease and can come
// back (spec 012 §4: one session working several tasks in sequence); spec 017
// §3 purges on exit only for a lease that is gone. Purging here would cost a
// fresh consent and a fresh Touch ID on return — unobtainable in the
// non-interactive session that is the common case.
func TestWorktreeExitKeepsSecrets(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())

	const taskID = "WL-4"
	if err := secrets.Put(taskID, "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: taskID, Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	root := initGitRepo(t)
	wtDir := addWorktree(t, root, taskID, "hop")
	if err := writeSessionMarker(wtDir, "sess-1", time.Now()); err != nil {
		t.Fatalf("write session marker: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:     "worktree-exit",
		Stdin:     bytes.NewReader(payloadJSON(t, Payload{Cwd: root, SessionID: "sess-1", ToolInput: pathToolInput(t, wtDir)})),
		Stdout:    &stdout,
		Stderr:    &stderr,
		NewClient: func() (*cli.Client, error) { return nil, errors.New("no config") },
	})
	if code != 0 {
		t.Fatalf("hook exit = %d; hooks never fail the event", code)
	}
	// Positive control: without it, "the secrets survived" would also be true
	// of a handler that returned at its first guard. The deliberately-failing
	// NewClient proves endSession was reached, and the marker removal proves
	// the handler ran to its end.
	if !strings.Contains(stderr.String(), "load config") {
		t.Fatalf("handler did not reach the backbone call; stderr: %q", stderr.String())
	}
	if _, ok := markerSessionID(wtDir); ok {
		t.Fatal("session marker survived worktree-exit")
	}
	if _, err := secrets.Fetch(taskID, "A_TOKEN"); err != nil {
		t.Fatalf("leaving the worktree purged a still-leased task's secrets: %v", err)
	}
	if _, ok := secrets.LoadManifest(taskID); !ok {
		t.Fatal("leaving the worktree removed a still-leased task's manifest")
	}
}

// --- worktree-create auto-resumes an abandoned worktree ---------------------

func TestWorktreeCreateAutoResumesExpiredLease(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Adopt me")

	// Sweep the lease so the task is back in ready with no lease.
	expireLease(t, st, taskID)
	detail, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Lease != nil {
		t.Fatalf("precondition: lease should be swept, got %+v", detail.Lease)
	}

	runHook(t, "worktree-create", Payload{Cwd: root, ToolInput: pathToolInput(t, wtDir)})
	if !rec.hitAny("/claim") {
		t.Fatalf("claim endpoint was not hit for auto-resume; paths: %v", rec.list())
	}
	after, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task after auto-resume: %v", err)
	}
	if after.State != "in_progress" || after.Lease == nil {
		t.Fatalf("task after auto-resume = state %q lease %+v, want in_progress/non-nil", after.State, after.Lease)
	}
}

// TestSessionStartNormalizesHarnessPayload drives session-start with
// Harness: "codex" and a camelCase payload (no cwd/session_id keys at all)
// against a worktree whose lease has expired, and asserts it re-acquires the
// lease and touches the session exactly as the Claude Code shape does — spec
// 024 acceptance 3: same behaviour, same backbone rows, different payload on
// stdin.
func TestSessionStartNormalizesHarnessPayload(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Codex resume")
	expireLease(t, st, taskID)

	raw := []byte(fmt.Sprintf(`{"cwd":%q,"sessionId":"s9"}`, wtDir))
	var outBuf, errBuf bytes.Buffer
	code := Run(context.Background(), Options{
		Event:   "session-start",
		Harness: "codex",
		Stdin:   bytes.NewReader(raw),
		Stdout:  &outBuf,
		Stderr:  &errBuf,
	})
	if code != 0 {
		t.Fatalf("session-start exit code = %d, want 0 (stderr: %s)", code, errBuf.String())
	}
	if !rec.hitAny("/claim") {
		t.Fatalf("claim endpoint was not hit for auto-resume; paths: %v", rec.list())
	}

	after, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task after auto-resume: %v", err)
	}
	if after.State != "in_progress" || after.Lease == nil {
		t.Fatalf("task after auto-resume = state %q lease %+v, want in_progress/non-nil", after.State, after.Lease)
	}

	lease, err := st.ActiveLease(context.Background(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	sess, err := st.AgentSession(context.Background(), lease.ID, "codex", "s9")
	if err != nil {
		t.Fatalf("agent session: %v", err)
	}
	if sess.EndedAt != nil {
		t.Fatal("session should still be open")
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

	// writeSessionMarker leaves LastHeartbeatAt empty (only a heartbeat that
	// actually reached the backbone should stamp it), so a heartbeat is due
	// immediately, even moments after the marker was written.
	if !heartbeatDue(root, base.Add(1*time.Second)) {
		t.Fatal("heartbeat not due with no recorded heartbeat yet")
	}

	// Record the first heartbeat.
	if err := recordHeartbeat(root, base); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}

	// Within the debounce window of the recorded heartbeat: not due.
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

// runHookOutput drives one hook invocation and returns what it wrote, failing
// the test on a non-zero exit code — a hook never fails its own event. Tests
// that need to vary anything else about Options (--next, NewClient, Now) still
// call Run directly.
func runHookOutput(t *testing.T, event string, p Payload) (stdout, stderr string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  event,
		Stdin:  bytes.NewReader(payloadJSON(t, p)),
		Stdout: &outBuf,
		Stderr: &errBuf,
	})
	if code != 0 {
		t.Fatalf("%s exit code = %d, want 0 (stderr: %s)", event, code, errBuf.String())
	}
	return outBuf.String(), errBuf.String()
}

// runHook is runHookOutput for the tests that assert on the backbone or the
// filesystem rather than on the hook's own output.
func runHook(t *testing.T, event string, p Payload) {
	t.Helper()
	runHookOutput(t, event, p)
}

// pathToolInput is the tool_input a worktree hook carries: the path it acted
// on.
func pathToolInput(t *testing.T, dir string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"path": dir})
	if err != nil {
		t.Fatalf("marshal tool_input: %v", err)
	}
	return raw
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

// TestSessionUnknownAgentRecordsOther covers the only path LODE_AGENT is set
// by hand on. An id the backbone has no entry for — a typo, or a harness
// worklode does not know yet — used to be posted verbatim, which
// store.TouchAgentSession rejects (it mirrors the agent_sessions.agent CHECK
// constraint). Because a hook downgrades every backbone failure to a warning
// so the triggering event still succeeds, that rejection lost the session
// with no signal the flag was the problem. It is folded onto "other" instead:
// the row lands, and the warning names the id that was not recognised.
func TestSessionUnknownAgentRecordsOther(t *testing.T) {
	t.Setenv("LODE_AGENT", "codx")
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Typo harness task")

	_, stderr := runHookOutput(t, "session-start", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if !strings.Contains(stderr, `codx`) || !strings.Contains(stderr, `"other"`) {
		t.Fatalf("stderr did not warn about the unknown agent: %q", stderr)
	}

	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	if _, err := st.AgentSession(t.Context(), lease.ID, "other", "sess-1"); err != nil {
		t.Fatalf("session was not recorded under %q: %v", model.AgentOther, err)
	}

	// The close half of the lifecycle normalizes identically, or the session
	// would stay open forever.
	runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1"})
	sess, err := st.AgentSession(t.Context(), lease.ID, "other", "sess-1")
	if err != nil {
		t.Fatalf("agent session after end: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatal("session-end did not close the session recorded as \"other\"")
	}
}

func TestHeartbeatOutsideWorktreeIsNOP(t *testing.T) {
	rec := newRecordingServer(t)
	runHook(t, "heartbeat", Payload{Cwd: t.TempDir(), SessionID: "sess-1"})
	if rec.hit() {
		t.Fatal("heartbeat outside a Worklode worktree called the backbone")
	}
}

// TestHeartbeatSelfHealsMissingMarker: a worktree that has lost its marker
// (e.g. it was never written, or was deleted) must not go silent forever —
// heartbeatDue is false with no marker, so without self-healing nothing would
// ever create one again. A heartbeat carrying a session id in the payload
// writes the marker and reports immediately.
func TestHeartbeatSelfHealsMissingMarker(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "Self heal")

	if _, ok := readSessionMarker(wtDir); ok {
		t.Fatal("precondition: no marker should exist yet")
	}

	before := rec.count("/agent-session")
	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if rec.count("/agent-session") != before+1 {
		t.Fatal("heartbeat with no marker and a payload session id did not report")
	}
	id, ok := markerSessionID(wtDir)
	if !ok || id != "sess-1" {
		t.Fatalf("marker not self-healed: id=%q ok=%v", id, ok)
	}
}

// TestHeartbeatUpdatesStaleMarkerID: when the payload's session id differs
// from the one recorded in the marker (e.g. after a /clear starts a new
// session in the same worktree), the marker must be brought up to date —
// otherwise a later marker-only report (pre-commit) would keep reporting the
// stale, no-longer-live session.
func TestHeartbeatUpdatesStaleMarkerID(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "Stale marker id")

	runHook(t, "session-start", Payload{Cwd: wtDir, SessionID: "sess-old"})

	before := rec.count("/agent-session")
	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-new"})
	if rec.count("/agent-session") != before+1 {
		t.Fatal("heartbeat with a differing session id did not report")
	}
	id, ok := markerSessionID(wtDir)
	if !ok || id != "sess-new" {
		t.Fatalf("marker id after drift = %q, %v, want sess-new, true", id, ok)
	}
}

// TestWorktreeExitWithoutExplicitPathIsNOP mirrors Claude Code's real
// ExitWorktree tool_input, which is {action, discard_changes} — no path key.
// handleWorktreeExit must not fall back to the payload cwd: by the time
// PostToolUse fires, ExitWorktree has already restored cwd to the worktree
// being returned TO, not the one being left, so a cwd fallback would end and
// delete the marker of the wrong worktree.
func TestWorktreeExitWithoutExplicitPathIsNOP(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "No explicit exit path")

	runHook(t, "session-start", Payload{Cwd: wtDir, SessionID: "sess-1"})

	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	before, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session before exit: %v", err)
	}
	if before.EndedAt != nil {
		t.Fatal("precondition: session should be open before the exit attempt")
	}
	markerBefore, okBefore := readSessionMarker(wtDir)
	if !okBefore {
		t.Fatal("precondition: marker should exist after session-start")
	}
	beforeCount := rec.count("/agent-session")

	// The real ExitWorktree tool_input: no path key.
	toolInput, _ := json.Marshal(map[string]string{"action": "keep"})
	runHook(t, "worktree-exit", Payload{Cwd: wtDir, SessionID: "sess-1", ToolInput: toolInput})

	if rec.count("/agent-session") != beforeCount {
		t.Fatal("worktree-exit without an explicit path called the backbone")
	}
	markerAfter, okAfter := readSessionMarker(wtDir)
	if !okAfter || markerAfter != markerBefore {
		t.Fatalf("marker changed by a path-less worktree-exit: before=%+v after=%+v (ok=%v)",
			markerBefore, markerAfter, okAfter)
	}
	after, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session after exit: %v", err)
	}
	if after.EndedAt != nil {
		t.Fatal("session ended by a path-less worktree-exit")
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
	toolInput := pathToolInput(t, wtDirB)
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

// --- session-start skills ----------------------------------------------------

// skillsBackbone fakes the backbone for the skills tests: a canned Brief plus
// a controllable archive endpoint (per-name success or forced 500). It is the
// single place these tests point LODE_SERVER/LODE_SKILLS_DIR at, so the
// "always use a throwaway skills dir" rule can't be forgotten in one of them.
type skillsBackbone struct {
	mu      sync.Mutex
	archive map[string][]byte
	fail    map[string]bool
	hang    map[string]bool
	brief   model.Brief

	inFlight    int32 // atomic
	maxInFlight int32 // atomic; high-water mark, so a test can assert the concurrency bound held
}

func newSkillsBackbone(t *testing.T, brief model.Brief) *skillsBackbone {
	t.Helper()
	b := &skillsBackbone{archive: map[string][]byte{}, fail: map[string]bool{}, hang: map[string]bool{}, brief: brief}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/brief", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(b.brief)
	})
	mux.HandleFunc("GET /api/v1/skills/{name}/archive/{hash}", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&b.inFlight, 1)
		defer atomic.AddInt32(&b.inFlight, -1)
		for {
			cur := atomic.LoadInt32(&b.maxInFlight)
			if n <= cur || atomic.CompareAndSwapInt32(&b.maxInFlight, cur, n) {
				break
			}
		}

		name, hash := r.PathValue("name"), r.PathValue("hash")
		b.mu.Lock()
		fail := b.fail[name]
		hang := b.hang[name]
		data, ok := b.archive[name+"@"+hash]
		b.mu.Unlock()
		if hang {
			// Block until the client gives up (archiveTimeout/skillsBudget)
			// rather than forever — a real timed-out client cancels its
			// request context, so this mirrors that instead of leaking a
			// goroutine for the life of the test process.
			<-r.Context().Done()
			return
		}
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	t.Setenv("LODE_SKILLS_DIR", t.TempDir())
	return b
}

func (b *skillsBackbone) setArchive(name, hash string, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.archive[name+"@"+hash] = data
}

func (b *skillsBackbone) failArchive(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail[name] = true
}

func (b *skillsBackbone) peakConcurrency() int {
	return int(atomic.LoadInt32(&b.maxInFlight))
}

func (b *skillsBackbone) hangArchive(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hang[name] = true
}

// setupFakeWorktree creates a .worktrees/<taskID>-<slug> git worktree under
// root without exercising a real claim/lease — the skills tests fake the
// backbone's HTTP surface directly instead.
func setupFakeWorktree(t *testing.T, root, taskID, slug string) string {
	t.Helper()
	wtDir := filepath.Join(root, ".worktrees", taskID+"-"+slug)
	if out, err := exec.Command("git", "-C", root, "worktree", "add", wtDir, "-b", taskID+"-"+slug).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return wtDir
}

// buildSkillArchive builds a single-file (SKILL.md) tar.gz and returns it
// alongside the content hash skillstore.Ensure will require of it.
func buildSkillArchive(t *testing.T, content string) (archive []byte, hash string) {
	t.Helper()
	data := []byte(content)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "SKILL.md", Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	hash = skillhash.Sum([]skillhash.File{{Path: "SKILL.md", Data: data}})
	return buf.Bytes(), hash
}

// runSessionStart runs the session-start hook against wtDir and returns its
// stdout/stderr, requiring exit 0 (the package's never-fail invariant).
func runSessionStart(t *testing.T, wtDir, sessionID string) (stdout, stderr string) {
	t.Helper()
	return runHookOutput(t, "session-start",
		Payload{Cwd: wtDir, SessionID: sessionID, HookEventName: "SessionStart"})
}

// additionalContext extracts the additionalContext string from a session-start
// hook's stdout.
func additionalContext(t *testing.T, stdout string) string {
	t.Helper()
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not valid additionalContext JSON: %v\nstdout: %s", err, stdout)
	}
	return out.HookSpecificOutput.AdditionalContext
}

// extractSupportingFilesPath pulls the path out of compactBrief's "(supporting
// files: <path>)" line — whatever ensureSkills/skillstore.Ensure actually
// returned, not an assumed layout.
func extractSupportingFilesPath(t *testing.T, ctx string) string {
	t.Helper()
	const marker = "(supporting files: "
	i := strings.Index(ctx, marker)
	if i < 0 {
		t.Fatalf("no supporting-files line in additionalContext: %q", ctx)
	}
	rest := ctx[i+len(marker):]
	j := strings.IndexByte(rest, ')')
	if j < 0 {
		t.Fatalf("unterminated supporting-files line in additionalContext: %q", ctx)
	}
	return rest[:j]
}

// extractMatchLocation pulls the trailing "— <location>" off a matched
// skill's line in compactBrief's output (either an install hint or a local
// SKILL.md path — again, whatever was actually emitted).
func extractMatchLocation(t *testing.T, ctx, name string) string {
	t.Helper()
	prefix := "- " + name + " ("
	for _, line := range strings.Split(ctx, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		const sep = " — "
		i := strings.Index(line, sep)
		if i < 0 {
			t.Fatalf("match line for %s has no location: %q", name, line)
		}
		return line[i+len(sep):]
	}
	t.Fatalf("no match line for %s in additionalContext: %q", name, ctx)
	return ""
}

// pinnedBodyLine returns the first line of a pinned skill's body — either
// its content's own first line, or the "(content omitted — ...)" pointer
// once the byte budget is exceeded.
func pinnedBodyLine(t *testing.T, ctx, name string) string {
	t.Helper()
	heading := "### Pinned: " + name + "\n"
	i := strings.Index(ctx, heading)
	if i < 0 {
		t.Fatalf("no pinned heading for %s in additionalContext: %q", name, ctx)
	}
	rest := ctx[i+len(heading):]
	j := strings.IndexByte(rest, '\n')
	if j < 0 {
		t.Fatalf("pinned section for %s has no body line: %q", name, ctx)
	}
	return rest[:j]
}

// TestSessionStartSkillsHappyPath covers a pinned skill (content inlined,
// dir materialized) and a matched skill (dir materialized, pointed at by its
// install line) both landing successfully.
func TestSessionStartSkillsHappyPath(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-1", "happy")

	tddContent := "# TDD\nRed-green-refactor.\n"
	tddArchive, tddHash := buildSkillArchive(t, tddContent)
	diagContent := "# Diagnose\nSystematic debugging.\n"
	diagArchive, diagHash := buildSkillArchive(t, diagContent)

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-1", Title: "Happy path", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned: []model.PinnedSkill{
				{Name: "tdd", Description: "Red-green-refactor discipline", Hash: tddHash, Content: tddContent},
			},
			Matches: []model.SkillMatch{
				{Name: "diagnose", Description: "Systematic debugging", Hash: diagHash, Score: 0.87},
			},
		},
	}
	back := newSkillsBackbone(t, brief)
	back.setArchive("tdd", tddHash, tddArchive)
	back.setArchive("diagnose", diagHash, diagArchive)

	stdout, _ := runSessionStart(t, wtDir, "s-skills-happy")
	ctx := additionalContext(t, stdout)

	// Pull the paths the hook actually emitted rather than assuming
	// skillstore's internal layout — Ensure owns that shape, not this test.
	pinnedPath := extractSupportingFilesPath(t, ctx)
	matchLoc := extractMatchLocation(t, ctx, "diagnose")

	wantSection := "\n## Skills\n" +
		"\n### Pinned: tdd\n" + tddContent + "\n" +
		"(supporting files: " + pinnedPath + ")\n" +
		"\n### Possibly relevant org skills\nRead the SKILL.md if relevant to this task:\n" +
		"- diagnose (0.87): Systematic debugging — " + matchLoc + "\n"
	if !strings.HasSuffix(ctx, wantSection) {
		t.Fatalf("additionalContext = %q\nwant suffix %q", ctx, wantSection)
	}

	// Verify the emitted paths actually resolve to the fetched content, not
	// just that some string got printed.
	got, err := os.ReadFile(filepath.Join(pinnedPath, "SKILL.md"))
	if err != nil || string(got) != tddContent {
		t.Fatalf("SKILL.md at pinned path %s = %q, %v; want %q", pinnedPath, got, err, tddContent)
	}
	if !strings.HasSuffix(matchLoc, "/SKILL.md") {
		t.Fatalf("match location %q should point at a SKILL.md file", matchLoc)
	}
	got, err = os.ReadFile(matchLoc)
	if err != nil || string(got) != diagContent {
		t.Fatalf("content at match location %s = %q, %v; want %q", matchLoc, got, err, diagContent)
	}
}

// TestSessionStartSkillsArchiveFetchFailure covers the warn-only discipline:
// one skill's archive 500s, the hook still exits 0 and emits the full brief,
// a warning lands on stderr, and the OTHER (pinned) skill is still installed.
// The failed match must fall back to an install hint rather than a bogus path.
func TestSessionStartSkillsArchiveFetchFailure(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-2", "archfail")

	tddContent := "# TDD\n"
	tddArchive, tddHash := buildSkillArchive(t, tddContent)
	_, diagHash := buildSkillArchive(t, "# Diagnose\n") // archive itself is never served: forced 500

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-2", Title: "Archive failure", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned:  []model.PinnedSkill{{Name: "tdd", Description: "d", Hash: tddHash, Content: tddContent}},
			Matches: []model.SkillMatch{{Name: "diagnose", Description: "Systematic debugging", Hash: diagHash, Score: 0.5}},
		},
	}
	back := newSkillsBackbone(t, brief)
	back.setArchive("tdd", tddHash, tddArchive)
	back.failArchive("diagnose")

	stdout, stderr := runSessionStart(t, wtDir, "s-archfail")
	ctx := additionalContext(t, stdout)

	if !strings.Contains(ctx, tddContent) {
		t.Fatalf("additionalContext missing pinned content: %q", ctx)
	}
	if !strings.Contains(stderr, "diagnose") {
		t.Fatalf("stderr missing warning about the failed skill: %q", stderr)
	}

	// The failed match falls back to the install hint, never a local path —
	// whatever shape a successful path would have taken.
	loc := extractMatchLocation(t, ctx, "diagnose")
	if loc != "lode skills install diagnose" {
		t.Fatalf("match location for a failed install = %q, want the install hint", loc)
	}

	// The other (pinned) skill still installed: its emitted path resolves to
	// the real content despite diagnose's failure.
	pinnedPath := extractSupportingFilesPath(t, ctx)
	got, err := os.ReadFile(filepath.Join(pinnedPath, "SKILL.md"))
	if err != nil || string(got) != tddContent {
		t.Fatalf("SKILL.md at pinned path %s = %q, %v; want %q", pinnedPath, got, err, tddContent)
	}
}

// TestSessionStartSkillsEmptySection covers a brief with no skills at all:
// no "## Skills" heading, and no crash.
func TestSessionStartSkillsEmptySection(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-3", "empty")

	brief := model.Brief{Task: model.Task{ID: "PROJ-3", Title: "No skills", State: "in_progress", Priority: "low"}}
	newSkillsBackbone(t, brief)

	stdout, _ := runSessionStart(t, wtDir, "s-empty")
	ctx := additionalContext(t, stdout)
	if strings.Contains(ctx, "## Skills") {
		t.Fatalf("additionalContext should have no Skills heading for an empty skills section: %q", ctx)
	}
}

// TestSessionStartSkillsPinnedEmptyHashSkipped covers a pinned skill with no
// hash (e.g. a skill pinned before its content synced): ensureSkills skips
// the fetch without warning, and the inline content still reaches the model.
func TestSessionStartSkillsPinnedEmptyHashSkipped(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-4", "nohash")

	draftContent := "# Draft\n"
	brief := model.Brief{
		Task: model.Task{ID: "PROJ-4", Title: "No hash", State: "in_progress", Priority: "low"},
		Skills: model.SkillRecommendation{
			Pinned: []model.PinnedSkill{{Name: "draft-skill", Description: "d", Hash: "", Content: draftContent}},
		},
	}
	newSkillsBackbone(t, brief)

	stdout, stderr := runSessionStart(t, wtDir, "s-nohash")
	ctx := additionalContext(t, stdout)
	if !strings.Contains(ctx, draftContent) {
		t.Fatalf("additionalContext missing pinned content: %q", ctx)
	}
	if strings.Contains(ctx, "supporting files") {
		t.Fatalf("a hashless pinned skill must not report a local dir: %q", ctx)
	}
	if strings.Contains(stderr, "draft-skill") {
		t.Fatalf("a hashless pinned skill must not produce a warning: %q", stderr)
	}
}

// shrinkSkillFetchBudget lowers skillsBudget/archiveTimeout/skillFetchConcurrency
// for a test and restores them on cleanup. These are vars (not consts)
// specifically so a test can avoid waiting out the real 10s budget against a
// deliberately hanging fixture; see skillsBudget's doc comment. This package
// must not adopt t.Parallel() while any test uses this.
func shrinkSkillFetchBudget(t *testing.T, budget, archive time.Duration, concurrency int) {
	t.Helper()
	origBudget, origArchive, origConcurrency := skillsBudget, archiveTimeout, skillFetchConcurrency
	skillsBudget, archiveTimeout, skillFetchConcurrency = budget, archive, concurrency
	t.Cleanup(func() {
		skillsBudget, archiveTimeout, skillFetchConcurrency = origBudget, origArchive, origConcurrency
	})
}

// TestSessionStartSkillsFetchBudgetBounded covers the fix for a measured
// regression: 1 pin + 5 matches (5 is RecommendSkills' default limit; see
// internal/api/brief.go) against a hanging archive endpoint used to cost
// 12s+ of dead air at session start, strictly linear in skill count. The
// fetch loop must instead be bounded overall by skillsBudget and run with
// bounded concurrency (skillFetchConcurrency), never serially per skill.
func TestSessionStartSkillsFetchBudgetBounded(t *testing.T) {
	shrinkSkillFetchBudget(t, 500*time.Millisecond, 500*time.Millisecond, 2)

	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-5", "budget")

	_, tddHash := buildSkillArchive(t, "# TDD\n")
	matches := make([]model.SkillMatch, 0, 5)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("match-%d", i)
		_, hash := buildSkillArchive(t, "# "+name+"\n")
		matches = append(matches, model.SkillMatch{Name: name, Description: "d", Hash: hash, Score: 0.5})
	}

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-5", Title: "Budget", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned:  []model.PinnedSkill{{Name: "tdd", Description: "d", Hash: tddHash, Content: "# TDD\n"}},
			Matches: matches,
		},
	}
	back := newSkillsBackbone(t, brief)
	back.hangArchive("tdd")
	for _, m := range matches {
		back.hangArchive(m.Name)
	}

	start := time.Now()
	stdout, stderr := runSessionStart(t, wtDir, "s-budget")
	elapsed := time.Since(start)

	// 6 skills serialized at the old 2s-per-fetch rate would be 12s; bounded
	// by a 500ms budget the whole loop — regardless of skill count — must
	// land near that budget, not near (skill count × archiveTimeout).
	if elapsed > skillsBudget+time.Second {
		t.Fatalf("session-start with 6 hanging archives took %s, want well under skillsBudget=%s", elapsed, skillsBudget)
	}

	// The concurrency cap actually held: with 6 hanging fetches and a limit
	// of 2, the server must never see more than 2 requests in flight at once.
	if peak := back.peakConcurrency(); peak != 2 {
		t.Fatalf("peak concurrent archive fetches = %d, want exactly the skillFetchConcurrency limit (2)", peak)
	}

	// The never-fail invariant holds throughout: the brief still comes
	// through, and every hung skill was warned about.
	ctx := additionalContext(t, stdout)
	if !strings.Contains(ctx, "PROJ-5") {
		t.Fatalf("additionalContext missing the brief despite hanging archives: %q", ctx)
	}
	if !strings.Contains(stderr, "tdd") {
		t.Fatalf("stderr missing warning for the hung pinned skill: %q", stderr)
	}
	for _, m := range matches {
		if !strings.Contains(stderr, m.Name) {
			t.Fatalf("stderr missing warning for hung match %s: %q", m.Name, stderr)
		}
	}
}

// TestSessionStartSkillsPinnedByteCapEmitsPointer covers the fix for the
// second measured regression: pinned content is inlined with no size bound,
// so one large SKILL.md (or several) could inject hundreds of KB into every
// session start. Once the running total of inlined pinned bytes would
// exceed maxInlinedSkillBytes, later pins get a pointer instead of their
// content — never a mid-document truncation.
func TestSessionStartSkillsPinnedByteCapEmitsPointer(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-6", "bytecap")

	small := "# Small\n"
	big := "# Big\n" + strings.Repeat("x", maxInlinedSkillBytes) // alone, guarantees overflow
	smallArchive, smallHash := buildSkillArchive(t, small)
	bigArchive, bigHash := buildSkillArchive(t, big)

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-6", Title: "Byte cap", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned: []model.PinnedSkill{
				{Name: "small", Description: "d", Hash: smallHash, Content: small},
				{Name: "big", Description: "d", Hash: bigHash, Content: big},
			},
		},
	}
	back := newSkillsBackbone(t, brief)
	back.setArchive("small", smallHash, smallArchive)
	back.setArchive("big", bigHash, bigArchive)

	stdout, _ := runSessionStart(t, wtDir, "s-bytecap")
	ctx := additionalContext(t, stdout)

	// The small pin fits under budget and is inlined in full.
	if got, want := pinnedBodyLine(t, ctx, "small"), "# Small"; got != want {
		t.Fatalf("small pin body line = %q, want %q (should be inlined in full)", got, want)
	}
	if strings.Contains(ctx, big) {
		t.Fatalf("big pin's content must not appear in full once the byte budget is exceeded")
	}

	// The big pin gets a pointer, sized and located, never a truncated body.
	line := pinnedBodyLine(t, ctx, "big")
	wantPrefix := "(content omitted — " + humanKB(len(big)) + "; read it at "
	if !strings.HasPrefix(line, wantPrefix) || !strings.HasSuffix(line, "/SKILL.md)") {
		t.Fatalf("big pin body line = %q, want prefix %q and suffix %q", line, wantPrefix, "/SKILL.md)")
	}
}

// TestSessionStartSkillsPinnedByteCapFallsBackToInstallHint covers the
// interaction the coordinator flagged between fixes 1-3: an over-budget
// pinned skill whose archive ALSO failed to fetch must point at the install
// hint, not a local path that was never actually populated.
func TestSessionStartSkillsPinnedByteCapFallsBackToInstallHint(t *testing.T) {
	root := initGitRepo(t)
	wtDir := setupFakeWorktree(t, root, "PROJ-7", "bytecapfail")

	big := "# Big\n" + strings.Repeat("y", maxInlinedSkillBytes)
	_, bigHash := buildSkillArchive(t, big)

	brief := model.Brief{
		Task: model.Task{ID: "PROJ-7", Title: "Byte cap fetch failure", State: "in_progress", Priority: "high"},
		Skills: model.SkillRecommendation{
			Pinned: []model.PinnedSkill{{Name: "big", Description: "d", Hash: bigHash, Content: big}},
		},
	}
	back := newSkillsBackbone(t, brief)
	back.failArchive("big")

	stdout, _ := runSessionStart(t, wtDir, "s-bytecapfail")
	ctx := additionalContext(t, stdout)

	got := pinnedBodyLine(t, ctx, "big")
	want := "(content omitted — " + humanKB(len(big)) + "; read it at lode skills install big)"
	if got != want {
		t.Fatalf("pinned body line = %q, want %q", got, want)
	}
}

// --- session-end reports transcript usage -----------------------------------

// sessionRecorder stands in for one of the backbone's agent-session endpoints
// and keeps every request body, so a test can assert on the exact usage posted.
type sessionRecorder struct {
	mu     sync.Mutex
	bodies []map[string]json.RawMessage
}

// newSessionRecorder serves route and records its bodies. reply is the JSON
// the route answers with; nil means 204, which is what the end route sends.
func newSessionRecorder(t *testing.T, route, reply string) *sessionRecorder {
	t.Helper()
	rec := &sessionRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, body)
		rec.mu.Unlock()
		if reply == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	return rec
}

func newEndRecorder(t *testing.T) *sessionRecorder {
	t.Helper()
	return newSessionRecorder(t, "POST /api/v1/tasks/{id}/agent-session/end", "")
}

// newTouchRecorder serves the heartbeat's own endpoint. Its reply must decode
// as an open session: reportSession skips the marker stamp on a closed one.
func newTouchRecorder(t *testing.T) *sessionRecorder {
	t.Helper()
	return newSessionRecorder(t, "POST /api/v1/tasks/{id}/agent-session",
		`{"lease_id":1,"agent":"claude-code","session_id":"sess-1"}`)
}

// only returns the single recorded body, failing if there was not exactly one.
func (r *sessionRecorder) only(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) != 1 {
		t.Fatalf("agent-session/end called %d times, want 1", len(r.bodies))
	}
	return r.bodies[0]
}

// addWorktree creates a .worktrees/<taskID>-<slug> git worktree under root.
// Unlike setupLeasedWorktree it needs no backbone: session-end's guard only
// reads the directory path.
func addWorktree(t *testing.T, root, taskID, slug string) string {
	t.Helper()
	return addWorktreeAt(t, root, ".worktrees", taskID, slug)
}

// addWorktreeAt is addWorktree with a configurable base, for tests exercising
// a non-default worktree_dir (LODE_WORKTREE_DIR).
func addWorktreeAt(t *testing.T, root, base, taskID, slug string) string {
	t.Helper()
	dir := filepath.Join(root, base, taskID+"-"+slug)
	out, err := exec.Command("git", "-C", root, "worktree", "add", dir, "-b", taskID+"-"+slug).CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return dir
}

// writeTranscript writes a JSONL transcript and returns its path.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// transcriptLine builds one assistant entry with the given identity and usage.
func transcriptLine(cwd, msgID, model string, in, write5m, write1h, read, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","cwd":%q,"timestamp":"2026-07-31T10:00:00Z",`+
		`"message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,`+
		`"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d,`+
		`"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		cwd, msgID, model, in, write5m+write1h, read, out, write5m, write1h)
}

// TestSessionEndPostsTranscriptUsage drives the whole accounting path: the
// same message id repeated across content-block lines is billed once, and a
// turn that ran in a different directory belongs to that worktree's lease, not
// this one.
func TestSessionEndPostsTranscriptUsage(t *testing.T) {
	rec := newEndRecorder(t)
	root := initGitRepo(t)
	wtDir := addWorktree(t, root, "WL-1", "bill-me")
	elsewhere := t.TempDir()

	path := writeTranscript(t,
		transcriptLine(wtDir, "msg_1", "claude-opus-5", 100, 200, 300, 400, 50),
		transcriptLine(wtDir, "msg_1", "claude-opus-5", 100, 200, 300, 400, 50), // same message, second content block
		transcriptLine(elsewhere, "msg_2", "claude-opus-5", 9_000, 9_000, 9_000, 9_000, 9_000),
	)

	runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1", TranscriptPath: path})

	body := rec.only(t)
	var usage []model.SessionUsageBucket
	if err := json.Unmarshal(body["usage"], &usage); err != nil {
		t.Fatalf("decode usage %s: %v", body["usage"], err)
	}
	want := []model.SessionUsageBucket{{
		Day: "2026-07-31", Model: "claude-opus-5", Speed: "standard",
		InputTokens: 100, CacheWrite5mTokens: 200, CacheWrite1hTokens: 300,
		CacheReadTokens: 400, OutputTokens: 50,
	}}
	if len(usage) != 1 || usage[0] != want[0] {
		t.Fatalf("posted usage = %+v, want %+v", usage, want)
	}
}

// TestHeartbeatPostsTranscriptUsage covers the only path a crashed or swept
// session's spend can reach the backbone by: usage rides the heartbeat, and
// carries the same per-worktree filtering the clean end applies. A heartbeat
// with no transcript posts no usage field at all, so it leaves whatever the
// backbone already recorded alone rather than clearing it.
func TestHeartbeatPostsTranscriptUsage(t *testing.T) {
	rec := newTouchRecorder(t)
	root := initGitRepo(t)
	wtDir := addWorktree(t, root, "WL-1", "bill-me")
	elsewhere := t.TempDir()

	path := writeTranscript(t,
		transcriptLine(wtDir, "msg_1", "claude-opus-5", 100, 200, 300, 400, 50),
		transcriptLine(elsewhere, "msg_2", "claude-opus-5", 9_000, 9_000, 9_000, 9_000, 9_000),
	)

	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1", TranscriptPath: path})

	var usage []model.SessionUsageBucket
	if err := json.Unmarshal(rec.only(t)["usage"], &usage); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	want := model.SessionUsageBucket{
		Day: "2026-07-31", Model: "claude-opus-5", Speed: "standard",
		InputTokens: 100, CacheWrite5mTokens: 200, CacheWrite1hTokens: 300,
		CacheReadTokens: 400, OutputTokens: 50,
	}
	if len(usage) != 1 || usage[0] != want {
		t.Fatalf("posted usage = %+v, want %+v", usage, want)
	}

	// A second worktree, so this heartbeat is not debounced by the first's
	// marker.
	quiet := addWorktree(t, root, "WL-2", "no-transcript")
	runHook(t, "heartbeat", Payload{Cwd: quiet, SessionID: "sess-2"})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.bodies) != 2 {
		t.Fatalf("agent-session called %d times, want 2", len(rec.bodies))
	}
	if raw, ok := rec.bodies[1]["usage"]; ok {
		t.Fatalf("heartbeat with no transcript posted usage %s, want the key absent", raw)
	}
}

// A hook must never fail its triggering event, so an unreadable transcript
// still ends the session — just with no usage attached.
func TestSessionEndWithoutTranscriptStillEndsSession(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"absent field", ""},
		{"missing file", filepath.Join(t.TempDir(), "gone.jsonl")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := newEndRecorder(t)
			root := initGitRepo(t)
			wtDir := addWorktree(t, root, "WL-2", "no-transcript")

			runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1", TranscriptPath: tc.path})
			body := rec.only(t)
			if got := string(body["usage"]); got != "null" {
				t.Fatalf("usage = %s, want null (nil must leave stored usage alone)", got)
			}
		})
	}
}
