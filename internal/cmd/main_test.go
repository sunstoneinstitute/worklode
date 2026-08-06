package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain isolates the package from the ambient CLI config before running
// the tests, and drops the shared lode binary's temp dir afterwards (it
// outlives any single test's t.TempDir()).
//
// The isolation matters because config resolution reads both the working
// directory and HOME: `lode` walks up from the working directory looking for
// .worklode/config.toml, then falls back to ~/.config/worklode/config.toml.
// `go test` starts in internal/cmd, inside this repo — which carries its own
// .worklode/config.toml pinning current_project to "worklode" — so without
// this, a command run by a test that does not set up its own repo silently
// scopes to a project the test store has never heard of, and every list comes
// back empty. Pointing the working directory and HOME at empty temp dirs
// makes an unscoped command genuinely unscoped; tests that want a scope still
// set one up themselves (setupRepoConfig, --project).
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "lode-cmd-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create isolation dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	home, wd := filepath.Join(dir, "home"), filepath.Join(dir, "wd")
	for _, d := range []string{home, wd} {
		if err := os.Mkdir(d, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "create %s: %v\n", d, err)
			return 1
		}
	}
	// GOCACHE defaults to a path under HOME, so the temp HOME below would give
	// buildLodeBinary's `go build` an empty cache and recompile the stdlib on
	// every run (~25s). Pin the real cache before HOME moves.
	if os.Getenv("GOCACHE") == "" {
		if out, err := exec.Command("go", "env", "GOCACHE").Output(); err == nil {
			if cache := strings.TrimSpace(string(out)); cache != "" {
				if err := os.Setenv("GOCACHE", cache); err != nil {
					fmt.Fprintf(os.Stderr, "set GOCACHE: %v\n", err)
					return 1
				}
			}
		}
	}
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintf(os.Stderr, "set HOME: %v\n", err)
		return 1
	}
	if err := os.Chdir(wd); err != nil {
		fmt.Fprintf(os.Stderr, "chdir %s: %v\n", wd, err)
		return 1
	}

	code := m.Run()
	if lodeBinary.path != "" {
		os.RemoveAll(filepath.Dir(lodeBinary.path))
	}
	return code
}
