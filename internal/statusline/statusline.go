// Package statusline renders one line for a coding agent's status-line slot
// from the JSON payload the agent writes to the command's stdin.
//
// The payload contract is Claude Code's, which Cursor CLI adopted verbatim, so
// a single command serves both harnesses and neither needs to be named. The
// harnesses that render a fixed set of built-in items instead (Codex CLI's
// `tui.status_line`, Gemini CLI's `/footer`) accept no command at all, so
// there is nothing to dispatch on and no harness flag to carry.
//
// Two properties are load-bearing, and both come from the harness re-running
// this on every assistant message: it makes no network call, and every segment
// degrades to empty rather than failing. A field the harness does not send
// costs one segment; it never costs the line.
package statusline

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// autoCompactBufferPct is the share of the context window Claude Code reserves
// for the auto-compact buffer. Usable context is the rest, and the bar reports
// occupancy of that usable range, so it reads 100% when compaction triggers
// rather than at a number the user never reaches.
const autoCompactBufferPct = 16.5

const (
	branchSymbol   = "⎇" // U+2387 alternate key, marks the git branch
	worktreeSymbol = "⧉" // U+29C9 two joined squares, marks a linked worktree
)

// Payload is the status-line JSON the harness writes to stdin. Both harnesses
// send more than this; every field here is optional, because the subset they
// agree on is smaller than either one's full payload.
type Payload struct {
	Model         *ModelInfo     `json:"model"`
	Workspace     *WorkspaceInfo `json:"workspace"`
	SessionID     string         `json:"session_id"`
	ContextWindow *ContextWindow `json:"context_window"`
	RateLimits    *RateLimits    `json:"rate_limits"`
}

// ModelInfo names the model driving the session.
type ModelInfo struct {
	DisplayName string `json:"display_name"`
}

// WorkspaceInfo locates the session. CurrentDir is preferred over the process
// working directory because the harness may run the command from elsewhere.
type WorkspaceInfo struct {
	CurrentDir string `json:"current_dir"`
}

// ContextWindow carries the harness's own accounting of window occupancy.
type ContextWindow struct {
	RemainingPercentage *float64 `json:"remaining_percentage"`
}

// RateLimits carries the Claude.ai subscription usage the harness reports on
// stdin (Claude Code ≥2.1.x) — present only for subscribers, and only once
// the API has reported at least one window. Both windows are read straight
// off the payload: unlike ContextWindow, nothing here needs a network call.
type RateLimits struct {
	FiveHour *RateLimitWindow `json:"five_hour"`
	SevenDay *RateLimitWindow `json:"seven_day"`
}

// RateLimitWindow is one usage window (5-hour session or 7-day week).
type RateLimitWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
}

// Options is the ambient state rendering reads. Every field has a working
// zero value; they exist so tests need neither a home directory nor a real
// temp dir.
type Options struct {
	// ConfigDir holds the harness's todo store. Empty means
	// $CLAUDE_CONFIG_DIR, falling back to ~/.claude.
	ConfigDir string
	// TempDir is where the context bridge file is written. Empty means
	// os.TempDir().
	TempDir string
	// Dir resolves git facts when the payload names no directory. Empty
	// means the process working directory.
	Dir string
}

// Run reads a payload from r and writes one status line to w. It returns an
// error for the caller's benefit only: the caller is expected to swallow it,
// because a harness renders whatever this prints and a failure must cost a
// blank line rather than an error in someone's prompt.
//
// A payload that has not arrived within timeout returns without writing.
// Blocking is the one failure mode the harness cannot absorb — it stalls the
// render — so waiting is bounded even though reading stdin normally is not.
func Run(r io.Reader, w io.Writer, timeout time.Duration, opts Options) error {
	raw, err := readWithin(r, timeout)
	if err != nil {
		return err
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("parse status line payload: %w", err)
	}
	_, err = io.WriteString(w, Render(&p, opts))
	return err
}

// Render builds the status line: model, the in-progress todo if the harness
// keeps one, the git location, and a context-usage meter.
func Render(p *Payload, opts Options) string {
	model := renderModel(p)
	location := renderLocation(p, opts.Dir)
	cfgDir := configDir(opts.ConfigDir)
	task := renderTask(cfgDir, p.SessionID)
	usage := renderUsage(p, opts.TempDir, cfgDir)

	if task != "" {
		return fmt.Sprintf("\x1b[2m%s\x1b[0m │ \x1b[1m%s\x1b[0m │ \x1b[2m%s\x1b[0m%s", model, task, location, usage)
	}
	return fmt.Sprintf("\x1b[2m%s\x1b[0m │ \x1b[2m%s\x1b[0m%s", model, location, usage)
}

// errTimeout reports that no payload arrived in time.
var errTimeout = fmt.Errorf("timed out reading the status line payload")

// readWithin reads all of r, giving up after timeout. The read is left running
// in its own goroutine rather than cancelled: the process is about to exit
// either way, and there is no cancellable read on an inherited stdin.
func readWithin(r io.Reader, timeout time.Duration) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(r)
		ch <- result{b, err}
	}()

	select {
	case res := <-ch:
		return res.data, res.err
	case <-time.After(timeout):
		return nil, errTimeout
	}
}

