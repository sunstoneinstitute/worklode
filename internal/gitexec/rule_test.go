package gitexec_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// gitSpawn matches a direct subprocess spawn of git — the thing this package
// exists to be the only instance of.
var gitSpawn = regexp.MustCompile(`exec\.Command(Context)?\((ctx, )?"git"`)

// TestGitRunsThroughThisPackage: worklode shells out to git in one place, so
// that a policy change (the GIT_OPTIONAL_LOCKS/GIT_TERMINAL_PROMPT
// environment, a timeout, `-c` hardening) lands everywhere at once. Four
// packages each rolled their own wrapper before WL-165; this keeps a fifth
// from appearing without anyone noticing.
//
// Test files are exempt: a test that drives git to build a fixture repo is
// asserting on git's behaviour, not carrying worklode's policy.
func TestGitRunsThroughThisPackage(t *testing.T) {
	root := repoRoot(t)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == ".worktrees" || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if filepath.Dir(rel) == filepath.Join("internal", "gitexec") {
			return nil // this package is the one allowed to spawn git
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // walking this repo's own source
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(src), "\n") {
			if gitSpawn.MatchString(line) {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(offenders) > 0 {
		t.Errorf("git is spawned directly at %s;\n"+
			"use internal/gitexec (Text/Line/Bytes/OK/Run, or Cmd when the call needs stdin) "+
			"so worklode's git policy stays in one place", strings.Join(offenders, ", "))
	}
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}
