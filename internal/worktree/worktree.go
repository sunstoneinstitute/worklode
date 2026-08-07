// Package worktree maps Worklode task identity onto git worktrees: the
// <base>/<branch> directory layout, the fallback branch name, and the lease
// identity string the backbone stores.
package worktree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// DefaultBase is the worktree base directory used when worktree_dir /
// LODE_WORKTREE_DIR is unset (spec 030 §3.1).
const DefaultBase = ".worktrees"

// idRe matches a task id anywhere in the worktree's directory name. The base
// directory is the guard; this only extracts (spec 030 §3.2).
var idRe = regexp.MustCompile(`[A-Z][A-Z0-9]*-[0-9]+`)

// Layout is the resolved worktree directory layout for a checkout. Construct
// it with NewLayout — the zero value has no base and rejects every path.
type Layout struct {
	base  string   // slash-separated, as configured
	parts []string // base split into segments
}

// NewLayout validates a configured base directory. It is interpreted relative
// to the git root, so an absolute path or one escaping the root is refused.
func NewLayout(base string) (Layout, error) {
	if base == "" {
		base = DefaultBase
	}
	// IsAbs before trimming: trimming "/" off "/abs/path" would make an
	// absolute path look relative.
	if filepath.IsAbs(base) || strings.HasPrefix(base, "/") {
		return Layout{}, fmt.Errorf("worktree dir %q must be a path relative to the repository root, not an absolute path", base)
	}
	base = strings.Trim(filepath.ToSlash(strings.TrimSpace(base)), "/")
	if base == "" {
		return Layout{}, fmt.Errorf("worktree dir must not be empty")
	}
	parts := strings.Split(base, "/")
	for _, p := range parts {
		if p == "" {
			return Layout{}, fmt.Errorf("worktree dir %q must not contain empty segments (e.g. a doubled %q)", base, "/")
		}
		if p == "." || p == ".." {
			return Layout{}, fmt.Errorf("worktree dir %q must not contain %q or %q segments", base, ".", "..")
		}
	}
	return Layout{base: base, parts: parts}, nil
}

// Base returns the configured base directory, relative to the git root.
func (l Layout) Base() string { return l.base }

// DirName is the directory a branch gets under the base directory. The layout
// is flat, so a "/" from a namespaced template ("team/{{ .id }}-{{ .slug }}")
// is flattened to "-": every worktree is one directory below the base, and
// under the default template the directory name is the branch name verbatim
// (spec 030 §3.1).
func DirName(branch string) string { return strings.ReplaceAll(branch, "/", "-") }

// Dir returns the worktree directory for a branch: <root>/<base>/<dirname>.
//
// Dir panics on a zero Layout: a silently wrong worktree path (dropping the
// base entirely) is far worse than a loud crash — construct with NewLayout.
func (l Layout) Dir(root, branch string) string {
	if len(l.parts) == 0 {
		panic("worktree: Dir called on zero Layout; construct with NewLayout")
	}
	return filepath.Join(root, filepath.FromSlash(l.base), DirName(branch))
}

// ParseDir returns the task id when path is a directory immediately below the
// base directory whose name carries an id. The layout is flat, so exactly one
// segment below the base counts: anything deeper is a path inside a worktree
// (or someone else's directory), not a worktree root. This is the uniform hook
// guard: ok=false ⇒ NOP.
func (l Layout) ParseDir(path string) (taskID string, ok bool) {
	if len(l.parts) == 0 {
		return "", false
	}
	segs := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	idx := lastIndexOf(segs, l.parts)
	if idx < 0 {
		return "", false
	}
	below := segs[idx+len(l.parts):]
	if len(below) != 1 {
		return "", false
	}
	id := idRe.FindString(below[0])
	if id == "" {
		return "", false
	}
	return id, true
}

// lastIndexOf returns the starting index of the last occurrence of sub in
// segs, or -1. The *last* occurrence wins so a repository that itself sits
// inside someone's worktree base still resolves against its own.
func lastIndexOf(segs, sub []string) int {
	for i := len(segs) - len(sub); i >= 0; i-- {
		if slices.Equal(segs[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// BranchName is the client-side fallback branch for a task, used only when a
// server response carries no branch. The server is the authority: it renders
// LODE_BRANCH_TEMPLATE and every response carries the result (spec 030 §1).
func BranchName(taskID, slug string) string { return taskID + "-" + slug }

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
