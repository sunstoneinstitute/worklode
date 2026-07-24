// Package hookrun implements the logic behind `lode hook <event>`: the editor
// and git lifecycle hooks that keep a Worklode lease alive around a coding
// session without ever failing the event that triggered them.
//
// Two rules govern every handler:
//
//   - Worklode's own action is GUARDED. Unless the working directory resolves
//     to a wt/<id>-<slug> worktree (worktree.Root → worktree.ParseDir), the
//     handler does nothing.
//   - Worklode NEVER fails the event. Every backbone call runs under a short
//     timeout and any error (no config, network, 4xx/5xx) is downgraded to a
//     stderr warning; the hook still exits 0. The only non-zero exit is the
//     child's own exit code when daisy-chaining (see Run).
package hookrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// backboneTimeout bounds every backbone call so a slow/unreachable server can
// never stall an editor or git event.
const backboneTimeout = 2 * time.Second

// leaseRenewWindow is how close to expiry a still-held lease must be for
// session-start to proactively renew it.
const leaseRenewWindow = 30 * time.Minute

// sessionMarkerFile is written in the worktree-private git dir to mark a live
// coding session; see the marker helpers below.
const sessionMarkerFile = "worklode-session.json"

// Payload is the subset of a hook's stdin JSON that Worklode reads. Claude
// Code sends cwd/session_id/hook_event_name/tool_input; a git pre-commit hook
// sends no stdin at all, so every field is optional.
type Payload struct {
	Cwd           string          `json:"cwd"`
	SessionID     string          `json:"session_id"`
	HookEventName string          `json:"hook_event_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

// Options configures a single Run. Stdin/Stdout/Stderr are injected so the
// package is testable; NewClient and Now default to the real config-backed
// client and time.Now when nil.
type Options struct {
	Event  string
	Next   []string // downstream command + argv after --next; nil ⇒ no chain
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	NewClient func() (*cli.Client, error)
	Now       func() time.Time
}

func (o Options) client() (*cli.Client, error) {
	if o.NewClient != nil {
		return o.NewClient()
	}
	return defaultClient()
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// defaultClient builds the backbone client from the on-disk config plus the
// LODE_SERVER/LODE_TOKEN overrides.
func defaultClient() (*cli.Client, error) {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.ServerURL == "" {
		return nil, errors.New(`server URL not set (LODE_SERVER or ~/.config/worklode/config.toml)`)
	}
	return cli.NewClient(cfg), nil
}

// knownEvents is the accepted <event> set.
var knownEvents = map[string]bool{
	"session-start":   true,
	"session-end":     true,
	"pre-commit":      true,
	"worktree-create": true,
	"worktree-remove": true,
}

// Run executes one hook invocation and returns the process exit code. It reads
// and buffers the entire payload from opts.Stdin, dispatches Worklode's own
// (guarded, never-failing) action for the event, and — regardless of whether
// that action did anything — runs the --next downstream command if present,
// replaying the original payload on its stdin and propagating its exit code.
// Without --next it always returns 0.
func Run(ctx context.Context, opts Options) int {
	raw, _ := io.ReadAll(opts.Stdin) // tolerate read errors / empty stdin

	var payload Payload
	_ = json.Unmarshal(raw, &payload) // tolerate empty / non-JSON stdin

	dir := resolveDir(payload)
	dispatch(ctx, opts, payload, dir)

	if len(opts.Next) > 0 {
		return runNext(opts, raw)
	}
	return 0
}

// resolveDir picks the working directory: the payload's cwd, else $PWD, else
// the process working directory.
func resolveDir(p Payload) string {
	if p.Cwd != "" {
		return p.Cwd
	}
	if pwd := os.Getenv("PWD"); pwd != "" {
		return pwd
	}
	wd, _ := os.Getwd()
	return wd
}

func dispatch(ctx context.Context, opts Options, p Payload, dir string) {
	switch opts.Event {
	case "session-start":
		handleSessionStart(ctx, opts, p, dir)
	case "session-end":
		handleSessionEnd(opts, dir)
	case "pre-commit":
		handlePreCommit(ctx, opts, dir)
	case "worktree-create":
		handleWorktreeCreate(ctx, opts, p, dir)
	case "worktree-remove":
		handleWorktreeRemove(ctx, opts, p, dir)
	default:
		warn(opts, "unknown hook event %q", opts.Event)
	}
}

// runNext spawns the downstream hook, feeding it the original payload bytes on
// stdin and wiring its stdout/stderr through, then returns its exit code. A
// failure to even start the child is a warning and exit 1 (this is the
// downstream chain, not a Worklode action, so a non-zero code is legitimate).
func runNext(opts Options, raw []byte) int {
	cmd := exec.Command(opts.Next[0], opts.Next[1:]...) //nolint:gosec // caller-supplied downstream hook
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		warn(opts, "run --next %v: %v", opts.Next, err)
		return 1
	}
	return 0
}

// --- event handlers ---------------------------------------------------------

func handleSessionStart(ctx context.Context, opts Options, p Payload, dir string) {
	root, ok := worktree.Root(dir)
	if !ok {
		return // not in a git repo ⇒ NOP
	}
	taskID, ok := worktree.ParseDir(root)
	if !ok {
		offerScan(ctx, opts, root)
		return
	}

	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	identity, err := worktree.Identity(root)
	if err != nil {
		warn(opts, "resolve worktree identity: %v", err)
		return
	}

	bctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	brief, _, err := c.Brief(bctx, taskID)
	cancel()
	if err != nil {
		warn(opts, "fetch brief for %s: %v", taskID, err)
		return
	}

	ensureLease(ctx, opts, c, taskID, identity, brief.Lease)

	if err := writeSessionMarker(root, p.SessionID); err != nil {
		warn(opts, "write session marker: %v", err)
	}

	emitAdditionalContext(opts.Stdout, compactBrief(brief))
}

// ensureLease keeps this worktree's lease healthy at session start: renew a
// still-held lease that is within the renew window, and otherwise re-acquire
// (renew if still nominally ours, re-claim if the sweeper took it). A lease
// held elsewhere, or any backbone error, is a warning only.
func ensureLease(ctx context.Context, opts Options, c *cli.Client, taskID, identity string, lease *cli.Lease) {
	now := opts.now()
	if lease != nil && lease.Worktree == identity && lease.ExpiresAt.After(now) {
		if lease.ExpiresAt.Sub(now) >= leaseRenewWindow {
			return // still ours with plenty of headroom
		}
		lctx, cancel := context.WithTimeout(ctx, backboneTimeout)
		defer cancel()
		if _, _, err := c.RenewLease(lctx, taskID, 0); err != nil {
			warn(opts, "renew lease on %s: %v", taskID, err)
		}
		return
	}
	lctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	if err := cli.ReacquireOrRenew(lctx, c, taskID, identity, lease); err != nil {
		warn(opts, "re-acquire lease on %s: %v", taskID, err)
	}
}

// offerScan runs at session start OUTSIDE a worktree: it looks at the sibling
// wt/* directories under the repo root and, for up to five that parse as
// Worklode worktrees, flags any whose lease is expired/absent and whose
// session marker is stale/absent as adoptable. No claim, no model call.
func offerScan(ctx context.Context, opts Options, repoRoot string) {
	entries, err := os.ReadDir(filepath.Join(repoRoot, "wt"))
	if err != nil {
		return // no wt/ dir ⇒ nothing to offer
	}

	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}

	now := opts.now()
	var lines []string
	fetched := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wtDir := filepath.Join(repoRoot, "wt", e.Name())
		taskID, ok := worktree.ParseDir(wtDir)
		if !ok {
			continue
		}
		if fetched >= 5 {
			break
		}
		fetched++

		bctx, cancel := context.WithTimeout(ctx, backboneTimeout)
		brief, _, err := c.Brief(bctx, taskID)
		cancel()
		if err != nil {
			continue // best-effort per worktree
		}

		leaseGone := brief.Lease == nil || brief.Lease.ExpiresAt.Before(now)
		if leaseGone && !sessionMarkerFresh(wtDir) {
			rel := "wt/" + e.Name()
			lines = append(lines, fmt.Sprintf(
				"Worklode worktree %s (%s: %s) is abandoned — `/lode:resume %s` to adopt it.",
				rel, taskID, brief.Task.Title, rel))
		}
	}

	if len(lines) > 0 {
		emitAdditionalContext(opts.Stdout, strings.Join(lines, "\n"))
	}
}

func handleSessionEnd(opts Options, dir string) {
	root, ok := worktree.Root(dir)
	if !ok {
		return
	}
	if _, ok := worktree.ParseDir(root); !ok {
		return
	}
	if err := removeSessionMarker(root); err != nil {
		warn(opts, "remove session marker: %v", err)
	}
}

func handlePreCommit(ctx context.Context, opts Options, dir string) {
	root, ok := worktree.Root(dir)
	if !ok {
		return
	}
	taskID, ok := worktree.ParseDir(root)
	if !ok {
		return
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	if _, _, err := c.RenewLease(cctx, taskID, 0); err != nil {
		// Expired-and-swept (e.g. 404) or any other failure must never block a
		// commit.
		warn(opts, "renew lease on %s: %v (commit not blocked)", taskID, err)
	}
}

func handleWorktreeCreate(ctx context.Context, opts Options, p Payload, dir string) {
	created := payloadPath(p, dir)
	taskID, ok := worktree.ParseDir(created)
	if !ok {
		return // not a wt/ dir (or unknown path) ⇒ NOP
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	identity, err := worktree.Identity(created)
	if err != nil {
		warn(opts, "resolve worktree identity: %v", err)
		return
	}
	bctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	brief, _, err := c.Brief(bctx, taskID)
	cancel()
	if err != nil {
		warn(opts, "fetch brief for %s: %v", taskID, err)
		return
	}
	leaseGone := brief.Lease == nil || brief.Lease.ExpiresAt.Before(opts.now())
	if leaseGone && !sessionMarkerFresh(created) {
		rctx, cancel := context.WithTimeout(ctx, backboneTimeout)
		defer cancel()
		if err := cli.ReacquireOrRenew(rctx, c, taskID, identity, brief.Lease); err != nil {
			warn(opts, "auto-resume %s: %v", taskID, err)
		}
	}
}

func handleWorktreeRemove(ctx context.Context, opts Options, p Payload, dir string) {
	removed := payloadPath(p, dir)
	taskID, ok := worktree.ParseDir(removed)
	if !ok {
		return
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	rctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	if _, err := c.ReleaseLease(rctx, taskID); err != nil {
		warn(opts, "release lease on %s: %v", taskID, err)
	}
}

// payloadPath returns the created/removed worktree path from the payload's
// tool_input, falling back to the resolved cwd (dir). The exact tool_input
// field name is not contractually fixed, so pathFromToolInput searches
// defensively; ParseDir then decides whether the result is a wt/ dir.
func payloadPath(p Payload, dir string) string {
	if path := pathFromToolInput(p.ToolInput); path != "" {
		return path
	}
	return dir
}

// pathFromToolInput extracts a filesystem path from a tool_input object,
// checking the likely key names first and then any key containing "path".
func pathFromToolInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, k := range []string{"path", "worktree_path", "created_path", "removed_path", "dir", "directory", "target", "destination"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	for k, v := range m {
		if s, ok := v.(string); ok && s != "" && strings.Contains(strings.ToLower(k), "path") {
			return s
		}
	}
	return ""
}

// --- Claude Code output -----------------------------------------------------

// emitAdditionalContext writes a SessionStart additionalContext object to
// stdout — the documented way a session-start hook injects context.
func emitAdditionalContext(w io.Writer, text string) {
	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "SessionStart",
			"additionalContext": text,
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	fmt.Fprintln(w, string(b))
}

// compactBrief renders a brief as a few lines of plain text suitable for
// injecting as session context.
func compactBrief(b cli.Brief) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: %s [%s, %s]", b.Task.ID, b.Task.Title, b.Task.State, b.Task.Priority)
	if b.Branch != "" {
		fmt.Fprintf(&sb, "\nbranch: %s", b.Branch)
	}
	if body := strings.TrimSpace(b.Body); body != "" {
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			body = body[:i]
		}
		fmt.Fprintf(&sb, "\n%s", body)
	}
	if len(b.OpenBlockers) > 0 {
		names := make([]string, 0, len(b.OpenBlockers))
		for _, blk := range b.OpenBlockers {
			names = append(names, blk.ID)
		}
		fmt.Fprintf(&sb, "\nopen blockers: %s", strings.Join(names, ", "))
	}
	return sb.String()
}

// --- session marker ---------------------------------------------------------

// sessionMarker records the process owning a live coding session in a
// worktree. A marker is stale once its pid is no longer alive.
type sessionMarker struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

// markerPath returns the marker file path inside root's worktree-private git
// dir.
func markerPath(root string) (string, error) {
	gitDir, err := worktree.GitDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDir, sessionMarkerFile), nil
}

// writeSessionMarker writes the current process's session marker for root.
func writeSessionMarker(root, sessionID string) error {
	path, err := markerPath(root)
	if err != nil {
		return err
	}
	b, err := json.Marshal(sessionMarker{
		SessionID: sessionID,
		PID:       os.Getpid(),
		StartedAt: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// removeSessionMarker deletes root's session marker; a missing marker is fine.
func removeSessionMarker(root string) error {
	path, err := markerPath(root)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// sessionMarkerFresh reports whether root has a session marker whose recorded
// pid is still alive. An absent/unreadable marker, or a dead pid, is stale.
func sessionMarkerFresh(root string) bool {
	path, err := markerPath(root)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var m sessionMarker
	if json.Unmarshal(data, &m) != nil || m.PID <= 0 {
		return false
	}
	return pidAlive(m.PID)
}

// pidAlive reports whether pid names a live process (signal 0 probe). Any
// error — ESRCH in particular — is treated as dead.
func pidAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// warn prints a non-fatal hook warning to stderr. Warnings never change the
// exit code — stderr is the safe channel that can't corrupt an editor's hook
// output stream.
func warn(opts Options, format string, args ...any) {
	if opts.Stderr == nil {
		return
	}
	fmt.Fprintf(opts.Stderr, "worklode hook: "+format+"\n", args...)
}
