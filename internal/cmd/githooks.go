package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// hookMarker identifies a git hook file as Worklode's own, so a re-run can
// tell "already ours, rewrite in place" from "third-party, preserve it".
const hookMarker = "# worklode-hook"

// What an uninstall did to one git hook.
const (
	hookActionNone     = "none"     // nothing of ours was there to remove
	hookActionRemoved  = "removed"  // our hook was removed, nothing to put back
	hookActionRestored = "restored" // our hook was removed and the preserved original put back
)

// gitHook is one git lifecycle hook `lode install` manages. framework says
// whether a .pre-commit-config.yaml at the repo root is a legitimate chain
// target for it: the pre-commit binary run bare executes its pre-commit
// stage, which is the wrong thing to fire from commit-msg, post-merge or
// post-commit. args says the handler reads git's own positional arguments,
// which changes where renderHookScript puts "$@".
type gitHook struct {
	name      string
	framework bool
	args      bool
}

// gitHooks is the managed set, in lifecycle order. post-merge and
// post-commit are both bound to the same `lode hook` handler because git
// splits the cases between them: `git merge` fires post-merge, while a squash
// merge and a commit that resolves a merge fire only post-commit.
//
// commit-msg is the trailer stamper (004 §2.4). It reads git's $1, the
// message file, so it is the one hook here with args set.
var gitHooks = []gitHook{
	{name: "pre-commit", framework: true},
	{name: "commit-msg", args: true},
	{name: "post-merge"},
	{name: "post-commit"},
}

// hookChain records what one installed hook chains to ("" for nothing).
type hookChain struct {
	Hook      string `json:"hook"`
	ChainedTo string `json:"chained_to,omitempty"`
}

// hookRemoval records what an uninstall did to one hook.
type hookRemoval struct {
	Hook   string `json:"hook"`
	Action string `json:"action"`
}

// installGitHooks writes (or rewrites) Worklode's git hooks into repoDir's
// shared hooks directory. It returns the resolved hooks directory and, per
// hook, what the installed hook chains to.
//
// Chain target precedence, evaluated fresh on every run so re-running
// converges rather than accumulates:
//
//  1. A previously preserved third-party hook (<name>.pre-lode already on
//     disk) always wins — chain to it, never re-rename, never clobber it.
//  2. Otherwise, an existing <name> that is NOT ours (no hookMarker) is a
//     third-party hook seen for the first time: rename it to <name>.pre-lode
//     and chain to it.
//  3. Otherwise, for pre-commit only, a .pre-commit-config.yaml at the repo
//     root chains to the pre-commit framework binary on PATH.
//  4. Otherwise, no chain.
//
// A failure on one hook aborts the run: the hooks it already wrote stay, and
// re-running converges, so a partial install is repairable rather than
// something to unwind here.
func installGitHooks(repoDir string) (hooksDir string, chains []hookChain, err error) {
	hooksDir, err = resolveHooksDir(repoDir)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create hooks dir %s: %w", hooksDir, err)
	}
	for _, h := range gitHooks {
		chainedTo, err := installGitHook(repoDir, hooksDir, h)
		if err != nil {
			return "", nil, err
		}
		chains = append(chains, hookChain{Hook: h.name, ChainedTo: chainedTo})
	}
	return hooksDir, chains, nil
}

// installGitHook installs one hook and returns what it chains to.
func installGitHook(repoDir, hooksDir string, h gitHook) (chainedTo string, err error) {
	hookPath := filepath.Join(hooksDir, h.name)
	preLodePath := hookPath + ".pre-lode"

	existing, readErr := os.ReadFile(hookPath)
	existingPresent := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", fmt.Errorf("read %s: %w", hookPath, readErr)
	}
	isOurs := existingPresent && strings.Contains(string(existing), hookMarker)
	preLodePresent := fileExists(preLodePath)

	switch {
	case existingPresent && !isOurs && preLodePresent:
		// A new, unrecognized hook has appeared alongside a previously
		// preserved third-party hook. Overwriting it and chaining to the
		// stale .pre-lode would silently drop the newer hook, so refuse and
		// let the user reconcile.
		return "", fmt.Errorf(
			"refusing to overwrite %s: an unrecognized %s hook exists alongside %s; "+
				"remove or merge one of them, then re-run", hookPath, h.name, preLodePath)
	case preLodePresent:
		chainedTo = preLodePath
	case existingPresent && !isOurs:
		if err := os.Rename(hookPath, preLodePath); err != nil {
			return "", fmt.Errorf("preserve existing %s hook: %w", h.name, err)
		}
		chainedTo = preLodePath
	case h.framework && preCommitConfigExists(repoDir):
		chainedTo = "pre-commit"
	default:
		chainedTo = ""
	}

	if err := os.WriteFile(hookPath, []byte(renderHookScript(h, chainedTo)), 0o755); err != nil {
		return "", fmt.Errorf("write %s: %w", hookPath, err)
	}
	// os.WriteFile only applies the mode bits to a newly created file; force
	// it when rewriting an existing (ours) hook in place too.
	if err := os.Chmod(hookPath, 0o755); err != nil {
		return "", fmt.Errorf("chmod %s: %w", hookPath, err)
	}
	return chainedTo, nil
}

