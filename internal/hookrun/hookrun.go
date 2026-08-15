// Package hookrun implements the logic behind `lode hook <event>`: the editor
// and git lifecycle hooks that keep a Worklode lease alive around a coding
// session without ever failing the event that triggered them.
//
// Two rules govern every handler:
//
//   - Worklode's own action is GUARDED. Unless the working directory resolves
//     to a Worklode worktree (worktree.Root → Layout.TaskID), the handler
//     does nothing.
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
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/skillstore"
	"github.com/sunstoneinstitute/worklode/internal/transcript"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// backboneTimeout bounds every backbone call so a slow/unreachable server can
// never stall an editor or git event.
const backboneTimeout = 2 * time.Second

// archiveTimeout bounds one skill archive fetch. Deliberately larger than
// backboneTimeout: archives are bulk payloads, not JSON round trips, and a
// truncated fetch degrades the feature silently (pinned content still
// inlines from the brief; only supporting files and matched dirs vanish).
// A var, not a const — see skillsBudget's note on why, and its note on
// t.Parallel().
var archiveTimeout = 10 * time.Second

// skillsBudget bounds the ENTIRE skill-fetch loop — every pin plus every
// match, however many the brief carries. Without an overall cap, a brief
// with several matches against a hanging endpoint would cost one
// archiveTimeout per skill, serialized; session-start is exactly when an
// agent is waiting to start work. A var, not a const, so tests can shrink
// it (and archiveTimeout/skillFetchConcurrency) instead of waiting out the
// real budget against a deliberately hanging fixture; this package must not
// adopt t.Parallel() while any test relies on that pattern (mirrors
// internal/skillstore's maxExtracted/maxEntries).
var skillsBudget = 10 * time.Second

// skillFetchConcurrency bounds how many archive fetches run at once.
// skillstore.Ensure is safe for concurrent callers (tmp-dir + rename with a
// race-loser fallback, and swapSymlink is tmp+rename too), so only the
// paths map built up here needs its own lock.
var skillFetchConcurrency = 4

// leaseRenewWindow is how close to expiry a still-held lease must be for
// session-start to proactively renew it.
const leaseRenewWindow = 30 * time.Minute

// heartbeatDebounce is the minimum gap between two agent-session heartbeats
// reported from one worktree. Stop fires per assistant turn, which in a fast
// conversation is several times a minute; the backbone does not need that.
const heartbeatDebounce = time.Minute

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
	// TranscriptPath is the session's JSONL transcript, sent on SessionEnd and
	// Stop. It is where the session's billed tokens come from.
	TranscriptPath string `json:"transcript_path"`
}

