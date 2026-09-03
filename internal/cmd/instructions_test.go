package cmd

import (
	"bytes"
	"io"
	"os"
	"os/exec"
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
	if !strings.Contains(first, "lode work next") {
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
	// Head and tail both: the block is appended below the head here, but a
	// hand-placed block has authored prose on both sides of it, and only a
	// fixture with a tail can catch a splice that runs to EOF.
	const head = "# Ours\n\nHand-written.\n"
	const tail = "## Tail\n\nProse the user wrote after the block.\n"
	if err := os.WriteFile(path, []byte(head), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}

	if action, err := ensureAgentsMD(root); err != nil || action != instrAdded {
		t.Fatalf("first run: %s %v", action, err)
	}
	// Move the tail below the block, which is where a hand-edited file has it.
	withTail := readFile(t, path) + "\n" + tail
	if err := os.WriteFile(path, []byte(withTail), 0o644); err != nil {
		t.Fatalf("add tail: %v", err)
	}
	if action, err := ensureAgentsMD(root); err != nil || action != instrUnchanged {
		t.Fatalf("run with a tail: %s %v", action, err)
	}
	got := readFile(t, path)
	if !strings.HasPrefix(got, head) {
		t.Fatalf("head lost: %q", got)
	}
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("tail lost: %q", got)
	}

	// A stale block is replaced in place, not duplicated, and the prose on
	// both sides of it survives.
	stale := strings.Replace(got, "lode work next", "lode obsolete", 1)
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

	// Uninstall gives back exactly the authored bytes, head and tail.
	if action, err := removeAgentsBlock(root); err != nil || action != instrRemoved {
		t.Fatalf("removeAgentsBlock: %s %v", action, err)
	}
	if stripped := readFile(t, path); stripped != head+"\n"+tail {
		t.Fatalf("stripped file = %q, want %q", stripped, head+"\n"+tail)
	}
}

