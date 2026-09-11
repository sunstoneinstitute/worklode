package hookrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hyperlinkTerminal makes cli.TerminalHyperlinks say yes for one test. TestMain
// pins TMUX for the package, so every other test runs with the hint off and
// asserts against output that does not depend on the developer's terminal.
func hyperlinkTerminal(t *testing.T) {
	t.Helper()
	t.Setenv("TMUX", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "ghostty")
}

// TestSessionStartRefLinkHint: on a terminal known to render OSC 8, the brief
// carries the instruction to print ids as links to the configured server.
func TestSessionStartRefLinkHint(t *testing.T) {
	_, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Linkable")
	hyperlinkTerminal(t)

	stdout, _ := runSessionStart(t, wtDir, "s-link")
	ctx := additionalContext(t, stdout)

	server := os.Getenv("LODE_SERVER")
	if server == "" {
		t.Fatal("precondition: test server did not set LODE_SERVER")
	}
	if !strings.Contains(ctx, server+"/<id>") {
		t.Fatalf("additionalContext missing the %s/<id> instruction: %q", server, ctx)
	}
	if !strings.Contains(ctx, `\x1b]8;;`) {
		t.Fatalf("additionalContext missing the OSC 8 escape form: %q", ctx)
	}
	if !strings.Contains(ctx, taskID) {
		t.Fatalf("hint displaced the brief; no task id in %q", ctx)
	}
}

// TestSessionStartRefLinkHintUnknownTerminal: an unknown terminal gets no
// hint, because a terminal that does not render the escape prints it raw.
func TestSessionStartRefLinkHintUnknownTerminal(t *testing.T) {
	_, c, _ := newRealServer(t)
	root := initGitRepo(t)
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "Unlinkable")
	t.Setenv("TMUX", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "dumb-term")

	ctx := additionalContext(t, mustStdout(runSessionStart(t, wtDir, "s-nolink")))
	if strings.Contains(ctx, "OSC 8") {
		t.Fatalf("unknown terminal got the hint: %q", ctx)
	}
}

// TestSessionStartRefLinksDisabled: ref_links = false in the repo config turns
// the hint off on a terminal that would otherwise get it.
func TestSessionStartRefLinksDisabled(t *testing.T) {
	_, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Opted out")
	hyperlinkTerminal(t)

	if err := os.MkdirAll(filepath.Join(root, ".worklode"), 0o755); err != nil {
		t.Fatalf("create .worklode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".worklode", "config.toml"),
		[]byte("ref_links = false\n"), 0o644); err != nil {
		t.Fatalf("write repo config: %v", err)
	}

	ctx := additionalContext(t, mustStdout(runSessionStart(t, wtDir, "s-optout")))
	if strings.Contains(ctx, "OSC 8") {
		t.Fatalf("ref_links = false still emitted the hint: %q", ctx)
	}
	if !strings.Contains(ctx, taskID) {
		t.Fatalf("brief missing from additionalContext: %q", ctx)
	}
}

// mustStdout drops the stderr half of a runSessionStart result.
func mustStdout(stdout, _ string) string { return stdout }