// Options configures a single Run. Stdin/Stdout/Stderr are injected so the
// package is testable; NewClient and Now default to the real config-backed
// client and time.Now when nil.
type Options struct {
	Event string
	// Args is the triggering event's own positional arguments — git's, for
	// the hooks that take any. commit-msg's $1 is the message file.
	Args   []string
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

// layoutFor resolves the worktree layout for dir — the payload cwd (the same
// directory resolveDir picks), never the process cwd. Spec 008 §5.1: a hook
// running inside a worktree resolves the base directory from its OWN cwd,
// because .worklode/config.toml is repo content checked out into every
// worktree; resolving from os.Getwd() (as cli.LoadConfig does) can silently
// pick up the wrong repo's config when the two diverge.
//
// This deliberately does not call cli.LoadConfig: LoadConfig's keychain
// token lookup costs ~9ms of subprocess time (go-keyring -> `security
// find-generic-password`), and this runs ahead of the ParseDir guard on
// every hook event — including heartbeat, which fires per assistant turn.
// cli.WorktreeDirFrom reads only the repo-local config file (plus one env
// var); no keychain, no token, no network.
//
// Resolved once per Run(), not per handler or per ParseDir call. A malformed
// worktree_dir (NewLayout rejects it, e.g. an absolute path) degrades to the
// default layout with a warning rather than erroring out — a hook must never
// fail an event.
func layoutFor(opts Options, dir string) worktree.Layout {
	l, err := worktree.NewLayout(cli.WorktreeDirFrom(dir))
	if err != nil {
		warn(opts, "resolve worktree layout: %v (using %s)", err, worktree.DefaultBase)
		l, _ = worktree.NewLayout("")
	}
	return l
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

// Event is one accepted `lode hook <event>`.
type Event struct {
	Name    string
	Summary string // one line, present tense: what the handler does
}

// events is the accepted <event> set, in lifecycle order, and the source of
// truth for `lode hook --list` and the command's own help. dispatch below must
// name exactly these — TestEventsAreDispatched holds the two together.
var events = []Event{
	{"session-start", "Renew the lease, open the agent session, inject the brief."},
	{"heartbeat", "Report the session as still alive (at most once a minute)."},
	{"session-end", "Close the agent session and bill its tokens to the lease."},
	{"pre-commit", "Push the lease TTL out on commit; never blocks the commit."},
	{"commit-msg", "Stamp the Worklode-Task trailer into a commit made in a task worktree."},
	{"post-merge", "Report a merge that landed on the default branch in this clone."},
	{"post-commit", "Same, for the squash and conflict-resolution merges post-merge never sees."},
	{"worktree-create", "Auto-resume the task's lease when its worktree is created."},
	{"worktree-remove", "Release the task's lease when its worktree is removed."},
	{"worktree-enter", "Open an agent session against the worktree just entered."},
	{"worktree-exit", "Close the agent session on the worktree just left."},
}

// Events returns the accepted hook events in lifecycle order.
func Events() []Event { return slices.Clone(events) }

// EventNames returns just the names, in the same order.
func EventNames() []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.Name)
	}
	return names
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
	l := layoutFor(opts, dir)
	dispatch(ctx, opts, payload, dir, l)

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

