package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSiblingForwardsStreamsEnvironmentAndExitCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lode-hook")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' \"$FROM_PARENT\"\n/bin/cat\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("FROM_PARENT", "parent-env")
	var out, errOut bytes.Buffer
	err := runSibling(t.Context(), "lode-hook", "user", nil, strings.NewReader("stdin-data"), &out, &errOut)
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("error = %v, want exit 7", err)
	}
	if got := out.String(); got != "parent-envstdin-data" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunSiblingNamesMissingDistribution(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := runSibling(t.Context(), "lode-statusline", "user", nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "lode-statusline") || !strings.Contains(err.Error(), "user distribution") {
		t.Fatalf("error = %v", err)
	}
}