// TestAgentsBlockOrphanBeginMarkerKeepsProse is the data-loss regression: a
// begin marker whose end marker was lost to a bad merge or a hand edit must
// not take the rest of the file with it. The region stops at the end of the
// marker's own line, on both the install and the uninstall side.
func TestAgentsBlockOrphanBeginMarkerKeepsProse(t *testing.T) {
	const orphan = "# Head\n\n" + agentsBlockBegin + "\nold body\n\n## Tail\n\nIMPORTANT USER PROSE\n"

	root := t.TempDir()
	path := filepath.Join(root, agentsFile)
	if err := os.WriteFile(path, []byte(orphan), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
	if action, err := ensureAgentsMD(root); err != nil || action != instrUpdated {
		t.Fatalf("ensureAgentsMD: %s %v", action, err)
	}
	got := readFile(t, path)
	for _, want := range []string{"# Head", "old body", "## Tail", "IMPORTANT USER PROSE"} {
		if !strings.Contains(got, want) {
			t.Fatalf("install lost %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, agentsBlockBegin); n != 1 {
		t.Fatalf("begin marker appears %d times:\n%s", n, got)
	}
	if !strings.Contains(got, agentsBlockEnd) {
		t.Fatalf("install did not repair the missing end marker:\n%s", got)
	}

	// The removal side has the same fallback and so needs the same guarantee.
	bare := t.TempDir()
	barePath := filepath.Join(bare, agentsFile)
	if err := os.WriteFile(barePath, []byte(orphan), 0o644); err != nil {
		t.Fatalf("seed %s: %v", barePath, err)
	}
	if action, err := removeAgentsBlock(bare); err != nil || action != instrRemoved {
		t.Fatalf("removeAgentsBlock: %s %v", action, err)
	}
	stripped := readFile(t, barePath)
	for _, want := range []string{"# Head", "old body", "## Tail", "IMPORTANT USER PROSE"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("uninstall lost %q:\n%s", want, stripped)
		}
	}
	if strings.Contains(stripped, agentsBlockBegin) {
		t.Fatalf("orphan marker survived uninstall:\n%s", stripped)
	}
}

// A file that ended up with two blocks — a bad merge, a copy-paste — converges
// to one, and uninstall leaves no residue behind.
func TestAgentsBlockDuplicateRegionsConverge(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, agentsFile)
	const middle = "## Between\n\nProse between the two blocks.\n"
	doubled := agentsBlock + "\n" + middle + "\n" + agentsBlock
	if err := os.WriteFile(path, []byte(doubled), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}

	if action, err := ensureAgentsMD(root); err != nil || action != instrUpdated {
		t.Fatalf("ensureAgentsMD: %s %v", action, err)
	}
	got := readFile(t, path)
	if n := strings.Count(got, agentsBlockBegin); n != 1 {
		t.Fatalf("begin marker appears %d times:\n%s", n, got)
	}
	if !strings.Contains(got, "Prose between the two blocks.") {
		t.Fatalf("prose between the blocks lost:\n%s", got)
	}
	if action, err := ensureAgentsMD(root); err != nil || action != instrUnchanged {
		t.Fatalf("second run: %s %v", action, err)
	}

	if action, err := removeAgentsBlock(root); err != nil || action != instrRemoved {
		t.Fatalf("removeAgentsBlock: %s %v", action, err)
	}
	if stripped := readFile(t, path); strings.Contains(stripped, agentsBlockBegin) {
		t.Fatalf("uninstall left residue:\n%s", stripped)
	}
}

// Markers quoted inside a fenced code block are documentation, not a managed
// region: a file that only mentions them is appended to, not spliced, and
// uninstall does not treat it as ours.
func TestAgentsBlockIgnoresMarkersInsideAFence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, agentsFile)
	quoted := "# Docs\n\nWorklode delimits its block like this:\n\n```\n" +
		agentsBlockBegin + "\n...\n" + agentsBlockEnd + "\n```\n"
	if err := os.WriteFile(path, []byte(quoted), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}

	if action, err := ensureAgentsMD(root); err != nil || action != instrAdded {
		t.Fatalf("ensureAgentsMD: %s %v", action, err)
	}
	got := readFile(t, path)
	if !strings.HasPrefix(got, quoted) {
		t.Fatalf("the quoted example was spliced instead of preserved:\n%s", got)
	}
	if action, err := removeAgentsBlock(root); err != nil || action != instrRemoved {
		t.Fatalf("removeAgentsBlock: %s %v", action, err)
	}
	if stripped := readFile(t, path); stripped != quoted {
		t.Fatalf("uninstall did not restore the quoted example:\n%q\nwant\n%q", stripped, quoted)
	}
}

// TestAgentsBlockInvocationsResolve puts the managed block under the same
// drift net as the markdown surfaces. The block ships into every user repo, so
// a renamed command or flag rots it exactly the way TestAgentSurfaces exists
// to prevent — but surfaceFiles scans .md, and this text lives in a .go const.
func TestAgentsBlockInvocationsResolve(t *testing.T) {
	invocations := findInvocations(agentsFile, agentsBlock)
	if len(invocations) < 4 {
		t.Fatalf("found %d lode invocations in the block, want the four entry commands",
			len(invocations))
	}
	for _, inv := range invocations {
		if reason := checkInvocation(rootCmd, inv.text); reason != "" {
			t.Errorf("%s block: %s\n\t%s", agentsFile, reason, inv.text)
		}
	}
}

