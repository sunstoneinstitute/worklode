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
	branchSymbol   = "⎇"  // U+2387 alternate key, marks the git branch
	worktreeSymbol = "⧉ " // U+29C9 two joined squares, marks a linked worktree
	resetSymbol    = "⟲ " // U+27F2 anticlockwise gapped circle arrow, marks a usage window's reset countdown
)

// ANSI escape codes available for status line colouring.
const (
	ansiReset = "\x1b[0m"
	ansiDim   = "\x1b[2m"
	ansiBold  = "\x1b[1m"

	ansiGreenDark  = "\x1b[92m"
	ansiYellowDark = "\x1b[93m"
	ansiOrangeDark = "\x1b[38;5;214m"
	ansiRedDark    = "\x1b[91m"

	ansiGreenLight  = "\x1b[32m"
	ansiYellowLight = "\x1b[33m"
	ansiOrangeLight = "\x1b[38;5;208m"
	ansiRedLight    = "\x1b[31m"

	ansiBlueDark  = "\x1b[94m"
	ansiBlueLight = "\x1b[34m"

	// The gold used when the context field's severity is bumped a level for
	// the base 200k window and lands on the yellow tier: punchier than the
	// plain ANSI yellow above it stands in for, so the bump reads as a step
	// up rather than the ramp's ordinary yellow.
	ansiGoldDark  = "\x1b[38;5;220m"
	ansiGoldLight = "\x1b[38;5;178m"
)

