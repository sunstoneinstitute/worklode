package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestLegacyStatuslineForwardsArgumentsAndStderr(t *testing.T) {
	bin := buildLodeBinary(t)
	dir := t.TempDir()
	child := filepath.Join(dir, "lode-statusline")
	if err := os.WriteFile(child, []byte("#!/bin/sh\nprintf '%s' \"$*\"\nprintf child-stderr >&2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bin, "statusline", "first", "second")
	command.Env = append(os.Environ(), "PATH="+dir)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "first second" || !strings.Contains(stderr.String(), "child-stderr") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestLegacyHookHelpForwardsToDirectBinary(t *testing.T) {
	bin := buildLodeBinary(t)
	command := exec.Command(bin, "hook", "--help")
	command.Env = append(os.Environ(), "PATH="+filepath.Dir(bin))
	out, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "lode-hook") {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestInterruptCancelsLongRunningSiblingWithSignalExit(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("shell fixture")
	}
	bin := buildLodeBinary(t)
	dir := t.TempDir()
	child := filepath.Join(dir, "lode-statusline")
	if err := os.WriteFile(child, []byte("#!/bin/sh\n/bin/sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bin, "statusline")
	command.Env = append(os.Environ(), "PATH="+dir)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := command.Wait()
	if code := exitStatus(err); code != 130 {
		t.Fatalf("exit code = %d, want 130 (%v)", code, err)
	}
}

func exitStatus(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 0
}

func TestRunSiblingNamesMissingDistribution(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := runSibling(t.Context(), "lode-statusline", "user", nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "lode-statusline") || !strings.Contains(err.Error(), "user distribution") {
		t.Fatalf("error = %v", err)
	}
}
