package safefetch

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestGuardHasNoExportedFields: an exported field on Fetcher is an SSRF guard
// anyone holding the value can switch off by assignment, which is what the
// AllowLoopbackForTest/AllowAnyHostForTest pair used to be (WL-232). Every
// setting is fixed at construction; a new knob belongs in TestEscapes, not on
// the struct.
func TestGuardHasNoExportedFields(t *testing.T) {
	typ := reflect.TypeOf(Fetcher{})
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() {
			t.Errorf("Fetcher.%s is exported; the guard's settings must be unexported "+
				"so production code cannot relax them by assignment", f.Name)
		}
	}
}

// TestEscapesAreTestOnly: NewForTest panics in a production binary, but a call
// site that reaches it at all is already a mistake worth failing the build on.
// Test files are exempt — being callable from another package's tests is the
// whole reason the constructor is exported.
func TestEscapesAreTestOnly(t *testing.T) {
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
		if filepath.Dir(rel) == filepath.Join("internal", "safefetch") {
			return nil // this package declares it
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // walking this repo's own source
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, "safefetch.NewForTest") {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(offenders) > 0 {
		t.Errorf("safefetch.NewForTest is called from non-test code at %s; "+
			"production must construct fetchers with safefetch.New", strings.Join(offenders, ", "))
	}
}

// TestNewForTestRefusesOutsideTestBinary covers the panic the exported wrapper
// takes when flag.Lookup("test.v") comes back nil, which no test binary can
// reproduce directly.
func TestNewForTestRefusesOutsideTestBinary(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("newForTest returned instead of panicking outside a test binary")
		}
	}()
	newForTest(false, nil, 1<<20, TestEscapes{Loopback: true, AnyHost: true})
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
