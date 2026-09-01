package statusline

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stripANSI removes escape sequences so assertions read against the text the
// user sees rather than the colouring around it.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' {
			b.WriteByte(s[i])
			continue
		}
		for i < len(s) && s[i] != 'm' {
			i++
		}
	}
	return b.String()
}

func pct(v float64) *float64 { return &v }

func TestRenderModelFallsBackWhenHarnessSendsNone(t *testing.T) {
	dir := t.TempDir()
	got := stripANSI(Render(&Payload{}, Options{ConfigDir: dir, TempDir: dir, Dir: dir}))
	if !strings.HasPrefix(got, "agent │ ") {
		t.Fatalf("want a neutral model placeholder, got %q", got)
	}
}

func TestRenderUsesModelDisplayName(t *testing.T) {
	dir := t.TempDir()
	p := &Payload{Model: &ModelInfo{DisplayName: "Opus 5"}}
	got := stripANSI(Render(p, Options{ConfigDir: dir, TempDir: dir, Dir: dir}))
	if !strings.HasPrefix(got, "Opus 5 │ ") {
		t.Fatalf("want the display name first, got %q", got)
	}
}

// A payload carrying only the fields both harnesses agree on must still render
// — that subset is the whole reason one command serves Claude Code and Cursor.
func TestRenderWithMinimalCrossHarnessPayload(t *testing.T) {
	dir := t.TempDir()
	p := &Payload{
		Model:         &ModelInfo{DisplayName: "Composer"},
		Workspace:     &WorkspaceInfo{CurrentDir: dir},
		SessionID:     "abc123",
		ContextWindow: &ContextWindow{RemainingPercentage: pct(58.25)},
	}
	got := stripANSI(Render(p, Options{ConfigDir: t.TempDir(), TempDir: t.TempDir(), Dir: dir}))
	if !strings.Contains(got, "Composer") || !strings.Contains(got, "50%") {
		t.Fatalf("want model and context usage, got %q", got)
	}
}

func TestRenderOmitsContextWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	got := stripANSI(Render(&Payload{Model: &ModelInfo{DisplayName: "m"}}, Options{ConfigDir: dir, TempDir: dir, Dir: dir}))
	if strings.Contains(got, "%") {
		t.Fatalf("want no usage segment, got %q", got)
	}
}

// The meter reports occupancy of the usable window, not of the raw one: a
// session with exactly the auto-compact buffer left is full, not 16.5% free.
func TestRenderContextNormalizesAgainstAutoCompactBuffer(t *testing.T) {
	tests := []struct {
		name      string
		remaining float64
		want      string
	}{
		{"untouched window", 100, "0%"},
		{"buffer only", autoCompactBufferPct, "100%"},
		{"past the buffer", 5, "100%"},
		{"halfway", 58.25, "50%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Payload{ContextWindow: &ContextWindow{RemainingPercentage: pct(tt.remaining)}}
			got := stripANSI(renderUsage(p, t.TempDir(), ""))
			if !strings.Contains(got, tt.want) {
				t.Fatalf("remaining %.2f: want %s, got %q", tt.remaining, tt.want, got)
			}
		})
	}
}

func TestRenderContextWritesBridgeFile(t *testing.T) {
	tmp := t.TempDir()
	p := &Payload{SessionID: "sess-1", ContextWindow: &ContextWindow{RemainingPercentage: pct(58.25)}}
	renderUsage(p, tmp, "")

	raw, err := os.ReadFile(filepath.Join(tmp, "claude-ctx-sess-1.json"))
	if err != nil {
		t.Fatalf("read bridge file: %v", err)
	}
	var got bridgeData
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse bridge file: %v", err)
	}
	if got.SessionID != "sess-1" || got.UsedPct != 50 || got.Timestamp == 0 {
		t.Fatalf("unexpected bridge data: %+v", got)
	}
}

func TestRenderContextWithoutSessionWritesNoBridgeFile(t *testing.T) {
	tmp := t.TempDir()
	p := &Payload{ContextWindow: &ContextWindow{RemainingPercentage: pct(50)}}
	renderUsage(p, tmp, "")

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("want no bridge file without a session id, got %v", entries)
	}
}

