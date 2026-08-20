package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/harness"
)

func TestEnsureAgentsMDCreatesAndConverges(t *testing.T) {
	root := t.TempDir()

	action, err := ensureAgentsMD(root)
	if err != nil || action != instrCreated {
		t.Fatalf("first run: %s %v", action, err)
	}
	first := readFile(t, filepath.Join(root, agentsFile))
	if !strings.Contains(first, "lode next") {
		t.Fatalf("block does not name the entry command: %s", first)
	}
	if !strings.Contains(first, agentsBlockBegin) || !strings.Contains(first, agentsBlockEnd) {
		t.Fatalf("block is not marker-delimited: %s", first)
	}

	action, err = ensureAgentsMD(root)
	if err != nil || action != instrUnchanged {
		t.Fatalf("second run: %s %v", action, err)
	}
	if got := readFile(t, filepath.Join(root, agentsFile)); got != first {
		t.Fatalf("second run rewrote the file:\n%q\nwant\n%q", got, first)
	}
}

func TestEnsureAgentsMDPreservesForeignContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, agentsFile)
	const prose = "# Ours\n\nHand-written.\n"
	if err := os.WriteFile(path, []byte(prose), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}

	if action, err := ensureAgentsMD(root); err != nil || action != instrUpdated {
		t.Fatalf("first run: %s %v", action, err)
	}
	got := readFile(t, path)
	if !strings.HasPrefix(got, prose) {
		t.Fatalf("foreign content lost: %q", got)
	}

	// A stale block is replaced in place, not duplicated.
	stale := strings.Replace(got, "lode next", "lode obsolete", 1)
	if stale == got {
		t.Fatal("could not corrupt the block body")
	}
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatalf("write stale block: %v", err)
	}
	if action, err := ensureAgentsMD(root); err != nil || action != instrUpdated {
		t.Fatalf("refresh run: %s %v", action, err)
	}
	refreshed := readFile(t, path)
	if n := strings.Count(refreshed, agentsBlockBegin); n != 1 {
		t.Fatalf("begin marker appears %d times:\n%s", n, refreshed)
	}
	if refreshed != got {
		t.Fatalf("refresh did not converge:\n%q\nwant\n%q", refreshed, got)
	}
}

func TestEnsureClaudeMD(t *testing.T) {
	root := t.TempDir()

	action, err := ensureClaudeMD(root)
	if err != nil || action != instrCreated {
		t.Fatalf("no CLAUDE.md: %s %v", action, err)
	}
	if got := readFile(t, filepath.Join(root, claudeFile)); strings.TrimSpace(got) != claudeImportLine {
		t.Fatalf("CLAUDE.md = %q, want %q", got, claudeImportLine)
	}

	// An existing CLAUDE.md is authored prose: never edited (spec 008 §17.7).
	const prose = "# Mine\n"
	if err := os.WriteFile(filepath.Join(root, claudeFile), []byte(prose), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md: %v", err)
	}
	action, err = ensureClaudeMD(root)
	if err != nil || action != instrSuggested {
		t.Fatalf("existing CLAUDE.md: %s %v", action, err)
	}
	if got := readFile(t, filepath.Join(root, claudeFile)); got != prose {
		t.Fatalf("CLAUDE.md was edited: %q", got)
	}

	// Once the suggestion has been taken, there is nothing left to suggest.
	if err := os.WriteFile(filepath.Join(root, claudeFile),
		[]byte(prose+"\n"+claudeImportLine+"\n"), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md with import: %v", err)
	}
	if action, err := ensureClaudeMD(root); err != nil || action != instrSatisfied {
		t.Fatalf("CLAUDE.md already importing: %s %v", action, err)
	}
}

