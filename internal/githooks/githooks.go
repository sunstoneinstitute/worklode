// Package githooks owns the git hook files `lode install` manages: which
// hooks are ours, what an installed hook chains to, and how install and
// uninstall converge on a repo that may already hold third-party hooks. None
// of it is argument parsing or output rendering, so it lives here rather than
// in internal/cmd, where the merge rules are testable without cobra — the
// same split internal/harness holds for agent settings files.
package githooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// Marker identifies a git hook file as Worklode's own, so a re-run can tell
// "already ours, rewrite in place" from "third-party, preserve it".
const Marker = "# worklode-hook"

// What an uninstall did to one git hook.
const (
	ActionNone     = "none"     // nothing of ours was there to remove
	ActionRemoved  = "removed"  // our hook was removed, nothing to put back
	ActionRestored = "restored" // our hook was removed and the preserved original put back
)

// Hook is one git lifecycle hook `lode install` manages. Framework says
// whether a .pre-commit-config.yaml at the repo root is a legitimate chain
// target for it: the pre-commit binary run bare executes its pre-commit
// stage, which is the wrong thing to fire from commit-msg, post-merge or
// post-commit. Args says the handler reads git's own positional arguments,
// which changes where renderScript puts "$@".
type Hook struct {
	Name      string
	Framework bool
	Args      bool
}

// Managed is the managed set, in lifecycle order. post-merge and post-commit
// are both bound to the same `lode-hook` handler because git splits the cases
// between them: `git merge` fires post-merge, while a squash merge and a
// commit that resolves a merge fire only post-commit.
//
// commit-msg is the trailer stamper (004 §2.4). It reads git's $1, the
// message file, so it is the one hook here with Args set.
var Managed = []Hook{
	{Name: "pre-commit", Framework: true},
	{Name: "commit-msg", Args: true},
	{Name: "post-merge"},
	{Name: "post-commit"},
}

// Chain records what one installed hook chains to ("" for nothing). The json
// tags are `lode install --json`'s stdout contract, which this package
// produces and internal/cmd only embeds.
type Chain struct {
	Hook      string `json:"hook"`
	ChainedTo string `json:"chained_to,omitempty"`
}

// Removal records what an uninstall did to one hook.
type Removal struct {
	Hook   string `json:"hook"`
	Action string `json:"action"`
}

// Install writes (or rewrites) Worklode's git hooks into repoDir's shared
// hooks directory. It returns the resolved hooks directory and, per hook,
// what the installed hook chains to.
//
// Chain target precedence, evaluated fresh on every run so re-running
// converges rather than accumulates:
//
//  1. A previously preserved third-party hook (<name>.pre-lode already on
//     disk) always wins — chain to it, never re-rename, never clobber it.
//  2. Otherwise, an existing <name> that is NOT ours (no Marker) is a
//     third-party hook seen for the first time: rename it to <name>.pre-lode
//     and chain to it.
//  3. Otherwise, for pre-commit only, a .pre-commit-config.yaml at the repo
//     root chains to the pre-commit framework binary on PATH.
//  4. Otherwise, no chain.
//
// A failure on one hook aborts the run: the hooks it already wrote stay, and
// re-running converges, so a partial install is repairable rather than
// something to unwind here.
func Install(repoDir string) (hooksDir string, chains []Chain, err error) {
	hooksDir, err = Dir(repoDir)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create hooks dir %s: %w", hooksDir, err)
	}
	for _, h := range Managed {
		chainedTo, err := installOne(repoDir, hooksDir, h)
		if err != nil {
			return "", nil, err
		}
		chains = append(chains, Chain{Hook: h.Name, ChainedTo: chainedTo})
	}
	return hooksDir, chains, nil
}

// installOne installs one hook and returns what it chains to.
func installOne(repoDir, hooksDir string, h Hook) (chainedTo string, err error) {
	hookPath := filepath.Join(hooksDir, h.Name)
	preLodePath := hookPath + ".pre-lode"

	existing, readErr := os.ReadFile(hookPath)
	existingPresent := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", fmt.Errorf("read %s: %w", hookPath, readErr)
	}
	isOurs := existingPresent && strings.Contains(string(existing), Marker)
	preLodePresent := fileExists(preLodePath)

	switch {
	case existingPresent && !isOurs && preLodePresent:
		// A new, unrecognized hook has appeared alongside a previously
		// preserved third-party hook. Overwriting it and chaining to the
		// stale .pre-lode would silently drop the newer hook, so refuse and
		// let the user reconcile.
		return "", fmt.Errorf(
			"refusing to overwrite %s: an unrecognized %s hook exists alongside %s; "+
				"remove or merge one of them, then re-run", hookPath, h.Name, preLodePath)
	case preLodePresent:
		chainedTo = preLodePath
	case existingPresent && !isOurs:
		if err := os.Rename(hookPath, preLodePath); err != nil {
			return "", fmt.Errorf("preserve existing %s hook: %w", h.Name, err)
		}
		chainedTo = preLodePath
	case h.Framework && preCommitConfigExists(repoDir):
		chainedTo = "pre-commit"
	default:
		chainedTo = ""
	}

	if err := os.WriteFile(hookPath, []byte(renderScript(h, chainedTo)), 0o755); err != nil {
		return "", fmt.Errorf("write %s: %w", hookPath, err)
	}
	// os.WriteFile only applies the mode bits to a newly created file; force
	// it when rewriting an existing (ours) hook in place too.
	if err := os.Chmod(hookPath, 0o755); err != nil {
		return "", fmt.Errorf("chmod %s: %w", hookPath, err)
	}
	return chainedTo, nil
}