// configDir resolves the harness config directory holding the todo store.
func configDir(override string) string {
	if override != "" {
		return override
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// renderModel returns the model's display name, or a neutral placeholder when
// the harness sends none.
func renderModel(p *Payload) string {
	if p.Model != nil && p.Model.DisplayName != "" {
		return p.Model.DisplayName
	}
	return "agent"
}

// renderLocation returns the git location segment: the project name, followed
// by either the task the workspace is bound to or its branch and worktree
// state. Project is the basename of the remote URL (github.com/foo/bar.git ->
// bar). Falls back to the directory basename outside a git repo.
func renderLocation(p *Payload, fallbackDir string) string {
	dir := fallbackDir
	if p.Workspace != nil && p.Workspace.CurrentDir != "" {
		dir = p.Workspace.CurrentDir
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}

	// One rev-parse for toplevel, common .git dir, and branch.
	info, err := gitexec.Text(dir, "rev-parse", "--path-format=absolute",
		"--show-toplevel", "--git-common-dir", "--abbrev-ref", "HEAD")
	lines := strings.Split(info, "\n")
	if err != nil || len(lines) < 3 {
		return filepath.Base(dir)
	}
	toplevel := lines[0]
	mainWorktree := filepath.Dir(lines[1]) // parent of the common .git dir
	branch := lines[2]

	project := gitProject(dir, mainWorktree)
	if taskID, ok := worktree.StampedTaskID(dir); ok {
		return formatTaskLocation(project, taskID, branch)
	}
	return formatLocation(project, toplevel != mainWorktree, branch)
}

// formatTaskLocation renders a workspace bound to a task: project, task id,
// and slug as three words. The branch is rendered from the id and slug
// (`WL-7-fix-the-thing`), so splitting the id back off recovers the slug, and
// a space where the joining dash was reads as two facts rather than one long
// token.
//
// A branch that does not carry the id — renamed by hand, or produced by a
// LODE_BRANCH_TEMPLATE that orders the parts differently — yields no slug, and
// the id stands alone rather than having a guess appended to it.
func formatTaskLocation(project, taskID, branch string) string {
	out := project + " " + taskID
	if slug := strings.TrimPrefix(branch, taskID+"-"); slug != branch && slug != "" {
		out += " " + slug
	}
	return out
}

// gitProject returns the project name: the basename of the remote URL with any
// ".git" suffix removed, falling back to the main worktree directory name.
func gitProject(dir, mainWorktree string) string {
	if url, ok := gitexec.Line(dir, "config", "--get", "remote.origin.url"); ok && url != "" {
		base := url
		if i := strings.LastIndexAny(base, "/:"); i >= 0 {
			base = base[i+1:]
		}
		if base = strings.TrimSuffix(base, ".git"); base != "" {
			return base
		}
	}
	return filepath.Base(mainWorktree)
}

// formatLocation joins project, worktree indicator, and branch, for a
// workspace that carries no task binding. Both symbols sit together
// immediately before the branch name when in a worktree.
//
// A workspace bound to a task shows the task id here instead: the id is the
// name of the work, and once it is known the branch and worktree symbols only
// spell out what the id already implies — a task branch is rendered *from* the
// id (`WL-7-fix-the-thing`), so showing both is the same fact twice at the
// width of a terminal prompt.
func formatLocation(project string, isWorktree bool, branch string) string {
	out := project
	hasBranch := branch != "" && branch != "HEAD"

	switch {
	case isWorktree && hasBranch:
		out += " " + branchSymbol + worktreeSymbol + " " + branch
	case isWorktree:
		out += " " + worktreeSymbol
	case hasBranch:
		out += " " + branchSymbol + " " + branch
	}
	return out
}

// todoItem is one entry in the harness's per-session todo file.
type todoItem struct {
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
}

// renderTask returns the active form of the current in-progress todo. The todo
// store is Claude Code's; on a harness that keeps none the directory is simply
// absent and the segment drops out.
func renderTask(dir, session string) string {
	if dir == "" || session == "" {
		return ""
	}

	todosDir := filepath.Join(dir, "todos")
	entries, err := os.ReadDir(todosDir)
	if err != nil {
		return ""
	}

	type fileEntry struct {
		name  string
		mtime time.Time
	}
	var matches []fileEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, session) && strings.Contains(name, "-agent-") && strings.HasSuffix(name, ".json") {
			if info, err := e.Info(); err == nil {
				matches = append(matches, fileEntry{name, info.ModTime()})
			}
		}
	}
	if len(matches) == 0 {
		return ""
	}
	// Newest wins: a session fans out to several agent files and only the
	// most recently written one describes what is running now.
	sort.Slice(matches, func(i, j int) bool { return matches[i].mtime.After(matches[j].mtime) })

	raw, err := os.ReadFile(filepath.Join(todosDir, matches[0].name))
	if err != nil {
		return ""
	}
	var todos []todoItem
	if json.Unmarshal(raw, &todos) != nil {
		return ""
	}
	for _, t := range todos {
		if t.Status == "in_progress" {
			return t.ActiveForm
		}
	}
	return ""
}

