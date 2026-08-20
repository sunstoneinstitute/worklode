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
// Six of the seven supported harnesses read AGENTS.md; Claude Code reads
// CLAUDE.md, which is why the import line exists.
const (
	agentsFile       = "AGENTS.md"
	claudeFile       = "CLAUDE.md"
	claudeImportLine = "@AGENTS.md"
)

// Actions reported for the instruction files. AGENTS.md is managed, so it is
// created/updated/unchanged; CLAUDE.md is authored prose Worklode never edits,
// so an existing one yields suggested (say the line) or satisfied (the line is
// already there, or AGENTS.md resolves to this same file).
const (
	instrCreated   = "created"
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

// ensureInstructions writes the managed block into the repo root's AGENTS.md
// and, where Worklode may, bootstraps CLAUDE.md to import it.
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
// the CLAUDE.md step sees the state the strip left behind.
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
	return instrUpdated, nil
}

// spliceAgentsBlock returns existing with the managed block current: replacing
// the marked region if it is there, appending it otherwise. A file whose end
// marker was deleted has its region taken as running to EOF, so the result
// still holds exactly one block.
func spliceAgentsBlock(existing string) string {
	out := existing
	if i := strings.Index(existing, agentsBlockBegin); i >= 0 {
		end := len(existing)
		after := i + len(agentsBlockBegin)
		if j := strings.Index(existing[after:], agentsBlockEnd); j >= 0 {
			end = after + j + len(agentsBlockEnd)
		}
		out = existing[:i] + strings.TrimSuffix(agentsBlock, "\n") + existing[end:]
	} else if strings.TrimSpace(existing) == "" {
		out = agentsBlock
	} else {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "\n" + agentsBlock
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

// ensureClaudeMD bootstraps the CLAUDE.md import Claude Code needs. It writes
// a file only where none exists: an existing CLAUDE.md is authored prose, so
// the addition is reported as a suggestion instead (spec 008 §17.7).
func ensureClaudeMD(root string) (string, error) {
	claudePath := filepath.Join(root, claudeFile)
	same, err := sameFile(filepath.Join(root, agentsFile), claudePath)
	if err != nil {
		return "", err
	}
	// AGENTS.md symlinked to CLAUDE.md means the block already sits in the
	// file Claude Code reads; there is nothing to create or suggest.
	if same {
		return instrSatisfied, nil
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
	if !strings.Contains(string(old), agentsBlockBegin) {
		return instrNone, nil
	}
	rest := stripAgentsBlock(string(old))
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

// removeClaudeMD deletes the one-line CLAUDE.md ensureClaudeMD created. A
// CLAUDE.md holding anything else — including the target of an AGENTS.md
// symlink — is authored prose and is left in place.
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

// stripAgentsBlock removes the marked region and the blank line that separated
// it, so a file the block was appended to comes back byte-identical.
func stripAgentsBlock(existing string) string {
	i := strings.Index(existing, agentsBlockBegin)
	if i < 0 {
		return existing
	}
	after := i + len(agentsBlockBegin)
	end := len(existing)
	if j := strings.Index(existing[after:], agentsBlockEnd); j >= 0 {
		end = after + j + len(agentsBlockEnd)
	}
	head := strings.TrimRight(existing[:i], "\n")
	tail := strings.TrimLeft(existing[end:], "\n")
	switch {
	case head == "":
		return tail
	case tail == "":
		return head + "\n"
	default:
		return head + "\n\n" + tail
	}
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
