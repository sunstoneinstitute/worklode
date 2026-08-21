package cmd

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateCommandRef regenerates the golden file this test checks, mirroring
// the repo's other generated-artifact tests (internal/ns/ns_test.go, run
// against scripts/nsgen.py): the test re-derives the expected content from
// the live source of truth — here, the cobra tree itself — and fails with
// the regen command when the checked-in file disagrees.
var updateCommandRef = flag.Bool("update-command-ref", false,
	"regenerate plugins/claude/lode/skills/worklode/references/commands.md")

// commandRefPath is the golden file's location, relative to the repo root.
const commandRefPath = "plugins/claude/lode/skills/worklode/references/commands.md"

// TestCommandReference guards the worklode skill's on-demand command
// catalog against CLI drift: a renamed command, added command, or changed
// flag set changes rootCmd and this test fails until the file is
// regenerated. TestAgentSurfaces separately checks that every invocation
// the file already contains still resolves; this test instead checks the
// file is the *complete*, current tree — the gap TestAgentSurfaces cannot
// see, since it only flags invocations that stopped resolving, never ones
// silently missing.
func TestCommandReference(t *testing.T) {
	got := renderCommandReference(rootCmd)
	path := filepath.Join(packageDir, "..", "..", commandRefPath)

	if *updateCommandRef {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", commandRefPath, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run: go test ./internal/cmd -run TestCommandReference -update-command-ref)", commandRefPath, err)
	}
	if got != string(want) {
		t.Errorf("%s is stale — run: go test ./internal/cmd -run TestCommandReference -update-command-ref", commandRefPath)
	}
}