// renderUsage returns the usage segment: context, session, and weekly
// percentages as coloured numbers (C:12% S:56% W:24%) rather than a bar, so
// all three fit in the width the bar alone used to need. Each field drops
// out independently — a payload can carry context_window without
// rate_limits (a harness below Claude Code 2.1.x, or a non-subscription
// account), or the reverse.
func renderUsage(p *Payload, tempDir, configDir string) string {
	dark := themeIsDark(configDir)
	var fields []string
	if used, ok := contextUsedPercent(p, tempDir); ok {
		fields = append(fields, usageField("C", used, dark))
	}
	if p.RateLimits != nil {
		if w := p.RateLimits.FiveHour; w != nil {
			fields = append(fields, usageField("S", int(math.Round(w.UsedPercentage)), dark))
		}
		if w := p.RateLimits.SevenDay; w != nil {
			fields = append(fields, usageField("W", int(math.Round(w.UsedPercentage)), dark))
		}
	}
	if len(fields) == 0 {
		return ""
	}
	return " " + strings.Join(fields, " ")
}

// harnessSettings is the subset of the harness's settings.json this package
// reads.
type harnessSettings struct {
	Theme string `json:"theme"`
}

// themeIsDark reports whether the harness's configured theme is a dark
// variant (dark, dark-ansi, dark-daltonized — Claude Code's own enum).
// Defaults to false, the light-suited colours, when settings.json is
// missing, unreadable, or names no theme: that was every render's colour
// before theme awareness existed, so an absent signal keeps it rather than
// guessing the more common dark terminal.
func themeIsDark(configDir string) bool {
	if configDir == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		return false
	}
	var s harnessSettings
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	return strings.HasPrefix(s.Theme, "dark")
}

// contextUsedPercent normalizes the harness's remaining-context percentage
// against the auto-compact buffer, so the number reads 100% right when
// compaction triggers rather than at whatever the raw window leaves. It also
// writes the same numbers to the bridge file, for the claude-context-monitor
// hook, which has no other way to see them.
func contextUsedPercent(p *Payload, tempDir string) (int, bool) {
	if p.ContextWindow == nil || p.ContextWindow.RemainingPercentage == nil {
		return 0, false
	}
	remaining := *p.ContextWindow.RemainingPercentage

	usableRemaining := math.Max(0, ((remaining-autoCompactBufferPct)/(100-autoCompactBufferPct))*100)
	used := int(math.Max(0, math.Min(100, math.Round(100-usableRemaining))))

	writeBridgeFile(tempDir, p.SessionID, remaining, used)
	return used, true
}

// usageField renders one labelled percentage, coloured by how close it is to
// its limit.
func usageField(label string, pct int, dark bool) string {
	return fmt.Sprintf("%s%s:%d%%\x1b[0m", usageColor(pct, dark), label, pct)
}

// usageColor mirrors the four-step severity ramp the bar used to encode in
// block count: green, yellow, orange, red.
func usageColor(pct int, dark bool) string {
	colors := usageColors(dark)
	switch {
	case pct < 50:
		return colors[0]
	case pct < 65:
		return colors[1]
	case pct < 80:
		return colors[2]
	default:
		return colors[3]
	}
}

// usageColors picks the severity ramp for the terminal's theme. The base
// (30-range) ANSI colours render as darker shades that read well on a light
// background; their bright (90-range) counterparts render lighter and read
// well on dark. Fixed RGB (the orange) has no such pair, so both ramps use
// the same mid-bright shade.
func usageColors(dark bool) [4]string {
	if dark {
		return [4]string{"\x1b[92m", "\x1b[93m", "\x1b[38;5;214m", "\x1b[91m"}
	}
	return [4]string{"\x1b[32m", "\x1b[33m", "\x1b[38;5;208m", "\x1b[31m"}
}

// bridgeData is the context snapshot written for out-of-process consumers.
type bridgeData struct {
	SessionID    string  `json:"session_id"`
	RemainingPct float64 `json:"remaining_percentage"`
	UsedPct      int     `json:"used_pct"`
	Timestamp    int64   `json:"timestamp"`
}

// writeBridgeFile publishes the context numbers for the claude-context-monitor
// PostToolUse hook, which has no other way to see them: the harness sends the
// context window to the status line and nowhere else. Best-effort by design —
// a failure here must not cost the rendered line.
func writeBridgeFile(tempDir, session string, remaining float64, used int) {
	if session == "" {
		return
	}
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	b, err := json.Marshal(bridgeData{
		SessionID:    session,
		RemainingPct: remaining,
		UsedPct:      used,
		Timestamp:    time.Now().Unix(),
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(tempDir, fmt.Sprintf("claude-ctx-%s.json", session)), b, 0o644)
}
