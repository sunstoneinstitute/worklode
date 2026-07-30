package cli

import (
	"context"
	"os/exec"
	"strings"
)

// gitRemoteURL returns the origin remote URL of the repo containing dir, or
// "" when dir is not in a git repo, the repo has no origin, git is not
// installed, or ctx ends first — a hung git (a dead network mount holding the
// worktree) must not hang the command. Scope resolution treats "" as "no
// remote to resolve" and falls through to an unscoped command, so a missing
// remote is never an error.
func gitRemoteURL(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