// Colours used for each piece of the status line. Package-global so a build
// can retint the line without touching render logic. Dim reads faint to the
// point of illegibility on a dark terminal, so only the light ramp dims
// model/location/reset text; dark renders them at normal weight instead.
var (
	taskColor = ansiBold

	modelColorDark  = ansiReset
	modelColorLight = ansiDim

	locationColorDark  = ansiReset
	locationColorLight = ansiDim

	taskIDColorDark  = ansiBlueDark
	taskIDColorLight = ansiBlueLight

	contextBumpColorDark  = ansiGoldDark
	contextBumpColorLight = ansiGoldLight

	resetColorDark  = ansiDim
	resetColorLight = ansiDim

	usageColorsDark  = [4]string{ansiGreenDark, ansiYellowDark, ansiOrangeDark, ansiRedDark}
	usageColorsLight = [4]string{ansiGreenLight, ansiYellowLight, ansiOrangeLight, ansiRedLight}
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
// TotalInputTokens and TotalOutputTokens are 0 (not absent) before the first
// API response, so they carry no presence signal the way the percentage's
// pointer does. ContextWindowSize is 200000 by default, or 1000000 for a
// model with extended context; a harness that predates the field sends 0.
type ContextWindow struct {
	RemainingPercentage *float64 `json:"remaining_percentage"`
	TotalInputTokens    int64    `json:"total_input_tokens"`
	TotalOutputTokens   int64    `json:"total_output_tokens"`
	ContextWindowSize   int64    `json:"context_window_size"`
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
	ResetsAt       int64   `json:"resets_at"`
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
	cfgDir := configDir(opts.ConfigDir)
	dark := themeIsDark(cfgDir)
	location := renderLocation(p, opts.Dir, dark)
	task := renderTask(cfgDir, p.SessionID)
	usage := renderUsage(p, opts.TempDir, cfgDir)

	modelColor, locationColor := modelColorLight, locationColorLight
	if dark {
		modelColor, locationColor = modelColorDark, locationColorDark
	}

	if task != "" {
		return fmt.Sprintf("%s%s%s │ %s%s%s │ %s%s%s%s", modelColor, model, ansiReset, taskColor, task, ansiReset, locationColor, location, ansiReset, usage)
	}
	return fmt.Sprintf("%s%s%s │ %s%s%s%s", modelColor, model, ansiReset, locationColor, location, ansiReset, usage)
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
func renderLocation(p *Payload, fallbackDir string, dark bool) string {
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
		return formatTaskLocation(project, taskID, branch, dark)
	}
	return formatLocation(project, toplevel != mainWorktree, branch)
}

// formatTaskLocation renders a workspace bound to a task: the task id in
// blue, then project, then slug as three words. The branch is rendered from
// the id and slug (`WL-7-fix-the-thing`), so splitting the id back off
// recovers the slug, and a space where the joining dash was reads as two
// facts rather than one long token.
//
// A branch that does not carry the id — renamed by hand, or produced by a
// LODE_BRANCH_TEMPLATE that orders the parts differently — yields no slug, and
// the id stands alone rather than having a guess appended to it.
//
// The id's colour is reset back to locationColor rather than ansiReset alone,
// because the caller wraps the whole location segment in locationColor and
// this text sits inside that span.
func formatTaskLocation(project, taskID, branch string, dark bool) string {
	taskIDColor, resumeColor := taskIDColorLight, locationColorLight
	if dark {
		taskIDColor, resumeColor = taskIDColorDark, locationColorDark
	}
	out := fmt.Sprintf("%s%s%s%s %s", taskIDColor, taskID, ansiReset, resumeColor, project)
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

// renderUsage returns the usage segment: context, session, and weekly usage
// as bracketed fields ([CtxWin 12% 101k] [Sess 56% ⟲2h3m] [Week 24%
// ⟲1d2h3m]) rather than a bar, so all three fit in the width the bar alone
// used to need. Each field drops out independently — a payload can carry
// context_window without rate_limits (a harness below Claude Code 2.1.x, or
// a non-subscription account), or the reverse. Only the rate-limit windows
// carry a reset countdown; context has no fixed reset schedule, and shows its
// raw token count instead.
func renderUsage(p *Payload, tempDir, configDir string) string {
	dark := themeIsDark(configDir)
	now := time.Now()
	var fields []string
	if used, tokens, windowSize, ok := contextUsedPercent(p, tempDir); ok {
		fields = append(fields, bracketField("CtxWin", used, contextColor(used, windowSize, dark), humanTokens(tokens)))
	}
	if p.RateLimits != nil {
		if w := p.RateLimits.FiveHour; w != nil {
			pct := int(math.Round(w.UsedPercentage))
			fields = append(fields, bracketField("Sess", pct, usageColor(pct, dark), resetSuffix(w.ResetsAt, now, dark)))
		}
		if w := p.RateLimits.SevenDay; w != nil {
			pct := int(math.Round(w.UsedPercentage))
			fields = append(fields, bracketField("Week", pct, usageColor(pct, dark), resetSuffix(w.ResetsAt, now, dark)))
		}
	}
	if len(fields) == 0 {
		return ""
	}
	return " " + strings.Join(fields, " ")
}

// bracketField renders one usage field as "[Label pct% extra]". "Label pct%"
// carries color; extra (a token count or a dimmed reset countdown) keeps
// whatever colour it was already given, or none. Brackets render in the
// terminal's default colour so they never compete with either.
func bracketField(label string, pct int, color, extra string) string {
	head := fmt.Sprintf("%s%s %d%%%s", color, label, pct, ansiReset)
	if extra == "" {
		return "[" + head + "]"
	}
	return "[" + head + " " + extra + "]"
}

// resetSuffix renders the dimmed "⟲2h3m" countdown, or "" once formatResetIn
// drops out.
func resetSuffix(resetsAt int64, now time.Time, dark bool) string {
	in := formatResetIn(resetsAt, now)
	if in == "" {
		return ""
	}
	color := resetColorLight
	if dark {
		color = resetColorDark
	}
	return fmt.Sprintf("%s%s%s%s", color, resetSymbol, in, ansiReset)
}

// formatResetIn renders the time remaining until resetsAt as its two most
// significant components (2d20h, 2h3m, 45m) — a third unit is precision the
// countdown doesn't need. Returns "" once the window has already rolled over
// (resetsAt in the past, or zero — the harness sends no window at all rather
// than resetsAt: 0), so a stale percentage is never paired with a countdown
// that has already expired.
func formatResetIn(resetsAt int64, now time.Time) string {
	remaining := time.Unix(resetsAt, 0).Sub(now)
	if remaining <= 0 {
		return ""
	}
	total := int(remaining.Truncate(time.Minute).Minutes())
	days, hours, minutes := total/(24*60), (total/60)%24, total%60

	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
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
// hook, which has no other way to see them. The returned token count is the
// raw total_input_tokens + total_output_tokens, on the window's own scale
// rather than the normalized percentage; windowSize is the raw
// context_window_size, 0 when the harness sends none.
func contextUsedPercent(p *Payload, tempDir string) (used int, tokens, windowSize int64, ok bool) {
	if p.ContextWindow == nil || p.ContextWindow.RemainingPercentage == nil {
		return 0, 0, 0, false
	}
	remaining := *p.ContextWindow.RemainingPercentage

	usableRemaining := math.Max(0, ((remaining-autoCompactBufferPct)/(100-autoCompactBufferPct))*100)
	used = int(math.Max(0, math.Min(100, math.Round(100-usableRemaining))))
	tokens = p.ContextWindow.TotalInputTokens + p.ContextWindow.TotalOutputTokens
	windowSize = p.ContextWindow.ContextWindowSize

	writeBridgeFile(tempDir, p.SessionID, remaining, used)
	return used, tokens, windowSize, true
}

// humanTokens abbreviates a token count for the status line: 101k, 1.2M.
// Below 1000 there is no "k" to round to, so it prints the exact count.
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", int(math.Round(float64(n)/1_000)))
	default:
		return fmt.Sprintf("%d", n)
	}
}

// baseContextWindowTokens is Claude's default context window size; a harness
// reporting this (rather than the 1M extended window) leaves much less
// working room than the raw percentage alone suggests.
const baseContextWindowTokens = 200_000

// severityIndex maps a percentage to its position in the four-step severity
// ramp: 0 green, 1 yellow, 2 orange, 3 red.
func severityIndex(pct int) int {
	switch {
	case pct < 50:
		return 0
	case pct < 65:
		return 1
	case pct < 80:
		return 2
	default:
		return 3
	}
}

// usageColor mirrors the four-step severity ramp the bar used to encode in
// block count: green, yellow, orange, red.
func usageColor(pct int, dark bool) string {
	return usageColors(dark)[severityIndex(pct)]
}

// contextColor picks the CtxWin field's colour: the usual severity ramp,
// bumped one level brighter when windowSize is the base 200k rather than the
// 1M extended window — that smaller window leaves less room to work with at
// the same percentage, so the warning should arrive a level early. A bump
// landing on yellow uses a punchier gold instead, so it reads as a step up
// rather than the ramp's ordinary yellow.
func contextColor(pct int, windowSize int64, dark bool) string {
	idx := severityIndex(pct)
	bumped := windowSize > 0 && windowSize <= baseContextWindowTokens && idx < 3
	if bumped {
		idx++
	}
	if bumped && idx == 1 {
		if dark {
			return contextBumpColorDark
		}
		return contextBumpColorLight
	}
	return usageColors(dark)[idx]
}

// usageColors picks the severity ramp for the terminal's theme. The base
// (30-range) ANSI colours render as darker shades that read well on a light
// background; their bright (90-range) counterparts render lighter and read
// well on dark. Fixed RGB (the orange) has no such pair, so both ramps use
// the same mid-bright shade.
func usageColors(dark bool) [4]string {
	if dark {
		return usageColorsDark
	}
	return usageColorsLight
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
