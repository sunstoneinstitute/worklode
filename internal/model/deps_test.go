package model_test

import (
	"os/exec"
	"strings"
	"testing"
)

// modelPkg is the import path under test. "go list -deps" always includes
// the target package itself in its output, so it must be skipped below —
// otherwise the check trips on its own import path, which of course
// contains a dot, and the guard could never pass.
const modelPkg = "github.com/sunstoneinstitute/worklode/internal/model"

// TestModelImportsStdlibOnly enforces ADR 036 §4: internal/model is a leaf
// every layer can depend on, so it may not reach back into the module or
// pull in a third-party package.
func TestModelImportsStdlibOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", modelPkg).Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == modelPkg {
			continue
		}
		// A stdlib import path has no dot in its first element.
		first, _, _ := strings.Cut(dep, "/")
		if strings.Contains(first, ".") {
			t.Errorf("internal/model must import stdlib only, got %s", dep)
		}
	}
}