// Session and weekly usage come straight off the payload — no network call,
// no bridge file — so both drop out independently when the harness omits
// one of them (an older Claude Code, or a non-subscription account).
func TestRenderUsageAddsSessionAndWeeklyFromRateLimits(t *testing.T) {
	p := &Payload{RateLimits: &RateLimits{
		FiveHour: &RateLimitWindow{UsedPercentage: 56},
		SevenDay: &RateLimitWindow{UsedPercentage: 24},
	}}
	if got := stripANSI(renderUsage(p, t.TempDir(), "")); got != " S:56% W:24%" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderUsageOmitsRateLimitFieldsIndependently(t *testing.T) {
	p := &Payload{RateLimits: &RateLimits{FiveHour: &RateLimitWindow{UsedPercentage: 10}}}
	if got := stripANSI(renderUsage(p, t.TempDir(), "")); got != " S:10%" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderUsageCombinesContextAndRateLimits(t *testing.T) {
	p := &Payload{
		ContextWindow: &ContextWindow{RemainingPercentage: pct(58.25)},
		RateLimits: &RateLimits{
			FiveHour: &RateLimitWindow{UsedPercentage: 56},
			SevenDay: &RateLimitWindow{UsedPercentage: 24},
		},
	}
	if got := stripANSI(renderUsage(p, t.TempDir(), "")); got != " C:50% S:56% W:24%" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderUsageColorsBySeverity(t *testing.T) {
	tests := []struct {
		pct   float64
		color string
	}{
		{10, "\x1b[32m"},
		{55, "\x1b[33m"},
		{70, "\x1b[38;5;208m"},
		{90, "\x1b[31m"},
	}
	for _, tt := range tests {
		p := &Payload{RateLimits: &RateLimits{FiveHour: &RateLimitWindow{UsedPercentage: tt.pct}}}
		if got := renderUsage(p, t.TempDir(), ""); !strings.Contains(got, tt.color) {
			t.Fatalf("pct %.0f: want colour %q in %q", tt.pct, tt.color, got)
		}
	}
}

// writeSettings writes a minimal settings.json with the given theme.
func writeSettings(t *testing.T, dir, theme string) {
	t.Helper()
	raw, err := json.Marshal(harnessSettings{Theme: theme})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRenderUsageColorsSwapToBrightWhenThemeIsDark(t *testing.T) {
	cfgDir := t.TempDir()
	writeSettings(t, cfgDir, "dark")

	p := &Payload{RateLimits: &RateLimits{FiveHour: &RateLimitWindow{UsedPercentage: 10}}}
	if got := renderUsage(p, t.TempDir(), cfgDir); !strings.Contains(got, "\x1b[92m") {
		t.Fatalf("want the bright-green dark-theme colour, got %q", got)
	}
}

func TestRenderUsageColorsStayBaseWhenThemeIsLight(t *testing.T) {
	cfgDir := t.TempDir()
	writeSettings(t, cfgDir, "light")

	p := &Payload{RateLimits: &RateLimits{FiveHour: &RateLimitWindow{UsedPercentage: 10}}}
	if got := renderUsage(p, t.TempDir(), cfgDir); !strings.Contains(got, "\x1b[32m") {
		t.Fatalf("want the base-green light-theme colour, got %q", got)
	}
}

func TestThemeIsDarkRecognizesEveryDarkVariant(t *testing.T) {
	for _, theme := range []string{"dark", "dark-ansi", "dark-daltonized"} {
		t.Run(theme, func(t *testing.T) {
			cfgDir := t.TempDir()
			writeSettings(t, cfgDir, theme)
			if !themeIsDark(cfgDir) {
				t.Fatalf("theme %q: want dark", theme)
			}
		})
	}
}

func TestThemeIsDarkDefaultsFalseWithoutSettings(t *testing.T) {
	tests := []struct {
		name      string
		configDir string
	}{
		{"no config dir", ""},
		{"missing settings.json", t.TempDir()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if themeIsDark(tt.configDir) {
				t.Fatalf("want false without a readable theme")
			}
		})
	}
}

func TestThemeIsDarkFalseOnMalformedSettings(t *testing.T) {
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if themeIsDark(cfgDir) {
		t.Fatalf("want false on malformed settings.json")
	}
}

// writeTodos creates one session todo file with the name shape the harness
// uses, backdated so mtime ordering is deterministic.
func writeTodos(t *testing.T, dir, name string, age time.Duration, todos []todoItem) {
	t.Helper()
	todosDir := filepath.Join(dir, "todos")
	if err := os.MkdirAll(todosDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(todos)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(todosDir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestRenderTaskReturnsInProgressTodo(t *testing.T) {
	dir := t.TempDir()
	writeTodos(t, dir, "sess-1-agent-sess-1.json", 0, []todoItem{
		{Status: "completed", ActiveForm: "Reading the spec"},
		{Status: "in_progress", ActiveForm: "Porting the status line"},
		{Status: "pending", ActiveForm: "Writing tests"},
	})
	if got := renderTask(dir, "sess-1"); got != "Porting the status line" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderTaskPrefersTheNewestAgentFile(t *testing.T) {
	dir := t.TempDir()
	writeTodos(t, dir, "sess-1-agent-old.json", time.Hour, []todoItem{
		{Status: "in_progress", ActiveForm: "Stale work"},
	})
	writeTodos(t, dir, "sess-1-agent-new.json", 0, []todoItem{
		{Status: "in_progress", ActiveForm: "Current work"},
	})
	if got := renderTask(dir, "sess-1"); got != "Current work" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderTaskIgnoresOtherSessions(t *testing.T) {
	dir := t.TempDir()
	writeTodos(t, dir, "sess-2-agent-sess-2.json", 0, []todoItem{
		{Status: "in_progress", ActiveForm: "Someone else's work"},
	})
	if got := renderTask(dir, "sess-1"); got != "" {
		t.Fatalf("want no task, got %q", got)
	}
}

// A harness with no todo store must lose the segment, not the line.
func TestRenderTaskWithoutTodoStore(t *testing.T) {
	if got := renderTask(t.TempDir(), "sess-1"); got != "" {
		t.Fatalf("want no task, got %q", got)
	}
	if got := renderTask("", "sess-1"); got != "" {
		t.Fatalf("want no task without a config dir, got %q", got)
	}
}

func TestFormatLocation(t *testing.T) {
	tests := []struct {
		name       string
		project    string
		isWorktree bool
		branch     string
		want       string
	}{
		{"main checkout", "worklode", false, "main", "worklode ⎇ main"},
		{"linked worktree", "worklode", true, "WL-7-fix", "worklode ⎇⧉ WL-7-fix"},
		{"detached worktree", "worklode", true, "HEAD", "worklode ⧉"},
		{"detached main", "worklode", false, "HEAD", "worklode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatLocation(tt.project, tt.isWorktree, tt.branch); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// initRepo makes a git repo with one commit, the worktree config extension
// enabled, and a remote, so location rendering has everything to read.
func initRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "extensions.worktreeConfig", "true"},
		{"config", "remote.origin.url", "git@github.com:sunstoneinstitute/worklode.git"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	return root
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

// A workspace bound to a task shows the id and slug as separate words, with no
// branch or worktree symbols: the branch is rendered from those two parts, so
// the symbols would only repeat them.
func TestRenderLocationPrefersTheTaskID(t *testing.T) {
	root := initRepo(t)
	gitIn(t, root, "checkout", "-q", "-b", "WL-7-fix-the-thing")
	gitIn(t, root, "config", "--worktree", "worklode.task-id", "WL-7")

	got := renderLocation(&Payload{Workspace: &WorkspaceInfo{CurrentDir: root}}, "")
	if want := "worklode WL-7 fix-the-thing"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.ContainsAny(got, branchSymbol+worktreeSymbol) {
		t.Fatalf("got %q, want no branch or worktree symbols alongside the task id", got)
	}
}

// The id and slug are separate words, so the dash that joins them in the
// branch must not survive; the slug keeps its own internal dashes.
func TestFormatTaskLocation(t *testing.T) {
	tests := []struct {
		name   string
		taskID string
		branch string
		want   string
	}{
		{"id and slug", "WL-7", "WL-7-fix-the-thing", "worklode WL-7 fix-the-thing"},
		{"single-word slug", "WL-7", "WL-7-fix", "worklode WL-7 fix"},
		{"branch is the bare id", "WL-7", "WL-7", "worklode WL-7"},
		{"branch renamed away from the id", "WL-7", "spike", "worklode WL-7"},
		// The split anchors on the full "WL-7-" join, not a bare "WL-7", so a
		// neighbouring task's branch cannot be sliced into a bogus slug.
		{"another task's branch", "WL-7", "WL-70-other", "worklode WL-7"},
		{"no branch", "WL-7", "", "worklode WL-7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTaskLocation("worklode", tt.taskID, tt.branch); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The stamp is worktree-scoped: a linked worktree carrying one shows its own
// id and slug while the main checkout it was created from keeps its branch.
func TestRenderLocationTaskIDIsPerWorktree(t *testing.T) {
	root := initRepo(t)
	wt := filepath.Join(root, ".worktrees", "WL-7-fix-the-thing")
	gitIn(t, root, "worktree", "add", "-b", "WL-7-fix-the-thing", wt)
	gitIn(t, wt, "config", "--worktree", "worklode.task-id", "WL-7")

	if got := renderLocation(&Payload{Workspace: &WorkspaceInfo{CurrentDir: wt}}, ""); got != "worklode WL-7 fix-the-thing" {
		t.Fatalf("worktree location = %q, want %q", got, "worklode WL-7 fix-the-thing")
	}
	if got := renderLocation(&Payload{Workspace: &WorkspaceInfo{CurrentDir: root}}, ""); got != "worklode ⎇ main" {
		t.Fatalf("main checkout location = %q, want the branch rendering", got)
	}
}

// Without a stamp, rendering is exactly what it was before the task binding
// existed — the claude-context-monitor behaviour.
func TestRenderLocationWithoutTaskIDShowsBranchAndWorktree(t *testing.T) {
	root := initRepo(t)
	if got := renderLocation(&Payload{Workspace: &WorkspaceInfo{CurrentDir: root}}, ""); got != "worklode ⎇ main" {
		t.Fatalf("got %q, want %q", got, "worklode ⎇ main")
	}

	wt := filepath.Join(root, ".worktrees", "spike")
	gitIn(t, root, "worktree", "add", "-b", "spike", wt)
	if got := renderLocation(&Payload{Workspace: &WorkspaceInfo{CurrentDir: wt}}, ""); got != "worklode ⎇⧉ spike" {
		t.Fatalf("got %q, want %q", got, "worklode ⎇⧉ spike")
	}
}

// A repo without extensions.worktreeConfig cannot scope the stamp, so the read
// must fail into the branch rendering rather than report a repo-wide value as
// if it were this workspace's.
func TestRenderLocationWithoutTheWorktreeConfigExtension(t *testing.T) {
	root := initRepo(t)
	gitIn(t, root, "config", "--unset", "extensions.worktreeConfig")
	wt := filepath.Join(root, ".worktrees", "spike")
	gitIn(t, root, "worktree", "add", "-b", "spike", wt)

	if got := renderLocation(&Payload{Workspace: &WorkspaceInfo{CurrentDir: wt}}, ""); got != "worklode ⎇⧉ spike" {
		t.Fatalf("got %q, want the branch rendering", got)
	}
}

func TestRenderLocationOutsideGitUsesDirectoryName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "somewhere")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Payload{Workspace: &WorkspaceInfo{CurrentDir: dir}}
	if got := renderLocation(p, ""); got != "somewhere" {
		t.Fatalf("got %q", got)
	}
}

func TestRunWritesALine(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(`{"model":{"display_name":"Opus 5"},"workspace":{"current_dir":"` + dir + `"}}`)
	var out bytes.Buffer
	if err := Run(in, &out, time.Second, Options{ConfigDir: dir, TempDir: dir, Dir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stripANSI(out.String()), "Opus 5") {
		t.Fatalf("got %q", out.String())
	}
}

func TestRunRejectsMalformedPayload(t *testing.T) {
	var out bytes.Buffer
	if err := Run(strings.NewReader("not json"), &out, time.Second, Options{}); err == nil {
		t.Fatal("want an error on a malformed payload")
	}
	if out.Len() != 0 {
		t.Fatalf("want nothing written, got %q", out.String())
	}
}

// A harness that opens the pipe but never writes must not stall the render.
func TestRunTimesOutOnAStalledPayload(t *testing.T) {
	var out bytes.Buffer
	err := Run(blockingReader{}, &out, 10*time.Millisecond, Options{})
	if err != errTimeout {
		t.Fatalf("want errTimeout, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("want nothing written, got %q", out.String())
	}
}

// blockingReader never returns, standing in for a pipe the harness holds open.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }
