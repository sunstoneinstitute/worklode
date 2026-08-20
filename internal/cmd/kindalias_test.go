package cmd

import (
	"strings"
	"testing"
)

// TestTaskAddWarnsOnDeprecatedKind proves `lode task add --kind spec` still
// succeeds (the server normalises it to design), prints the exact
// deprecation warning on stderr, and prints nothing about it on stdout.
func TestTaskAddWarnsOnDeprecatedKind(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	stdout, stderr, err := runLodeOutErr(t, "task", "add", "--project", "proj",
		"--title", "Spec task", "--kind", "spec")
	if err != nil {
		t.Fatalf("task add --kind spec: %v\nstderr: %s", err, stderr)
	}
	const want = `warning: task kind "spec" is deprecated, use "design"` + "\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if strings.Contains(stdout, "deprecated") {
		t.Errorf("stdout unexpectedly mentions the deprecation warning: %q", stdout)
	}
}

// TestTaskAddNoWarningOnCurrentKind proves the current kind name prints no
// warning on either stream.
func TestTaskAddNoWarningOnCurrentKind(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	_, stderr, err := runLodeOutErr(t, "task", "add", "--project", "proj",
		"--title", "Design task", "--kind", "design")
	if err != nil {
		t.Fatalf("task add --kind design: %v\nstderr: %s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}
