package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The two repo-root instruction files `lode install` manages (spec 008 §17.7).
// Six of the seven supported harnesses read AGENTS.md; Claude Code also reads
// CLAUDE.local.md, which is why the import line exists there rather than in
// CLAUDE.md — CLAUDE.md is committed, authored prose Worklode has no business
// editing, while CLAUDE.local.md is per-checkout and gitignored, so the
// import is safe to write unconditionally.
const (
	agentsFile       = "AGENTS.md"
	claudeFile       = "CLAUDE.local.md"
	claudeImportLine = "@AGENTS.md"
	gitignoreFile    = ".gitignore"
)

// Actions reported for the instruction files. AGENTS.md is managed, so it is
// created (no file at all), added (a file carrying no block of ours), updated
// (a block of ours replaced) or unchanged; an existing CLAUDE.local.md may
// carry a developer's own notes, so Worklode never edits it, and an existing
// one yields suggested (say the line) or satisfied (the line is already
// there, or AGENTS.md resolves to this same file).
const (
	instrCreated   = "created"
	instrAdded     = "added"
	instrUpdated   = "updated"
	instrUnchanged = "unchanged"
	instrSuggested = "suggested"
	instrSatisfied = "satisfied"
	instrRemoved   = "removed"
	instrNone      = "none"
)

const (
	agentsBlockBegin = "<!-- worklode:begin — managed by `lode install`; edits inside are overwritten -->"
	agentsBlockEnd   = "<!-- worklode:end -->"
)

// agentsBlock is the two facts an agent needs before a brief exists (spec 008
// §17.7): that this repo is Worklode-tracked, and how work is entered.
// Deliberately short — the task brief, not this file, carries task context.
const agentsBlock = agentsBlockBegin + `
## Worklode

This repository is tracked by Worklode (` + "`lode`" + `). Work is entered by
claiming a task, which also creates the worktree the work happens in:

- ` + "`lode next`" + ` claims the highest-ranked ready task and creates its worktree.
- ` + "`lode task claim <id>`" + ` claims one specific task instead.
- ` + "`lode resume <dir>`" + ` re-enters a worktree that already exists.
- ` + "`lode status`" + ` reports the current worktree's task and lease.

The claimed task's brief carries the work itself; this block only says how to
reach it.
` + agentsBlockEnd + "\n"

// instructionsResult reports what one run did to each instruction file.
type instructionsResult struct {
	AgentsMD string `json:"agents_md,omitempty"`
	ClaudeMD string `json:"claude_md,omitempty"`
}

// ensureInstructions writes the managed block into root's AGENTS.md and, where
// Worklode may, bootstraps CLAUDE.local.md to import it. root is the *main*
// worktree's root (worktree.MainRoot), so a run from inside a linked worktree
// refreshes the main checkout's files rather than the worktree's own copies.
func ensureInstructions(root string) (*instructionsResult, error) {
	agents, err := ensureAgentsMD(root)
	if err != nil {
		return nil, err
	}
	claude, err := ensureClaudeMD(root)
	if err != nil {
		return nil, err
	}
	return &instructionsResult{AgentsMD: agents, ClaudeMD: claude}, nil
}

// removeInstructions is ensureInstructions' inverse. AGENTS.md goes first so
// the CLAUDE.local.md step sees the state the strip left behind.
func removeInstructions(root string) (*instructionsResult, error) {
	agents, err := removeAgentsBlock(root)
	if err != nil {
		return nil, err
	}
	claude, err := removeClaudeMD(root)
	if err != nil {
		return nil, err
	}
	return &instructionsResult{AgentsMD: agents, ClaudeMD: claude}, nil
}

