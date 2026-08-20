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

// packageDir is internal/cmd itself, captured before runTests moves the
// process out of it. Tests that read a fixture under testdata/ join onto this
// rather than a relative path, which the chdir below would break.
var packageDir string

// isolationHome is the temp HOME runTests installs. Kept in a package var so
// TestIsolationHomeHoldsNoGoCache can assert against the HOME the package was
// started with, not whatever HOME an individual test has scoped with
// t.Setenv.
var isolationHome string

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "lode-cmd-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create isolation dir: %v\n", err)
		return 1
	}
	// Reported, not ignored: a silent failure here leaks the whole isolation
	// tree, and the symptom surfaces runs later as an unrelated test failing
	// to write a temp file.
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "remove isolation dir %s: %v\n", dir, err)
		}
	}()

	home, wd := filepath.Join(dir, "home"), filepath.Join(dir, "wd")
	for _, d := range []string{home, wd} {
		if err := os.Mkdir(d, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "create %s: %v\n", d, err)
			return 1
		}
	}
	// Both Go caches default to a path under HOME, so the temp HOME below
	// would give buildLodeBinary's `go build` empty ones: GOCACHE recompiles
	// the stdlib on every run (~25s), and GOMODCACHE re-downloads every
	// module into the temp dir (~18k files). Worse, the module cache is
	// written read-only, so the RemoveAll above cannot delete it — the dir
	// survives the run and leaks its inodes, which on a CI runner with a
	// tmpfs /tmp exhausts nr_inodes after a few dozen runs and fails
	// unrelated tests with "no space left on device". Pin the real caches
	// before HOME moves.
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		if os.Getenv(key) != "" {
			continue
		}
		out, err := exec.Command("go", "env", key).Output()
		if err != nil {
			continue
		}
		cache := strings.TrimSpace(string(out))
		if cache == "" {
			continue
		}
		if err := os.Setenv(key, cache); err != nil {
			fmt.Fprintf(os.Stderr, "set %s: %v\n", key, err)
			return 1
		}
	}
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintf(os.Stderr, "set HOME: %v\n", err)
		return 1
	}
	isolationHome = home
	if packageDir, err = os.Getwd(); err != nil {
		fmt.Fprintf(os.Stderr, "read the package directory: %v\n", err)
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

// TestIsolationHomeHoldsNoGoCache guards the cache pinning above. Unpinning
// either cache breaks no assertion on its own — the tests still pass, just
// slower — while `go build` quietly fills the temp HOME with a module cache
// it writes read-only and the deferred RemoveAll then cannot delete. That is
// invisible locally and fatal on the self-hosted runner, whose tmpfs /tmp is
// capped by inode count: ~18k inodes leaked per run exhausted it after a few
// dozen runs and failed unrelated tests with "no space left on device"
// (WL-190). So assert the invariant directly, after a real build has had its
// chance to write.
func TestIsolationHomeHoldsNoGoCache(t *testing.T) {
	if isolationHome == "" {
		t.Fatal("runTests did not record the isolation HOME")
	}
	// The child `go build` resolves these from the environment it inherits,
	// so ask the toolchain rather than reading the variables: an empty pin
	// resolves back under HOME and must fail here too.
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		out, err := exec.Command("go", "env", key).Output()
		if err != nil {
			t.Fatalf("go env %s: %v", key, err)
		}
		cache := strings.TrimSpace(string(out))
		if cache == "" {
			t.Fatalf("%s resolves to nothing; runTests must pin it before HOME moves", key)
		}
		if under(cache, isolationHome) {
			t.Fatalf("%s is %s, inside the isolation HOME %s: pin it to the real cache before HOME moves", key, cache, isolationHome)
		}
	}
	// buildLodeBinary is what actually writes; run it before looking.
	buildLodeBinary(t)
	for _, rel := range []string{filepath.Join("go", "pkg", "mod"), filepath.Join(".cache", "go-build")} {
		if _, err := os.Stat(filepath.Join(isolationHome, rel)); err == nil {
			t.Errorf("the build populated %s inside the isolation HOME", rel)
		}
	}
}

// under reports whether path is dir or sits beneath it.
func under(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
