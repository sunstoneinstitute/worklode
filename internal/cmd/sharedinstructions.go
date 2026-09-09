package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const sharedInstructionsFile = ".worklode/agent-instructions.md"
const instructionReadLine = "> Before starting work, read `.worklode/agent-instructions.md` in the repository root if it exists."

// Shared instructions contain no machine-specific data and may be committed
// with the root pointers, so new clones and linked worktrees inherit them.
func ensureSharedInstructions(root string) (*instructionsResult, error) {
	action, err := ensureManagedBlock(filepath.Join(root, sharedInstructionsFile))
	if err != nil {
		return nil, err
	}

	// Strip only our marked regions from the known old locations. Personal
	// notes and imports remain untouched, including through symlinks.
	for _, name := range []string{agentsFile, "CLAUDE.md", claudeFile} {
		if _, err := removeManagedBlock(filepath.Join(root, name)); err != nil {
			return nil, err
		}
	}
	if _, err := removeClaudeMD(root); err != nil {
		return nil, err
	}
	res := &instructionsResult{SharedMD: action, BlockFile: sharedInstructionsFile}
	res.AgentsMD, err = ensureInstructionPointer(filepath.Join(root, agentsFile))
	if err != nil {
		return nil, err
	}
	res.ClaudeMD, err = ensureInstructionPointer(filepath.Join(root, "CLAUDE.md"))
	return res, err
}

func ensureInstructionPointer(path string) (string, error) {
	body, err := os.ReadFile(path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	inFence := false
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
		if !inFence && trimmed == instructionReadLine {
			return instrUnchanged, nil
		}
	}
	next := string(body)
	if len(next) > 0 {
		if !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += "\n"
	}
	next += instructionReadLine + "\n"
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if missing {
		return instrCreated, nil
	}
	return instrAdded, nil
}