// TestEnsureAgentsMDThroughSymlink pins the layout this repo itself uses:
// AGENTS.md is a symlink to CLAUDE.md. The block must land in the target, the
// authored prose must survive, the symlink must stay a symlink, and
// ensureClaudeMD must have nothing to add.
func TestEnsureAgentsMDThroughSymlink(t *testing.T) {
	root := t.TempDir()
	claudePath := filepath.Join(root, claudeFile)
	agentsPath := filepath.Join(root, agentsFile)
	const prose = "# Mine\n\nAuthored prose.\n"
	if err := os.WriteFile(claudePath, []byte(prose), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md: %v", err)
	}
	if err := os.Symlink(claudeFile, agentsPath); err != nil {
		t.Fatalf("symlink AGENTS.md -> CLAUDE.md: %v", err)
	}

	if action, err := ensureAgentsMD(root); err != nil || action != instrUpdated {
		t.Fatalf("ensureAgentsMD: %s %v", action, err)
	}
	got := readFile(t, claudePath)
	if !strings.HasPrefix(got, prose) {
		t.Fatalf("authored prose lost from CLAUDE.md: %q", got)
	}
	if !strings.Contains(got, agentsBlockBegin) {
		t.Fatalf("block did not land in the symlink target: %q", got)
	}
	fi, err := os.Lstat(agentsPath)
	if err != nil {
		t.Fatalf("lstat AGENTS.md: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("AGENTS.md was replaced by a regular file")
	}

	if action, err := ensureClaudeMD(root); err != nil || action != instrSatisfied {
		t.Fatalf("ensureClaudeMD: %s %v", action, err)
	}
	if after := readFile(t, claudePath); after != got {
		t.Fatalf("ensureClaudeMD edited the file: %q", after)
	}

	// Uninstall must not delete the symlink target, which is authored prose.
	if action, err := removeAgentsBlock(root); err != nil || action != instrRemoved {
		t.Fatalf("removeAgentsBlock: %s %v", action, err)
	}
	if stripped := readFile(t, claudePath); stripped != prose {
		t.Fatalf("stripped CLAUDE.md = %q, want %q", stripped, prose)
	}
	if _, err := os.Lstat(agentsPath); err != nil {
		t.Fatalf("AGENTS.md symlink gone: %v", err)
	}
	if action, err := removeClaudeMD(root); err != nil || action != instrNone {
		t.Fatalf("removeClaudeMD on a symlink target: %s %v", action, err)
	}
	if _, err := os.Stat(claudePath); err != nil {
		t.Fatalf("CLAUDE.md deleted: %v", err)
	}
}

func TestRemoveAgentsBlock(t *testing.T) {
	// Block-only file: the whole file goes.
	root := t.TempDir()
	if _, err := ensureAgentsMD(root); err != nil {
		t.Fatalf("ensureAgentsMD: %v", err)
	}
	if action, err := removeAgentsBlock(root); err != nil || action != instrRemoved {
		t.Fatalf("block-only file: %s %v", action, err)
	}
	if _, err := os.Stat(filepath.Join(root, agentsFile)); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md survived a block-only removal: %v", err)
	}

	// Mixed file: the block goes, the rest comes back byte-identical.
	mixed := t.TempDir()
	path := filepath.Join(mixed, agentsFile)
	const prose = "# Ours\n\nHand-written.\n"
	if err := os.WriteFile(path, []byte(prose), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
	if _, err := ensureAgentsMD(mixed); err != nil {
		t.Fatalf("ensureAgentsMD: %v", err)
	}
	if action, err := removeAgentsBlock(mixed); err != nil || action != instrRemoved {
		t.Fatalf("mixed file: %s %v", action, err)
	}
	if got := readFile(t, path); got != prose {
		t.Fatalf("stripped file = %q, want %q", got, prose)
	}

	// No block: nothing to do, and the file is untouched.
	if action, err := removeAgentsBlock(mixed); err != nil || action != instrNone {
		t.Fatalf("no block: %s %v", action, err)
	}
	if got := readFile(t, path); got != prose {
		t.Fatalf("file changed on a no-op removal: %q", got)
	}

	// No file at all is not an error either.
	if action, err := removeAgentsBlock(t.TempDir()); err != nil || action != instrNone {
		t.Fatalf("missing AGENTS.md: %s %v", action, err)
	}
}

func TestRemoveClaudeMD(t *testing.T) {
	// The one-line CLAUDE.md Worklode created is Worklode's to remove.
	root := t.TempDir()
	if _, err := ensureClaudeMD(root); err != nil {
		t.Fatalf("ensureClaudeMD: %v", err)
	}
	if action, err := removeClaudeMD(root); err != nil || action != instrRemoved {
		t.Fatalf("import-only CLAUDE.md: %s %v", action, err)
	}
	if _, err := os.Stat(filepath.Join(root, claudeFile)); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md survived: %v", err)
	}

	// Authored prose is never deleted, even with the import line in it.
	authored := t.TempDir()
	body := "# Mine\n\n" + claudeImportLine + "\n"
	if err := os.WriteFile(filepath.Join(authored, claudeFile), []byte(body), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md: %v", err)
	}
	if action, err := removeClaudeMD(authored); err != nil || action != instrNone {
		t.Fatalf("authored CLAUDE.md: %s %v", action, err)
	}
	if got := readFile(t, filepath.Join(authored, claudeFile)); got != body {
		t.Fatalf("authored CLAUDE.md changed: %q", got)
	}

	if action, err := removeClaudeMD(t.TempDir()); err != nil || action != instrNone {
		t.Fatalf("missing CLAUDE.md: %s %v", action, err)
	}
}