// Uninstall removes Worklode's git hooks from repoDir's shared hooks
// directory and restores whatever Install preserved. It returns the resolved
// hooks directory and, per hook, one of the Action constants.
//
// It only ever removes a hook carrying Marker: a hook it does not recognize
// as its own belongs to someone else and is left untouched, mirroring
// Install's refusal to clobber third-party hooks. Uninstalling twice, or in a
// repo that never installed, is a no-op rather than an error. For the same
// reason a <name>.pre-lode with no hook of ours in front of it is left
// alone: only external meddling produces one, and restoring it blindly could
// bury a newer third-party hook.
func Uninstall(repoDir string) (hooksDir string, removals []Removal, err error) {
	hooksDir, err = Dir(repoDir)
	if err != nil {
		return "", nil, err
	}
	for _, h := range Managed {
		action, err := uninstallOne(hooksDir, h.Name)
		if err != nil {
			return "", nil, err
		}
		removals = append(removals, Removal{Hook: h.Name, Action: action})
	}
	return hooksDir, removals, nil
}

// uninstallOne removes one hook and returns what it did.
func uninstallOne(hooksDir, name string) (action string, err error) {
	hookPath := filepath.Join(hooksDir, name)
	preLodePath := hookPath + ".pre-lode"

	existing, readErr := os.ReadFile(hookPath)
	if os.IsNotExist(readErr) {
		return ActionNone, nil
	}
	if readErr != nil {
		return "", fmt.Errorf("read %s: %w", hookPath, readErr)
	}
	if !strings.Contains(string(existing), Marker) {
		return ActionNone, nil
	}

	if err := os.Remove(hookPath); err != nil {
		return "", fmt.Errorf("remove %s: %w", hookPath, err)
	}
	if !fileExists(preLodePath) {
		return ActionRemoved, nil
	}
	if err := os.Rename(preLodePath, hookPath); err != nil {
		return "", fmt.Errorf("restore %s: %w", hookPath, err)
	}
	return ActionRestored, nil
}

// Dir resolves repoDir's shared hooks directory via
// `git -C repoDir rev-parse --git-path hooks`, which honors core.hooksPath,
// and makes the result absolute (git reports it relative to repoDir).
func Dir(repoDir string) (string, error) {
	raw, ok := gitexec.Line(repoDir, "rev-parse", "--git-path", "hooks")
	if !ok {
		return "", fmt.Errorf("resolve git hooks directory (is %s a git repo?)", repoDir)
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(repoDir, raw)
	}
	return filepath.Clean(raw), nil
}

// Installed reports whether repoDir carries our hooks, alongside the resolved
// hooks directory. Presence is judged by the pre-commit hook: it is the one
// every install writes and the heartbeat every harness falls back to, so a
// repo missing it is one `lode install` has not reached. The error is only
// ever "not a git repo"; a hooks directory that exists but holds nothing of
// ours is (dir, false, nil).
func Installed(repoDir string) (hooksDir string, installed bool, err error) {
	hooksDir, err = Dir(repoDir)
	if err != nil {
		return "", false, err
	}
	content, readErr := os.ReadFile(filepath.Join(hooksDir, "pre-commit"))
	return hooksDir, readErr == nil && strings.Contains(string(content), Marker), nil
}

// preCommitConfigExists reports whether .pre-commit-config.yaml exists at
// repoDir's git worktree root.
func preCommitConfigExists(repoDir string) bool {
	root, ok := worktree.Root(repoDir)
	if !ok {
		return false
	}
	return fileExists(filepath.Join(root, ".pre-commit-config.yaml"))
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// renderScript renders the body of one hook. chainedTo == "" omits the
// --next clause entirely. The chain target is single-quoted so a target path
// containing spaces (common on macOS) is not word-split by /bin/sh before lode
// runs, which would silently drop the chained hook.
//
// Where "$@" lands decides who sees git's arguments. `lode-hook` reads the
// words between the event and --next as its own and passes everything after
// --next to the chained hook verbatim, so an unchained script's trailing "$@"
// already reaches the handler. A chained script has to name it twice for both
// to see it, which only an Args hook needs.
func renderScript(h Hook, chainedTo string) string {
	const header = "#!/bin/sh\n" + Marker + " v1 — installed by `lode install`; do not edit.\n"
	if chainedTo == "" {
		return header + fmt.Sprintf(`exec lode-hook %s "$@"`, h.Name) + "\n"
	}
	own := ""
	if h.Args {
		own = ` "$@"`
	}
	return header + fmt.Sprintf(`exec lode-hook %s%s --next %s "$@"`,
		h.Name, own, shellSingleQuote(chainedTo)) + "\n"
}

// shellSingleQuote wraps s in single quotes for safe use as one POSIX shell
// word, escaping each embedded single quote as:
//
//	'\''
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
