package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// hookMarker identifies a pre-commit file as Worklode's own, so a re-run can
// tell "already ours, rewrite in place" from "third-party, preserve it".
const hookMarker = "# worklode-hook"

func init() {
	rootCmd.AddCommand(newInstallHooksCmd())
}

func newInstallHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install-git-hooks",
		Short: "Install Worklode's pre-commit hook into the current repo",
		Long: "Writes a pre-commit hook (into the repo's shared hooks directory, honoring " +
			"core.hooksPath) that invokes `lode hook pre-commit`. If a third-party pre-commit " +
			"hook is already present, it is preserved as pre-commit.pre-lode and chained to; " +
			"otherwise, if .pre-commit-config.yaml exists, it chains to the pre-commit framework. " +
			"Safe to re-run: it converges rather than accumulating renamed hooks.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determine working directory: %w", err)
			}
			hooksDir, chainedTo, err := installGitHooks(cwd)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				b, err := json.Marshal(struct {
					HooksDir  string `json:"hooks_dir"`
					ChainedTo string `json:"chained_to"`
				}{HooksDir: hooksDir, ChainedTo: chainedTo})
				if err != nil {
					return err
				}
				printRaw(cmd, b)
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "installed pre-commit hook in %s\n", hooksDir)
			if chainedTo != "" {
				fmt.Fprintf(out, "chains to: %s\n", chainedTo)
			} else {
				fmt.Fprintln(out, "no existing hook to chain to")
			}
			return nil
		},
	}
	return cmd
}

// installGitHooks writes (or rewrites) Worklode's pre-commit hook into
// repoDir's shared hooks directory. It returns the resolved hooks directory
// and what the installed hook chains to ("" for nothing).
//
// Chain target precedence, evaluated fresh on every run so re-running
// converges rather than accumulates:
//
//  1. A previously preserved third-party hook (pre-commit.pre-lode already on
//     disk) always wins — chain to it, never re-rename, never clobber it.
//  2. Otherwise, an existing pre-commit that is NOT ours (no hookMarker) is a
//     third-party hook seen for the first time: rename it to
//     pre-commit.pre-lode and chain to it.
//  3. Otherwise, a .pre-commit-config.yaml at the repo root chains to the
//     pre-commit framework binary on PATH.
//  4. Otherwise, no chain.
func installGitHooks(repoDir string) (hooksDir, chainedTo string, err error) {
	hooksDir, err = resolveHooksDir(repoDir)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create hooks dir %s: %w", hooksDir, err)
	}

	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	preLodePath := filepath.Join(hooksDir, "pre-commit.pre-lode")

	existing, readErr := os.ReadFile(preCommitPath)
	existingPresent := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", "", fmt.Errorf("read %s: %w", preCommitPath, readErr)
	}
	isOurs := existingPresent && strings.Contains(string(existing), hookMarker)
	preLodePresent := fileExists(preLodePath)

	switch {
	case existingPresent && !isOurs && preLodePresent:
		// A new, unrecognized pre-commit hook has appeared alongside a
		// previously preserved third-party hook. Overwriting it and chaining
		// to the stale .pre-lode would silently drop the newer hook, so refuse
		// and let the user reconcile.
		return "", "", fmt.Errorf(
			"refusing to overwrite %s: an unrecognized pre-commit hook exists alongside %s; "+
				"remove or merge one of them, then re-run", preCommitPath, preLodePath)
	case preLodePresent:
		chainedTo = preLodePath
	case existingPresent && !isOurs:
		if err := os.Rename(preCommitPath, preLodePath); err != nil {
			return "", "", fmt.Errorf("preserve existing pre-commit hook: %w", err)
		}
		chainedTo = preLodePath
	case preCommitConfigExists(repoDir):
		chainedTo = "pre-commit"
	default:
		chainedTo = ""
	}

	if err := os.WriteFile(preCommitPath, []byte(renderHookScript(chainedTo)), 0o755); err != nil {
		return "", "", fmt.Errorf("write %s: %w", preCommitPath, err)
	}
	// os.WriteFile only applies the mode bits to a newly created file; force
	// it when rewriting an existing (ours) hook in place too.
	if err := os.Chmod(preCommitPath, 0o755); err != nil {
		return "", "", fmt.Errorf("chmod %s: %w", preCommitPath, err)
	}

	return hooksDir, chainedTo, nil
}

// resolveHooksDir resolves repoDir's shared hooks directory via
// `git -C repoDir rev-parse --git-path hooks`, which honors core.hooksPath,
// and makes the result absolute (git reports it relative to repoDir).
func resolveHooksDir(repoDir string) (string, error) {
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--git-path", "hooks").Output()
	if err != nil {
		return "", fmt.Errorf("resolve git hooks directory (is %s a git repo?): %w", repoDir, err)
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "", fmt.Errorf("git rev-parse --git-path hooks returned no output")
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

// renderHookScript renders the pre-commit hook body. chainedTo == "" omits
// the --next clause entirely. The chain target is single-quoted so a target
// path containing spaces (common on macOS) is not word-split by /bin/sh before
// lode runs, which would silently drop the chained hook.
func renderHookScript(chainedTo string) string {
	const header = "#!/bin/sh\n" + hookMarker + " v1 — installed by `lode install-git-hooks`; do not edit.\n"
	if chainedTo == "" {
		return header + `exec lode hook pre-commit "$@"` + "\n"
	}
	return header + fmt.Sprintf(`exec lode hook pre-commit --next %s "$@"`, shellSingleQuote(chainedTo)) + "\n"
}

// shellSingleQuote wraps s in single quotes for safe use as one POSIX shell
// word, escaping any embedded single quote as the standard '\” sequence.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