func TestEnsureClaudeMD(t *testing.T) {
	root := t.TempDir()

	action, err := ensureClaudeMD(root)
	if err != nil || action != instrCreated {
		t.Fatalf("no CLAUDE.local.md: %s %v", action, err)
	}
	if got := readFile(t, filepath.Join(root, claudeFile)); strings.TrimSpace(got) != claudeImportLine {
		t.Fatalf("CLAUDE.local.md = %q, want %q", got, claudeImportLine)
	}
	if got := readFile(t, filepath.Join(root, gitignoreFile)); !containsLine(got, claudeFile) {
		t.Fatalf(".gitignore = %q, want a %s line", got, claudeFile)
	}

	// An existing CLAUDE.local.md may carry a developer's own notes: never
	// edited (spec 008 §17.7).
	const prose = "# Mine\n"
	if err := os.WriteFile(filepath.Join(root, claudeFile), []byte(prose), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.local.md: %v", err)
	}
	action, err = ensureClaudeMD(root)
	if err != nil || action != instrSuggested {
		t.Fatalf("existing CLAUDE.local.md: %s %v", action, err)
	}
	if got := readFile(t, filepath.Join(root, claudeFile)); got != prose {
		t.Fatalf("CLAUDE.local.md was edited: %q", got)
	}

	// Once the suggestion has been taken, there is nothing left to suggest.
	if err := os.WriteFile(filepath.Join(root, claudeFile),
		[]byte(prose+"\n"+claudeImportLine+"\n"), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.local.md with import: %v", err)
	}
	if action, err := ensureClaudeMD(root); err != nil || action != instrSatisfied {
		t.Fatalf("CLAUDE.local.md already importing: %s %v", action, err)
	}
}

// TestEnsureClaudeMDGitignoreConverges pins ensureGitignored's own edge cases:
// a missing .gitignore is created, an existing one gains the entry once, a
// hand-edited one missing its trailing newline is not corrupted, and a run
// once the entry is already there changes nothing.
func TestEnsureClaudeMDGitignoreConverges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, gitignoreFile)
	const existing = "*.log" // deliberately no trailing newline
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed %s: %v", gitignoreFile, err)
	}
	if err := ensureGitignored(root, claudeFile); err != nil {
		t.Fatalf("ensureGitignored: %v", err)
	}
	first := readFile(t, path)
	if !strings.HasPrefix(first, existing+"\n") {
		t.Fatalf(".gitignore = %q, want the existing pattern preserved with a newline", first)
	}
	if n := strings.Count(first, claudeFile); n != 1 {
		t.Fatalf(".gitignore has %s %d times: %q", claudeFile, n, first)
	}

	if err := ensureGitignored(root, claudeFile); err != nil {
		t.Fatalf("second ensureGitignored: %v", err)
	}
	if got := readFile(t, path); got != first {
		t.Fatalf("second run changed .gitignore:\n%q\nwant\n%q", got, first)
	}
}

