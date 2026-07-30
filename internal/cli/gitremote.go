package cli

import (
	"os/exec"
	"strings"
)

// gitRemoteURL returns the origin remote URL of the repo containing dir, or
// "" when dir is not in a git repo, the repo has no origin, or git is not
// installed. Scope resolution treats "" as "no remote to resolve" and falls
// through to an unscoped command — a missing remote is never an error.
func gitRemoteURL(dir string) string {
	cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
