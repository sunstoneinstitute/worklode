// Package worktree maps Worklode task identity onto git worktrees: the
// deterministic wt/<id>-<slug> directory name, its wl/<id>-<slug> branch,
// and the lease identity string the backbone stores.
package worktree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// dirRe matches a worktree directory's last segment: a task id, optionally
// followed by a lowercase slug. The bare-id form (WL-7) is intentionally valid.
var dirRe = regexp.MustCompile(`^(WL-\d+)(?:-[a-z0-9-]+)?$`)

// DirName returns the deterministic worktree directory name for a task.
func DirName(taskID, slug string) string { return "wt/" + taskID + "-" + slug }

// BranchName returns the branch name for a task's worktree.
func BranchName(taskID, slug string) string { return "wl/" + taskID + "-" + slug }

// ParseDir returns the task id when path's last two segments are
// wt/<WL-n>-<slug>. This is the uniform hook guard: ok=false ⇒ NOP.
func ParseDir(path string) (taskID string, ok bool) {
	dir, last := filepath.Split(filepath.Clean(path))
	if filepath.Base(filepath.Clean(dir)) != "wt" {
		return "", false
	}
	m := dirRe.FindStringSubmatch(last)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Root walks up from dir to the enclosing git worktree root
// (git -C dir rev-parse --show-toplevel); ok=false outside a repo.
func Root(dir string) (string, bool) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// Identity returns "<hostname>:<abs path>" — the lease worktree identity.
// It resolves path to its git worktree root first, so any directory inside a
// worktree yields the same stable identity. Fails outside a git worktree.
func Identity(path string) (string, error) {
	out, err := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s is not inside a git worktree: %s", path, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%s is not inside a git worktree: %w", path, err)
	}
	root := strings.TrimSpace(string(out))
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("determine hostname: %w", err)
	}
	return host + ":" + root, nil
}

// GitDir returns the worktree-private git dir (rev-parse --git-dir, abs).
func GitDir(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s is not inside a git worktree: %s", root, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%s is not inside a git worktree: %w", root, err)
	}
	return strings.TrimSpace(string(out)), nil
}