// uninstallGitHooks removes Worklode's git hooks from repoDir's shared hooks
// directory and restores whatever install preserved. It returns the resolved
// hooks directory and, per hook, one of the hookAction constants.
//
// It only ever removes a hook carrying hookMarker: a hook it does not
// recognize as its own belongs to someone else and is left untouched, mirroring
// install's refusal to clobber third-party hooks. Uninstalling twice, or in a
// repo that never installed, is a no-op rather than an error. For the same
// reason a <name>.pre-lode with no hook of ours in front of it is left
// alone: only external meddling produces one, and restoring it blindly could
// bury a newer third-party hook.
func uninstallGitHooks(repoDir string) (hooksDir string, removals []hookRemoval, err error) {
	hooksDir, err = resolveHooksDir(repoDir)
	if err != nil {
		return "", nil, err
	}
	for _, h := range gitHooks {
		action, err := uninstallGitHook(hooksDir, h.name)
		if err != nil {
			return "", nil, err
		}
		removals = append(removals, hookRemoval{Hook: h.name, Action: action})
	}
	return hooksDir, removals, nil
}

// uninstallGitHook removes one hook and returns what it did.
func uninstallGitHook(hooksDir, name string) (action string, err error) {
	hookPath := filepath.Join(hooksDir, name)
	preLodePath := hookPath + ".pre-lode"

	existing, readErr := os.ReadFile(hookPath)
	if os.IsNotExist(readErr) {
		return hookActionNone, nil
	}
	if readErr != nil {
		return "", fmt.Errorf("read %s: %w", hookPath, readErr)
	}
	if !strings.Contains(string(existing), hookMarker) {
		return hookActionNone, nil
	}

	if err := os.Remove(hookPath); err != nil {
		return "", fmt.Errorf("remove %s: %w", hookPath, err)
	}
	if !fileExists(preLodePath) {
		return hookActionRemoved, nil
	}
	if err := os.Rename(preLodePath, hookPath); err != nil {
		return "", fmt.Errorf("restore %s: %w", hookPath, err)
	}
	return hookActionRestored, nil
}

// resolveHooksDir resolves repoDir's shared hooks directory via
// `git -C repoDir rev-parse --git-path hooks`, which honors core.hooksPath,
// and makes the result absolute (git reports it relative to repoDir).
func resolveHooksDir(repoDir string) (string, error) {
	raw, ok := gitexec.Line(repoDir, "rev-parse", "--git-path", "hooks")
	if !ok {
		return "", fmt.Errorf("resolve git hooks directory (is %s a git repo?)", repoDir)
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(repoDir, raw)
	}
	return filepath.Clean(raw), nil
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

// renderHookScript renders the body of one hook. chainedTo == "" omits the
// --next clause entirely. The chain target is single-quoted so a target path
// containing spaces (common on macOS) is not word-split by /bin/sh before lode
// runs, which would silently drop the chained hook.
//
// Where "$@" lands decides who sees git's arguments. `lode hook` reads the
// words between the event and --next as its own and passes everything after
// --next to the chained hook verbatim, so an unchained script's trailing "$@"
// already reaches the handler. A chained script has to name it twice for both
// to see it, which only an args hook needs.
func renderHookScript(h gitHook, chainedTo string) string {
	const header = "#!/bin/sh\n" + hookMarker + " v1 — installed by `lode install`; do not edit.\n"
	if chainedTo == "" {
		return header + fmt.Sprintf(`exec lode hook %s "$@"`, h.name) + "\n"
	}
	own := ""
	if h.args {
		own = ` "$@"`
	}
	return header + fmt.Sprintf(`exec lode hook %s%s --next %s "$@"`,
		h.name, own, shellSingleQuote(chainedTo)) + "\n"
}

// shellSingleQuote wraps s in single quotes for safe use as one POSIX shell
// word, escaping each embedded single quote as:
//
//	'\''
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
