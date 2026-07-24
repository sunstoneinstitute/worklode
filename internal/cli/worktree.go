package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// WorktreeIdentity returns the canonical worktree identity for dir:
// "<hostname>:<abs git worktree root>". Fails outside a git worktree.
func WorktreeIdentity(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git worktree: %w", dir, err)
	}
	root := strings.TrimSpace(string(out))
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("determine hostname: %w", err)
	}
	return host + ":" + root, nil
}
