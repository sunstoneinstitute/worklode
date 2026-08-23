package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// lodeBinary caches the built CLI for the whole package run. Go caches
// compilation but never the link step, so each build costs seconds — and
// every test that needs it only execs the binary, so one copy serves them all.
var lodeBinary struct {
	once sync.Once
	path string
	err  error
	out  []byte
}

// buildLodeBinary builds the lode CLI (cmd/lode) once per package run and
// returns the path to the binary. Debug symbols are stripped: nothing here
// debugs the child, and stripping cuts the link time several-fold.
func buildLodeBinary(t *testing.T) string {
	t.Helper()
	lodeBinary.once.Do(func() {
		dir, err := os.MkdirTemp("", "lode-bin")
		if err != nil {
			lodeBinary.err = err
			return
		}
		suffix := ""
		if runtime.GOOS == "windows" {
			suffix = ".exe"
		}
		bin := filepath.Join(dir, "lode"+suffix)
		build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin, "./cmd/lode")
		build.Dir = store.ModuleRootForTests()
		lodeBinary.out, lodeBinary.err = build.CombinedOutput()
		if lodeBinary.err == nil {
			build = exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", filepath.Join(dir, "lode-hook"+suffix), "./cmd/lode-hook")
			build.Dir = store.ModuleRootForTests()
			lodeBinary.out, lodeBinary.err = build.CombinedOutput()
		}
		if lodeBinary.err == nil {
			lodeBinary.path = bin
		}
	})
	if lodeBinary.err != nil {
		t.Fatalf("go build lode: %v\n%s", lodeBinary.err, lodeBinary.out)
	}
	return lodeBinary.path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode()
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// chainFor returns what the named hook chains to in an install result,
// failing the test if the hook is missing from it.
func chainFor(t *testing.T, chains []githooks.Chain, hook string) string {
	t.Helper()
	for _, c := range chains {
		if c.Hook == hook {
			return c.ChainedTo
		}
	}
	t.Fatalf("no %s in install result %+v", hook, chains)
	return ""
}

// actionFor is chainFor for an uninstall result.
func actionFor(t *testing.T, removals []githooks.Removal, hook string) string {
	t.Helper()
	for _, r := range removals {
		if r.Hook == hook {
			return r.Action
		}
	}
	t.Fatalf("no %s in uninstall result %+v", hook, removals)
	return ""
}

// initGitRepoInDir inits a git repo with one commit at the given dir and
// returns its path resolved to git's own toplevel (macOS /var symlink). Unlike
// initGitRepo it lets the caller choose the directory, so a test can put the
// repo under a path containing a space.
func initGitRepoInDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run := func(args ...string) {
		t.Helper()
		// commit.gpgsign=false: the developer's global config may enable
		// signing, which a temp-repo test commit must not depend on.
		c := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("rev-parse toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}
