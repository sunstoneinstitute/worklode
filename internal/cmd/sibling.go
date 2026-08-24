package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// runSibling backs the five compatibility shims 053 §3 keeps for one release:
// lode hook, statusline, serve, watch, and migrate forward to their sibling
// binary and do no mode-specific work. WL-319 removes them after the first
// release containing the split.
func runSibling(ctx context.Context, name, distribution string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	child := exec.CommandContext(ctx, name, args...)
	child.Stdin, child.Stdout, child.Stderr, child.Env = stdin, stdout, stderr, os.Environ()
	if err := runChild(child); err != nil {
		if _, ok := err.(*exec.Error); ok {
			return fmt.Errorf("%s is missing from the %s distribution: %w", name, distribution, err)
		}
		return err
	}
	return nil
}
