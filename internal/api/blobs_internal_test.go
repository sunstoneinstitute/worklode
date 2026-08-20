package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckSpoolWritable covers the probe without a store. The error must name
// the path: it is all an operator gets from the crash loop.
func TestCheckSpoolWritable(t *testing.T) {
	if err := checkSpoolWritable(t.TempDir()); err != nil {
		t.Fatalf("writable dir: %v", err)
	}
	// Empty means os.TempDir(), which is writable in a test environment.
	if err := checkSpoolWritable(""); err != nil {
		t.Fatalf("default dir: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "not-mounted")
	err := checkSpoolWritable(missing)
	if err == nil {
		t.Fatal("no error for a directory that does not exist")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not name %q", err, missing)
	}
}

// TestCheckSpoolWritableLeavesNothingBehind: the probe runs on every boot, so
// a crash-looping pod must not fill its own spool volume.
func TestCheckSpoolWritableLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	for range 3 {
		if err := checkSpoolWritable(dir); err != nil {
			t.Fatalf("probe: %v", err)
		}
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("probe left %v behind", entries)
	}
}