func dispatch(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	switch opts.Event {
	case "session-start":
		handleSessionStart(ctx, opts, p, dir, l)
	case "session-end":
		handleSessionEnd(ctx, opts, p, dir, l)
	case "pre-commit":
		handlePreCommit(ctx, opts, dir, l)
	case "commit-msg":
		// No backbone call and no context: the task id is already on disk, and
		// a commit must not wait on the network to get its trailer.
		handleCommitMsg(opts, dir, l)
	case "post-merge", "post-commit":
		// No worktree lookup: these run in the main clone, where there is no
		// lease to find. The guard is per-handler (HEAD must be the default
		// branch), so nothing global relaxes.
		handleLocalMerge(ctx, opts, dir)
	case "worktree-create":
		handleWorktreeCreate(ctx, opts, p, dir, l)
	case "worktree-remove":
		handleWorktreeRemove(ctx, opts, p, dir, l)
	case "heartbeat":
		handleHeartbeat(ctx, opts, p, dir, l)
	case "worktree-enter":
		handleWorktreeEnter(ctx, opts, p, dir, l)
	case "worktree-exit":
		handleWorktreeExit(ctx, opts, p, dir, l)
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

// agentName is the coding agent reporting this hook. Claude Code is the only
// integration today, so it is the default; other agents set LODE_AGENT. It is
// an env var rather than a flag because `lode hook` disables flag parsing so
// the --next argv passes through verbatim.
func agentName() string {
	if a := os.Getenv("LODE_AGENT"); a != "" {
		return a
	}
	return "claude-code"
}

// reportSession reports an agent session on taskID and stamps the marker's
// heartbeat time. Like every hookrun backbone call it is bounded and
// downgrades failure to a warning.
//
// transcriptPath, when set, also reports what the session has billed in root
// so far. Only a clean end reports usage otherwise, so a crashed agent — or
// one whose lease the sweeper expires — would never have its spend recorded
// at all. The report is a running total and replaces the stored one, so the
// last heartbeat before the crash is what survives. Callers with no
// transcript (pre-commit, which reads only the marker) pass "".
//
// A touch can come back with EndedAt set: the lease closed between this
// call's ActiveLease check and its write, so the store left the session
// closed instead of reopening it (see TouchAgentSession's doc comment). That
// is not a heartbeat — stamping the marker here would suppress the next
// real one for up to heartbeatDebounce, so the marker is only stamped when
// the returned session is actually open.
func reportSession(ctx context.Context, opts Options, c *cli.Client, taskID, root, sessionID, transcriptPath string) {
	if sessionID == "" {
		return
	}
	// Parsed before the timeout below is started, for the reason endSession
	// gives: a transcript is local file IO and must not eat the backbone
	// budget.
	usage := sessionUsage(opts, transcriptPath, root)

	sctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	sess, _, err := c.TouchAgentSession(sctx, taskID, agentName(), "", sessionID, usage)
	if err != nil {
		warn(opts, "report agent session on %s: %v", taskID, err)
		return
	}
	if sess.EndedAt != nil {
		return
	}
	if err := recordHeartbeat(root, opts.now()); err != nil {
		warn(opts, "record heartbeat: %v", err)
	}
}

// endSession ends an agent session on taskID, reporting the tokens it billed
// in root. Mirrors reportSession's shape — bounded, downgrades failure to a
// warning — so the two halves of a session's lifecycle (touch/end) enforce the
// same timeout-and-warn contract in exactly one place each.
func endSession(ctx context.Context, opts Options, taskID, sessionID, transcriptPath, root string) {
	if sessionID == "" {
		return
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	// Parsed before the timeout below is started: a transcript is local file
	// IO and can run to hundreds of megabytes, and reading it must not eat the
	// backbone budget.
	usage := sessionUsage(opts, transcriptPath, root)

	ectx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	if err := c.EndAgentSession(ectx, taskID, cli.EndAgentSessionInput{
		Agent: agentName(), SessionID: sessionID, Usage: usage,
	}); err != nil {
		warn(opts, "end agent session on %s: %v", taskID, err)
	}
}

// sessionUsage reads the session's transcript and returns the buckets that
// billed against root.
//
// Root is load-bearing: one session can work several worktrees in sequence
// against a single cumulative transcript, so filtering by the directory each
// turn ran in is what stops the same tokens being billed to two leases.
//
// No failure here is fatal — a missing path, an unreadable file, or an empty
// result all yield nil, which leaves the backbone's stored usage untouched and
// still lets the session end.
func sessionUsage(opts Options, transcriptPath, root string) []model.SessionUsageBucket {
	if transcriptPath == "" {
		return nil
	}
	buckets, err := transcript.ParseFile(transcriptPath, transcript.Options{Root: root})
	if err != nil {
		warn(opts, "parse transcript %s: %v", transcriptPath, err)
		return nil
	}
	out := make([]model.SessionUsageBucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, model.SessionUsageBucket{
			Day:                b.Day.Format(time.DateOnly),
			Model:              b.Model,
			Speed:              b.Speed,
			InputTokens:        b.Usage.Input,
			CacheWrite5mTokens: b.Usage.CacheWrite5m,
			CacheWrite1hTokens: b.Usage.CacheWrite1h,
			CacheReadTokens:    b.Usage.CacheRead,
			OutputTokens:       b.Usage.Output,
		})
	}
	if len(out) == 0 {
		return nil // an empty slice would clear stored usage; nothing was read
	}
	return out
}

// --- event handlers ---------------------------------------------------------

func handleSessionStart(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	root, ok := worktree.Root(dir)
	if !ok {
		return // not in a git repo ⇒ NOP
	}
	taskID, ok := l.TaskID(root)
	if !ok {
		offerScan(ctx, opts, root, l)
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

	if err := writeSessionMarker(root, p.SessionID, opts.now()); err != nil {
		warn(opts, "write session marker: %v", err)
	}
	reportSession(ctx, opts, c, taskID, root, p.SessionID, p.TranscriptPath)

	skillPaths := ensureSkills(ctx, opts, c, brief)
	emitAdditionalContext(opts.Stdout, compactBrief(brief, skillPaths))
}

// ensureSkills lazily fetches brief-referenced skill archives into the local
// content-addressed store, bounded-parallel and bounded overall by
// skillsBudget: however many skills a brief carries, this can never cost
// more than one budget's worth of dead air at session start. Failures are
// warnings: the pinned content is already inline in the brief, and
// recommended skills degrade to an install hint. Returns name -> local path
// for the ones that are present.
func ensureSkills(ctx context.Context, opts Options, c *cli.Client, b model.Brief) map[string]string {
	root, err := skillstore.Root()
	if err != nil {
		warn(opts, "skill store: %v", err)
		return nil
	}

	sctx, cancel := context.WithTimeout(ctx, skillsBudget)
	defer cancel()

	var mu sync.Mutex
	paths := map[string]string{}
	g, gctx := errgroup.WithContext(sctx)
	g.SetLimit(skillFetchConcurrency)

	ensure := func(name, hash string) {
		if hash == "" {
			return
		}
		g.Go(func() error {
			actx, cancel := context.WithTimeout(gctx, archiveTimeout)
			defer cancel()
			p, err := skillstore.Ensure(root, name, hash, func() ([]byte, error) {
				return c.SkillArchive(actx, name, hash)
			})
			// mu also guards opts.Stderr: warn's writer (a *bytes.Buffer in
			// tests) is not safe for concurrent use, and up to
			// skillFetchConcurrency fetches can land here at once.
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				warn(opts, "skill %s: %v (run: lode skills install %s)", name, err, name)
				return nil // one skill's failure must never abort the others
			}
			paths[name] = p
			return nil
		})
	}
	for _, p := range b.Skills.Pinned {
		ensure(p.Name, p.Hash)
	}
	for _, m := range b.Skills.Matches {
		ensure(m.Name, m.Hash)
	}
	_ = g.Wait() // every branch above returns nil; errors are warned in place
	return paths
}

// ensureLease keeps this worktree's lease healthy at session start: renew a
// still-held lease that is within the renew window, and otherwise re-acquire
// (renew if still nominally ours, re-claim if the sweeper took it). A lease
// held elsewhere, or any backbone error, is a warning only.
func ensureLease(ctx context.Context, opts Options, c *cli.Client, taskID, identity string, lease *model.Lease) {
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

// offerScan runs at session start OUTSIDE a worktree: it reads the configured
// worktree base directory under the repo root and, for up to five entries that
// parse as Worklode worktrees, flags any whose lease is expired/absent and
// whose session marker is stale/absent as adoptable. No claim, no model call.
//
// One flat ReadDir, not a walk: the layout puts every worktree exactly one
// level below the base (spec 008 §5.1), so there is nothing deeper to find.
func offerScan(ctx context.Context, opts Options, repoRoot string, l worktree.Layout) {
	base := filepath.Join(repoRoot, filepath.FromSlash(l.Base()))
	entries, err := os.ReadDir(base)
	if err != nil {
		return // no base dir ⇒ nothing to offer
	}

	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}

	now := opts.now()
	var lines []string
	scanned := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Cap before resolving the id, not after: TaskID shells out to git
		// for every candidate, so a repo with many worktrees would otherwise
		// pay a subprocess per directory to use at most five of them.
		if scanned >= 5 {
			break
		}
		scanned++

		wtDir := filepath.Join(base, e.Name())
		taskID, ok := l.TaskID(wtDir)
		if !ok {
			continue
		}

		bctx, cancel := context.WithTimeout(ctx, backboneTimeout)
		brief, _, briefErr := c.Brief(bctx, taskID)
		cancel()
		if briefErr != nil {
			continue // best-effort per worktree
		}

		leaseGone := brief.Lease == nil || brief.Lease.ExpiresAt.Before(now)
		if leaseGone && !sessionMarkerFresh(wtDir) {
			shown := filepath.ToSlash(filepath.Join(l.Base(), e.Name()))
			lines = append(lines, fmt.Sprintf(
				"Worklode worktree %s (%s: %s) is abandoned — `/lode:resume %s` to adopt it.",
				shown, taskID, brief.Task.Title, shown))
		}
	}

	if len(lines) > 0 {
		emitAdditionalContext(opts.Stdout, strings.Join(lines, "\n"))
	}
}

func handleSessionEnd(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	root, ok := worktree.Root(dir)
	if !ok {
		return
	}
	taskID, ok := l.TaskID(root)
	if !ok {
		return
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID, _ = markerSessionID(root)
	}
	endSession(ctx, opts, taskID, sessionID, p.TranscriptPath, root)
	if err := removeSessionMarker(root); err != nil {
		warn(opts, "remove session marker: %v", err)
	}
}

// handlePreCommit keeps this worktree's lease alive across a long working
// session: every commit pushes the TTL out.
//
// It reads the brief first so it can tell the two cases apart. Committing in a
// worktree that holds no lease of ours is ordinary — the lease was swept, or
// released, or the task was already delivered (delivery does not close leases;
// only release, abandon, reopen and the expiry sweep do) — and there is
// nothing to renew and nothing to warn about. Re-claiming here is deliberately
// not done: a git hook must not silently take a claim, and a claim on an
// already-delivered task would fail anyway. Session start and worktree create
// own re-acquisition (ensureLease).
//
// The session report is gated on the same answer: an agent session hangs off
// the active lease, so with no lease of ours it would 404 for exactly the same
// benign reason. Nothing here can block a commit.
func handlePreCommit(ctx context.Context, opts Options, dir string, l worktree.Layout) {
	root, ok := worktree.Root(dir)
	if !ok {
		return
	}
	taskID, ok := l.TaskID(root)
	if !ok {
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
		warn(opts, "fetch brief for %s: %v (commit not blocked)", taskID, err)
		return
	}
	if brief.Lease == nil || brief.Lease.Worktree != identity {
		return // no lease of ours: benign, and not ours to take
	}

	rctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	if _, _, err := c.RenewLease(rctx, taskID, 0); err != nil {
		// Any failure must never block a commit.
		warn(opts, "renew lease on %s: %v (commit not blocked)", taskID, err)
	}

	if heartbeatDue(root, opts.now()) {
		sessionID, _ := markerSessionID(root)
		// No payload, so no transcript: a git hook is not the place to
		// find one, and the next heartbeat reports the spend anyway.
		reportSession(ctx, opts, c, taskID, root, sessionID, "")
	}
}

func handleWorktreeCreate(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	created := payloadPath(p, dir)
	taskID, ok := l.TaskID(created)
	if !ok {
		return // not under the worktree base dir (or unknown path) ⇒ NOP
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

func handleWorktreeRemove(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	removed := payloadPath(p, dir)
	taskID, ok := l.TaskID(removed)
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

// handleHeartbeat reports that this worktree's session is still alive. Bound
// to Stop, StopFailure, SubagentStop and Notification: between them they cover
// a session that finishes a turn, dies on an API error, spends a long turn in
// subagents, or sits blocked on a human. Debounced against the marker so a
// fast conversation does not flood the backbone.
//
// The marker is the authority for both the session id and the debounce
// window, and this handler keeps it that way: a missing marker is self-healed
// (a worktree that lost its marker would otherwise go silent forever), and a
// marker recording a different session id than the payload (e.g. after a
// /clear) is brought up to date rather than left stale for the next
// marker-only caller (pre-commit). Either case reports immediately — a
// debounce window that was never actually started for this id is not one to
// wait out.
func handleHeartbeat(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	root, ok := worktree.Root(dir)
	if !ok {
		return
	}
	taskID, ok := l.TaskID(root)
	if !ok {
		return
	}

	m, hasMarker := readSessionMarker(root)
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = m.SessionID
	}
	if sessionID == "" {
		return // no marker and no payload id ⇒ nothing to report
	}

	if !hasMarker || (p.SessionID != "" && p.SessionID != m.SessionID) {
		if err := writeSessionMarker(root, sessionID, opts.now()); err != nil {
			warn(opts, "write session marker: %v", err)
		}
	} else if !heartbeatDue(root, opts.now()) {
		return
	}

	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	reportSession(ctx, opts, c, taskID, root, sessionID, p.TranscriptPath)
}

// handleWorktreeEnter reports the session against the lease of the worktree it
// just moved into. One session can work several tasks in sequence; each gets
// its own row, keyed by (lease, agent, session id). It writes the marker here
// too — symmetric with handleSessionStart — because the session is now live
// in THIS worktree: without a marker, heartbeats here would debounce off
// forever (no marker ⇒ nothing due) and sessionMarkerFresh would read this
// worktree as abandoned while it is actively being worked.
func handleWorktreeEnter(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	entered := payloadPath(p, dir)
	root, ok := worktree.Root(entered)
	if !ok {
		return
	}
	taskID, ok := l.TaskID(root)
	if !ok {
		return
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	// An empty session id would make a marker that only confuses
	// markerSessionID/heartbeatDue (both treat "" as "no session"), so only
	// write one when there is a real id to write.
	if p.SessionID != "" {
		if err := writeSessionMarker(root, p.SessionID, opts.now()); err != nil {
			warn(opts, "write session marker: %v", err)
		}
	}
	reportSession(ctx, opts, c, taskID, root, p.SessionID, p.TranscriptPath)
}

// handleWorktreeExit closes the session's row on the worktree it is leaving
// and removes its marker — symmetric with handleSessionEnd.
//
// Alone among the worktree hooks it requires an explicit tool_input path and
// never falls back to the payload cwd (dir is unused, kept for dispatch
// symmetry): by the time a PostToolUse fires, cwd is the worktree being
// returned TO, so the fallback would end the wrong session. A session that
// leaves without an explicit exit still ages out — last_seen_at stops
// advancing, and the lease close ends the row for good.
func handleWorktreeExit(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	exited := pathFromToolInput(p.ToolInput)
	if exited == "" {
		return
	}
	root, ok := worktree.Root(exited)
	if !ok {
		return
	}
	taskID, ok := l.TaskID(root)
	if !ok {
		return
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID, _ = markerSessionID(root)
	}
	// root is the worktree being LEFT, so its turns are the ones that bill
	// against this lease.
	endSession(ctx, opts, taskID, sessionID, p.TranscriptPath, root)
	if err := removeSessionMarker(root); err != nil {
		warn(opts, "remove session marker: %v", err)
	}
}

// payloadPath returns the created/removed worktree path from the payload's
// tool_input, falling back to the resolved cwd (dir). The exact tool_input
// field name is not contractually fixed, so pathFromToolInput searches
// defensively; ParseDir then decides whether the result is under the
// worktree base dir.
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
// injecting as session context. skillPaths is name -> local dir for skills
// ensureSkills managed to fetch; a skill missing from it either had no hash
// (pinned) or failed to fetch (falls back to an install hint).
func compactBrief(b model.Brief, skillPaths map[string]string) string {
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
	if len(b.Skills.Pinned)+len(b.Skills.Matches) > 0 {
		fmt.Fprintf(&sb, "\n## Skills\n")
		var inlined int     // running total of bytes inlined so far, across ALL pins
		var overBudget bool // sticky: once one pin doesn't fit, none after it are considered either
		for _, p := range b.Skills.Pinned {
			fmt.Fprintf(&sb, "\n### Pinned: %s\n", p.Name)
			loc := "lode skills install " + p.Name
			if path := skillPaths[p.Name]; path != "" {
				loc = filepath.Join(path, "SKILL.md")
			}
			if !overBudget && inlined+len(p.Content) <= maxInlinedSkillBytes {
				inlined += len(p.Content)
				fmt.Fprintf(&sb, "%s\n", p.Content)
				if path := skillPaths[p.Name]; path != "" {
					fmt.Fprintf(&sb, "(supporting files: %s)\n", path)
				}
				continue
			}
			// Never truncate mid-document: half a SKILL.md is worse than
			// none, since the model would act on incomplete instructions.
			overBudget = true
			fmt.Fprintf(&sb, "(content omitted — %s; read it at %s)\n", humanKB(len(p.Content)), loc)
		}
		if len(b.Skills.Matches) > 0 {
			fmt.Fprintf(&sb, "\n### Possibly relevant org skills\nRead the SKILL.md if relevant to this task:\n")
			for _, m := range b.Skills.Matches {
				loc := "lode skills install " + m.Name
				if path := skillPaths[m.Name]; path != "" {
					loc = filepath.Join(path, "SKILL.md")
				}
				fmt.Fprintf(&sb, "- %s (%.2f): %s — %s\n", m.Name, m.Score, m.Description, loc)
			}
		}
	}
	return sb.String()
}

// maxInlinedSkillBytes caps the total bytes of pinned SKILL.md content
// compactBrief will inline, summed across every pin — not a per-pin cap and
// not a pin-count cap, since one large pin costs far more context than
// several small ones.
const maxInlinedSkillBytes = 32 << 10

// humanKB renders a byte count in kilobytes to one decimal place.
func humanKB(n int) string {
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// --- session marker ---------------------------------------------------------

// sessionMarker records the process owning a live coding session in a
// worktree. A marker is stale once its pid is no longer alive.
type sessionMarker struct {
	SessionID       string `json:"session_id"`
	PID             int    `json:"pid"`
	StartedAt       string `json:"started_at"`
	LastHeartbeatAt string `json:"last_heartbeat_at,omitempty"`
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

// readSessionMarker reads root's marker. A missing or unparseable marker
// returns ok=false — never an error, since every caller treats "no marker" as
// "nothing to do".
func readSessionMarker(root string) (sessionMarker, bool) {
	path, err := markerPath(root)
	if err != nil {
		return sessionMarker{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionMarker{}, false
	}
	var m sessionMarker
	if json.Unmarshal(data, &m) != nil {
		return sessionMarker{}, false
	}
	return m, true
}

// writeMarker serializes m to root's marker path.
func writeMarker(root string, m sessionMarker) error {
	path, err := markerPath(root)
	if err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// writeSessionMarker writes the current process's session marker for root.
// LastHeartbeatAt is left empty rather than stamped with now: heartbeatDue
// already treats an unparseable/absent stamp as due, and recordHeartbeat
// (called only after a heartbeat actually reaches the backbone) is the sole
// writer of that field. Stamping it here would claim a heartbeat that never
// happened and could suppress a real one for up to heartbeatDebounce.
func writeSessionMarker(root, sessionID string, now time.Time) error {
	return writeMarker(root, sessionMarker{
		SessionID: sessionID,
		PID:       os.Getpid(),
		StartedAt: now.Format(time.RFC3339),
	})
}

// markerSessionID returns the session id recorded for root. Used by hooks that
// receive no stdin (git pre-commit) and so cannot learn it from a payload.
func markerSessionID(root string) (string, bool) {
	m, ok := readSessionMarker(root)
	if !ok || m.SessionID == "" {
		return "", false
	}
	return m.SessionID, true
}

// heartbeatDue reports whether root's session is due another heartbeat. No
// marker means no session to report, so nothing is due. An unparseable
// timestamp counts as due — reporting once too often beats going silent.
func heartbeatDue(root string, now time.Time) bool {
	m, ok := readSessionMarker(root)
	if !ok || m.SessionID == "" {
		return false
	}
	last, err := time.Parse(time.RFC3339, m.LastHeartbeatAt)
	if err != nil {
		return true
	}
	return now.Sub(last) >= heartbeatDebounce
}

// recordHeartbeat stamps root's marker with the time of a heartbeat that was
// just reported to the backbone.
func recordHeartbeat(root string, now time.Time) error {
	m, ok := readSessionMarker(root)
	if !ok {
		return nil
	}
	m.LastHeartbeatAt = now.Format(time.RFC3339)
	return writeMarker(root, m)
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
	m, ok := readSessionMarker(root)
	if !ok || m.PID <= 0 {
		return false
	}
	return pidAlive(m.PID)
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
