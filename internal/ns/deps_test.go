package ns_test

import (
	"os/exec"
	"strings"
	"testing"
)

const nsPkg = "github.com/sunstoneinstitute/worklode/internal/ns"

// TestNSImportsStdlibOnly keeps the task kinds, their descriptors and the
// other generated enums readable from any package, hot paths included.
func TestNSImportsStdlibOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", nsPkg).Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == nsPkg {
			continue
		}
		// A stdlib import path has no dot in its first element.
		first, _, _ := strings.Cut(dep, "/")
		if strings.Contains(first, ".") {
			t.Errorf("internal/ns must import stdlib only, got %s", dep)
		}
	}
}