// containsLine reports whether body has line among its lines, trimmed.
func containsLine(body, line string) bool {
	for _, l := range strings.Split(body, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// TestEnsureAgentsMDThroughSymlink pins the AGENTS.md-symlinked-to-CLAUDE.local.md
// layout: the block must land in the target, the authored prose must
// survive, the symlink must stay a symlink, and ensureClaudeMD must have
// nothing to add.
func TestEnsureAgentsMDThroughSymlink(t *testing.T) {
	root := t.TempDir()
	claudePath := filepath.Join(root, claudeFile)
	agentsPath := filepath.Join(root, agentsFile)
	const prose = "# Mine\n\nAuthored prose.\n"
	if err := os.WriteFile(claudePath, []byte(prose), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.local.md: %v", err)
	}
	if err := os.Symlink(claudeFile, agentsPath); err != nil {
		t.Fatalf("symlink AGENTS.md -> CLAUDE.local.md: %v", err)
	}

	if action, err := ensureAgentsMD(root); err != nil || action != instrAdded {
		t.Fatalf("ensureAgentsMD: %s %v", action, err)
	}
	got := readFile(t, claudePath)
	if !strings.HasPrefix(got, prose) {
		t.Fatalf("authored prose lost from CLAUDE.local.md: %q", got)
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
		t.Fatalf("stripped CLAUDE.local.md = %q, want %q", stripped, prose)
	}
	if _, err := os.Lstat(agentsPath); err != nil {
		t.Fatalf("AGENTS.md symlink gone: %v", err)
	}
	if action, err := removeClaudeMD(root); err != nil || action != instrNone {
		t.Fatalf("removeClaudeMD on a symlink target: %s %v", action, err)
	}
	if _, err := os.Stat(claudePath); err != nil {
		t.Fatalf("CLAUDE.local.md deleted: %v", err)
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
	// The one-line CLAUDE.local.md Worklode created is Worklode's to remove.
	root := t.TempDir()
	if _, err := ensureClaudeMD(root); err != nil {
		t.Fatalf("ensureClaudeMD: %v", err)
	}
	if action, err := removeClaudeMD(root); err != nil || action != instrRemoved {
		t.Fatalf("import-only CLAUDE.local.md: %s %v", action, err)
	}
	if _, err := os.Stat(filepath.Join(root, claudeFile)); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.local.md survived: %v", err)
	}

	// Authored prose is never deleted, even with the import line in it.
	authored := t.TempDir()
	body := "# Mine\n\n" + claudeImportLine + "\n"
	if err := os.WriteFile(filepath.Join(authored, claudeFile), []byte(body), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.local.md: %v", err)
	}
	if action, err := removeClaudeMD(authored); err != nil || action != instrNone {
		t.Fatalf("authored CLAUDE.local.md: %s %v", action, err)
	}
	if got := readFile(t, filepath.Join(authored, claudeFile)); got != body {
		t.Fatalf("authored CLAUDE.local.md changed: %q", got)
	}

	if action, err := removeClaudeMD(t.TempDir()); err != nil || action != instrNone {
		t.Fatalf("missing CLAUDE.local.md: %s %v", action, err)
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
		t.Fatalf("CLAUDE.local.md survived uninstall: %v", err)
	}
}

// gitIn runs one git command in dir, failing the test on a non-zero exit.
// Signing and identity are pinned so a temp repo never depends on the
// developer's global config.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{
		"-c", "commit.gpgsign=false",
		"-c", "user.email=test@example.com", "-c", "user.name=test",
	}, args...)...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// linkedWorktree creates <root>/.worktrees/<name> as a real linked worktree —
// the layout `lode work next` produces — and returns its path.
func linkedWorktree(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, ".worktrees", name)
	gitIn(t, root, "worktree", "add", "-b", name, dir)
	return dir
}

// assertWorktreeClean fails when dir's working tree carries any change at all.
// It is the actual regression assertion for WL-219: an install run inside a
// task worktree must leave that task's branch exactly as it found it.
func assertWorktreeClean(t *testing.T, dir string) {
	t.Helper()
	if out := gitIn(t, dir, "status", "--porcelain"); out != "" {
		t.Fatalf("install dirtied the task worktree %s:\n%s", dir, out)
	}
}

// TestInstallFromLinkedWorktreeWritesToMainCheckout is the WL-219 regression:
// AGENTS.md is a tracked file, so an install run from a task worktree anchors
// it at the main checkout — the worktree inherits it rather than committing a
// copy onto its own branch. CLAUDE.local.md is gitignored rather than
// tracked, but it anchors at the same root so the pair stays together and the
// worktree does not grow its own separate copy.
func TestInstallFromLinkedWorktreeWritesToMainCheckout(t *testing.T) {
	root := initGitRepo(t)
	wt := linkedWorktree(t, root, "WL-1-fix-the-thing")

	res, err := installHooks(discardCmd(), wt, hookTargets{vcs: vcsGit}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks from a linked worktree: %v", err)
	}
	if res.Instructions == nil {
		t.Fatal("install result carries no instructions stanza")
	}
	if res.Instructions.AgentsMD != instrCreated || res.Instructions.ClaudeMD != instrCreated {
		t.Fatalf("instructions = %+v, want both created", res.Instructions)
	}
	if got := readFile(t, filepath.Join(root, agentsFile)); !strings.Contains(got, agentsBlockBegin) {
		t.Fatalf("main checkout's AGENTS.md has no managed block: %q", got)
	}
	if fileExists(filepath.Join(wt, agentsFile)) {
		t.Fatalf("%s written into the task worktree %s", agentsFile, wt)
	}
	if fileExists(filepath.Join(wt, claudeFile)) {
		t.Fatalf("%s written into the task worktree %s", claudeFile, wt)
	}
	assertWorktreeClean(t, wt)

	// Uninstall follows the block to where install put it, so running it from
	// the worktree strips the main checkout rather than finding nothing.
	ures, err := uninstallHooks(wt, hookTargets{vcs: vcsGit}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("uninstallHooks from a linked worktree: %v", err)
	}
	if ures.Instructions == nil {
		t.Fatal("uninstall result carries no instructions stanza")
	}
	if ures.Instructions.AgentsMD != instrRemoved || ures.Instructions.ClaudeMD != instrRemoved {
		t.Fatalf("instructions = %+v, want both removed", ures.Instructions)
	}
	if fileExists(filepath.Join(root, agentsFile)) {
		t.Fatalf("%s survived uninstall in the main checkout", agentsFile)
	}
	assertWorktreeClean(t, wt)
}