// ensureAgentsMD splices the managed block into root's AGENTS.md, creating the
// file when it is missing and leaving every byte outside the markers alone.
func ensureAgentsMD(root string) (string, error) {
	path := filepath.Join(root, agentsFile)
	old, err := os.ReadFile(path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	// Whether a block of ours was already there is what separates appending
	// one from refreshing one; the report says which.
	had := len(findBlockRegions(string(old))) > 0
	next := spliceAgentsBlock(string(old))
	if !missing && next == string(old) {
		return instrUnchanged, nil
	}
	// Reading and writing follow symlinks on purpose: AGENTS.md is very often
	// a link to CLAUDE.md, and that layout is asking for the block to land in
	// the target. Replacing the link with a regular file would break it.
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if missing {
		return instrCreated, nil
	}
	if !had {
		return instrAdded, nil
	}
	return instrUpdated, nil
}

// blockRegion is one managed region's byte range in a file, from the first
// byte of its begin-marker line to the byte after its end-marker line.
type blockRegion struct{ start, end int }

// findBlockRegions locates every managed region in body.
//
// A marker counts only on a line of its own and outside a fenced code block,
// so an AGENTS.md that quotes the markers in an example is not mistaken for a
// managed file. A begin marker with no end marker bounds its region at the end
// of its own line: the missing marker never claimed the bytes below it, and
// swallowing them to EOF would delete authored prose.
func findBlockRegions(body string) []blockRegion {
	var out []blockRegion
	inFence := false
	open, openEnd := -1, -1

	for pos := 0; pos <= len(body); {
		nl := strings.IndexByte(body[pos:], '\n')
		line, lineEnd := body[pos:], len(body)
		if nl >= 0 {
			line, lineEnd = body[pos:pos+nl], pos+nl+1
		}
		switch trimmed := strings.TrimSpace(line); {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
		case inFence:
			// A marker inside a fence is quoted text, not a marker.
		case trimmed == agentsBlockBegin:
			// A second begin before any end closes the first at its own line.
			if open >= 0 {
				out = append(out, blockRegion{open, openEnd})
			}
			open, openEnd = pos, lineEnd
		case trimmed == agentsBlockEnd && open >= 0:
			out = append(out, blockRegion{open, lineEnd})
			open = -1
		}
		if nl < 0 {
			break
		}
		pos = lineEnd
	}
	if open >= 0 {
		out = append(out, blockRegion{open, openEnd})
	}
	return out
}

// rebuild reassembles body with every managed region removed and, when block
// is non-empty, block put back where the first region was. A file that somehow
// carries two blocks converges to one without losing what sat between them.
//
// The authored chunks between regions keep their interior bytes; only the
// blank lines that separated a chunk from a region are normalized, which is
// what makes an appended block round-trip byte-for-byte through install and
// uninstall.
func rebuild(body string, regions []blockRegion, block string) string {
	chunks := make([]string, 0, len(regions)+1)
	prev := 0
	for _, r := range regions {
		chunks = append(chunks, body[prev:r.start])
		prev = r.end
	}
	chunks = append(chunks, body[prev:])

	parts := make([]string, 0, len(chunks)+1)
	for i, c := range chunks {
		if i == 1 && block != "" {
			parts = append(parts, block)
		}
		if t := strings.Trim(c, "\n"); strings.TrimSpace(t) != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// spliceAgentsBlock returns existing with the managed block current: replacing
// the marked region where there is one, appending it otherwise.
func spliceAgentsBlock(existing string) string {
	regions := findBlockRegions(existing)
	if len(regions) == 0 {
		if strings.TrimSpace(existing) == "" {
			return agentsBlock
		}
		return strings.TrimRight(existing, "\n") + "\n\n" + agentsBlock
	}
	return rebuild(existing, regions, strings.TrimSuffix(agentsBlock, "\n"))
}

// ensureClaudeMD bootstraps the CLAUDE.local.md import Claude Code needs. It
// writes a file only where none exists: an existing CLAUDE.local.md may carry
// a developer's own local notes, so the addition is reported as a suggestion
// instead (spec 008 §17.7). Either way, the file is gitignored, since it is
// per-checkout state, never something to commit.
func ensureClaudeMD(root string) (string, error) {
	claudePath := filepath.Join(root, claudeFile)
	same, err := sameFile(filepath.Join(root, agentsFile), claudePath)
	if err != nil {
		return "", err
	}
	// AGENTS.md symlinked to CLAUDE.local.md means the block already sits in
	// the file Claude Code reads; there is nothing to create or suggest.
	if same {
		return instrSatisfied, nil
	}
	if err := ensureGitignored(root, claudeFile); err != nil {
		return "", err
	}
	b, err := os.ReadFile(claudePath)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(claudePath, []byte(claudeImportLine+"\n"), 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", claudePath, err)
		}
		return instrCreated, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", claudePath, err)
	}
	if hasImportLine(string(b)) {
		return instrSatisfied, nil
	}
	return instrSuggested, nil
}

// ensureGitignored appends name to root's tracked .gitignore, once. Unlike
// hookrun.ensureExcluded's info/exclude (worktree-local machine state), this
// entry belongs in every clone's .gitignore: CLAUDE.local.md is a convention
// every contributor's checkout produces, not just this machine's.
func ensureGitignored(root, name string) error {
	path := filepath.Join(root, gitignoreFile)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == name {
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	// A hand-edited file with no trailing newline would otherwise get name
	// merged onto the end of its last pattern.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Fprintln(f)
	}
	fmt.Fprintln(f, name)
	return nil
}

// removeAgentsBlock strips the managed region from root's AGENTS.md. The file
// itself is deleted only when it is a regular file with nothing but the block
// in it: a symlink's target belongs to whoever wrote it.
func removeAgentsBlock(root string) (string, error) {
	path := filepath.Join(root, agentsFile)
	old, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return instrNone, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	regions := findBlockRegions(string(old))
	if len(regions) == 0 {
		return instrNone, nil
	}
	rest := rebuild(string(old), regions, "")
	if strings.TrimSpace(rest) == "" {
		regular, err := isRegularFile(path)
		if err != nil {
			return "", err
		}
		if regular {
			if err := os.Remove(path); err != nil {
				return "", fmt.Errorf("remove %s: %w", path, err)
			}
			return instrRemoved, nil
		}
	}
	if err := os.WriteFile(path, []byte(rest), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return instrRemoved, nil
}

// removeClaudeMD deletes the one-line CLAUDE.local.md ensureClaudeMD created.
// A CLAUDE.local.md holding anything else — including the target of an
// AGENTS.md symlink — carries a developer's own notes and is left in place.
// The .gitignore entry ensureGitignored added is left alone too: it is
// harmless to keep ignoring a file that may still hold those notes.
func removeClaudeMD(root string) (string, error) {
	claudePath := filepath.Join(root, claudeFile)
	same, err := sameFile(filepath.Join(root, agentsFile), claudePath)
	if err != nil {
		return "", err
	}
	if same {
		return instrNone, nil
	}
	b, err := os.ReadFile(claudePath)
	if errors.Is(err, fs.ErrNotExist) {
		return instrNone, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", claudePath, err)
	}
	if strings.TrimSpace(string(b)) != claudeImportLine {
		return instrNone, nil
	}
	regular, err := isRegularFile(claudePath)
	if err != nil {
		return "", err
	}
	if !regular {
		return instrNone, nil
	}
	if err := os.Remove(claudePath); err != nil {
		return "", fmt.Errorf("remove %s: %w", claudePath, err)
	}
	return instrRemoved, nil
}

// hasImportLine reports whether body already imports AGENTS.md.
func hasImportLine(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == claudeImportLine {
			return true
		}
	}
	return false
}

// sameFile reports whether a and b resolve to the same file. A missing file is
// not an error: it is simply not the same file as anything.
func sameFile(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", a, err)
	}
	fb, err := os.Stat(b)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", b, err)
	}
	return os.SameFile(fa, fb), nil
}

// isRegularFile reports whether path is a file rather than a symlink to one.
func isRegularFile(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("lstat %s: %w", path, err)
	}
	return fi.Mode().IsRegular(), nil
}