// TestInstallWritesInstructions is the wiring check: the managed block is
// repo-level, so it lands from one install run and comes back out on
// uninstall, reported on both sides.
func TestInstallWritesInstructions(t *testing.T) {
	root := initGitRepo(t)

	res, err := installHooks(discardCmd(), root, claudeTargets(vcsGit, false), harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if res.Instructions == nil {
		t.Fatal("install result carries no instructions stanza")
	}
	if res.Instructions.AgentsMD != instrCreated || res.Instructions.ClaudeMD != instrCreated {
		t.Fatalf("instructions = %+v, want both created", res.Instructions)
	}
	if got := readFile(t, filepath.Join(root, agentsFile)); !strings.Contains(got, agentsBlockBegin) {
		t.Fatalf("AGENTS.md has no managed block: %q", got)
	}

	ures, err := uninstallHooks(root, claudeTargets(vcsGit, false), harness.ScopeLocal)
	if err != nil {
		t.Fatalf("uninstallHooks: %v", err)
	}
	if ures.Instructions == nil {
		t.Fatal("uninstall result carries no instructions stanza")
	}
	if ures.Instructions.AgentsMD != instrRemoved || ures.Instructions.ClaudeMD != instrRemoved {
		t.Fatalf("instructions = %+v, want both removed", ures.Instructions)
	}
	if _, err := os.Stat(filepath.Join(root, agentsFile)); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md survived uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, claudeFile)); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md survived uninstall: %v", err)
	}
}

// The block is repo-level, so a directory outside any git repo has nowhere to
// put it: warn and carry on rather than failing the run.
func TestInstallHooksWarnsOutsideGitRepo(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)
	res, err := installHooks(cmd, dir, hookTargets{}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks outside a git repo: %v", err)
	}
	if res.Instructions != nil {
		t.Fatalf("instructions = %+v, want none outside a git repo", res.Instructions)
	}
	if !strings.Contains(stderr.String(), agentsFile) {
		t.Fatalf("stderr = %q, want a warning naming %s", stderr.String(), agentsFile)
	}
	if _, statErr := os.Stat(filepath.Join(dir, agentsFile)); !os.IsNotExist(statErr) {
		t.Fatalf("AGENTS.md written outside a git repo: %v", statErr)
	}
}

func TestReportInstallInstructionLines(t *testing.T) {
	var buf strings.Builder
	cmd := discardCmd()
	cmd.SetOut(&buf)
	res := installResult{Instructions: &instructionsResult{
		AgentsMD: instrCreated, ClaudeMD: instrSuggested}}
	if err := reportInstall(cmd, res); err != nil {
		t.Fatalf("reportInstall: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, agentsFile+":") || !strings.Contains(out, claudeFile+":") {
		t.Fatalf("report = %q, want one line per instruction file", out)
	}
	if !strings.Contains(out, claudeImportLine) {
		t.Fatalf("report = %q, want the suggestion to name %q", out, claudeImportLine)
	}
}

func TestReportUninstallInstructionLines(t *testing.T) {
	var buf strings.Builder
	cmd := discardCmd()
	cmd.SetOut(&buf)
	res := uninstallResult{Instructions: &instructionsResult{
		AgentsMD: instrRemoved, ClaudeMD: instrNone}}
	if err := reportUninstall(cmd, res); err != nil {
		t.Fatalf("reportUninstall: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, agentsFile+":") || !strings.Contains(out, claudeFile+":") {
		t.Fatalf("report = %q, want one line per instruction file", out)
	}
}