// TestInstallFromLinkedWorktreeFollowsAgentsSymlink covers AGENTS.md symlinked
// to CLAUDE.local.md, both tracked. The block must land in the main
// checkout's CLAUDE.local.md, through the main checkout's own symlink,
// leaving the task worktree's copies untouched — that pair is exactly what an
// install from a WL-46 worktree dirtied.
func TestInstallFromLinkedWorktreeFollowsAgentsSymlink(t *testing.T) {
	root := initGitRepo(t)
	const authored = "# Repo\n\nAuthored prose.\n"
	if err := os.WriteFile(filepath.Join(root, claudeFile), []byte(authored), 0o644); err != nil {
		t.Fatalf("write %s: %v", claudeFile, err)
	}
	if err := os.Symlink(claudeFile, filepath.Join(root, agentsFile)); err != nil {
		t.Fatalf("symlink %s -> %s: %v", agentsFile, claudeFile, err)
	}
	gitIn(t, root, "add", agentsFile, claudeFile)
	gitIn(t, root, "commit", "-m", "instruction files")

	wt := linkedWorktree(t, root, "WL-2-fix-the-thing")
	if got := readFile(t, filepath.Join(wt, claudeFile)); got != authored {
		t.Fatalf("worktree %s = %q, want the committed prose", claudeFile, got)
	}

	res, err := installHooks(discardCmd(), wt, hookTargets{vcs: vcsGit}, harness.ScopeLocal)
	if err != nil {
		t.Fatalf("installHooks from a linked worktree: %v", err)
	}
	// AGENTS.md gained a block it did not have; CLAUDE.local.md *is* AGENTS.md,
	// so it is already satisfied rather than suggested.
	if res.Instructions.AgentsMD != instrAdded || res.Instructions.ClaudeMD != instrSatisfied {
		t.Fatalf("instructions = %+v, want added/satisfied", res.Instructions)
	}

	// The main checkout's symlink survives, and the block landed in its target.
	info, err := os.Lstat(filepath.Join(root, agentsFile))
	if err != nil {
		t.Fatalf("lstat main %s: %v", agentsFile, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("main %s is no longer a symlink", agentsFile)
	}
	mainClaude := readFile(t, filepath.Join(root, claudeFile))
	if !strings.Contains(mainClaude, agentsBlockBegin) {
		t.Fatalf("main %s has no managed block: %q", claudeFile, mainClaude)
	}
	if !strings.Contains(mainClaude, "Authored prose.") {
		t.Fatalf("main %s lost its authored prose: %q", claudeFile, mainClaude)
	}

	// The task worktree's own pair is byte-for-byte what it was checked out as.
	if got := readFile(t, filepath.Join(wt, claudeFile)); got != authored {
		t.Fatalf("task worktree %s was rewritten: %q", claudeFile, got)
	}
	assertWorktreeClean(t, wt)
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
	// Naming the file is not enough: the "unexpected result" arm does that
	// too. Pin the sentence each action is supposed to produce.
	for _, want := range []string{
		agentsFile + ": created with the Worklode block",
		claudeFile + ": claude-code reads this file",
		claudeImportLine,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "unexpected result") {
		t.Fatalf("report = %q, want no unexpected-result line", out)
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
	for _, want := range []string{
		agentsFile + ": removed the Worklode block",
		claudeFile + ": left alone (local notes)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "unexpected result") {
		t.Fatalf("report = %q, want no unexpected-result line", out)
	}
}

// Appending the block to a file that carried none is not refreshing one, so
// the two must not print the same sentence.
func TestReportInstallDistinguishesAddedFromRefreshed(t *testing.T) {
	lines := map[string]string{}
	for _, action := range []string{instrAdded, instrUpdated} {
		var buf strings.Builder
		cmd := discardCmd()
		cmd.SetOut(&buf)
		res := installResult{Instructions: &instructionsResult{
			AgentsMD: action, ClaudeMD: instrSatisfied}}
		if err := reportInstall(cmd, res); err != nil {
			t.Fatalf("reportInstall %s: %v", action, err)
		}
		if strings.Contains(buf.String(), "unexpected result") {
			t.Fatalf("%s: report = %q", action, buf.String())
		}
		lines[action] = buf.String()
	}
	if lines[instrAdded] == lines[instrUpdated] {
		t.Fatalf("added and refreshed both report %q", lines[instrAdded])
	}
	if !strings.Contains(lines[instrAdded], "added the Worklode block") {
		t.Fatalf("added report = %q", lines[instrAdded])
	}
}

// An AGENTS.md that only @-imports other instruction files is a hub: appending
// the block there would put it in a file Claude Code never reads, so it goes
// into the imported CLAUDE.local.md instead — and stays there on refresh.
func TestInstructionsFollowAgentsImports(t *testing.T) {
	root := t.TempDir()
	const hub = "@CLAUDE.md\n@CLAUDE.local.md\n"
	writeFile(t, root, agentsFile, hub)
	writeFile(t, root, "CLAUDE.md", "# Committed prose\n")
	writeFile(t, root, claudeFile, "## Local notes\n")

	res, err := ensureInstructions(root)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if res.AgentsMD != instrAdded || res.ClaudeMD != instrSatisfied || res.BlockFile != claudeFile {
		t.Fatalf("got %+v", res)
	}
	if got := readFile(t, filepath.Join(root, agentsFile)); got != hub {
		t.Fatalf("hub was edited: %q", got)
	}
	if got := readFile(t, filepath.Join(root, "CLAUDE.md")); strings.Contains(got, agentsBlockBegin) {
		t.Fatalf("block landed in committed prose: %q", got)
	}
	local := readFile(t, filepath.Join(root, claudeFile))
	if !strings.Contains(local, agentsBlockBegin) || !strings.Contains(local, "Local notes") {
		t.Fatalf("block not spliced into %s: %q", claudeFile, local)
	}

	// A stale block in the imported file is refreshed in place, not duplicated
	// into the hub.
	writeFile(t, root, claudeFile,
		"## Local notes\n\n"+agentsBlockBegin+"\nstale\n"+agentsBlockEnd+"\n")
	res, err = ensureInstructions(root)
	if err != nil || res.AgentsMD != instrUpdated {
		t.Fatalf("refresh: %+v %v", res, err)
	}
	if got := readFile(t, filepath.Join(root, claudeFile)); strings.Contains(got, "stale") {
		t.Fatalf("stale block survived: %q", got)
	}
	if got := readFile(t, filepath.Join(root, agentsFile)); got != hub {
		t.Fatalf("hub was edited on refresh: %q", got)
	}

	// Uninstall strips it from the same file and leaves the notes.
	if res, err = removeInstructions(root); err != nil || res.AgentsMD != instrRemoved {
		t.Fatalf("remove: %+v %v", res, err)
	}
	got := readFile(t, filepath.Join(root, claudeFile))
	if strings.Contains(got, agentsBlockBegin) || !strings.Contains(got, "Local notes") {
		t.Fatalf("uninstall left %s wrong: %q", claudeFile, got)
	}
}
