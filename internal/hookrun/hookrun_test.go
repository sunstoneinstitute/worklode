package hookrun

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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

// expireLease force-expires taskID's lease via the store, mirroring the
// sweeper (see TestWorktreeCreateAutoResumesExpiredLease), so a later Brief
// call returns Lease == nil.
func expireLease(t *testing.T, st *store.Store, taskID string) {
	t.Helper()
	if _, err := st.ExpireLeases(context.Background(), time.Now().Add(3*time.Hour)); err != nil {
		t.Fatalf("expire lease on %s: %v", taskID, err)
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

// newUsageRecorder serves the consolidated session-usage endpoint, which is
// where a session's billed tokens go since spec 052 §3. The lifecycle routes
// (touch/end) 404 into a warning, which a hook downgrades and survives.
func newUsageRecorder(t *testing.T) *sessionRecorder {
	t.Helper()
	return newSessionRecorder(t, "POST /api/v1/projects/{id}/session-usage", "")
}

// byTask decodes the recorded classification of the single usage report.
func (r *sessionRecorder) byTask(t *testing.T) map[string][]model.SessionUsageBucket {
	t.Helper()
	var out map[string][]model.SessionUsageBucket
	if err := json.Unmarshal(r.only(t)["by_task"], &out); err != nil {
		t.Fatalf("decode by_task: %v", err)
	}
	return out
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
		t.Fatalf("recorded route called %d times, want 1", len(r.bodies))
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
