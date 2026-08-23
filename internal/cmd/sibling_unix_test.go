//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

func TestLegacyStatuslineForwardsSignals(t *testing.T) {
	for _, test := range []struct {
		name   string
		signal syscall.Signal
		status int
		mark   string
	}{
		{name: "interrupt", signal: syscall.SIGINT, status: 130, mark: "INT"},
		{name: "terminate", signal: syscall.SIGTERM, status: 143, mark: "TERM"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bin := buildLodeBinary(t)
			dir := t.TempDir()
			ready := filepath.Join(dir, "ready")
			received := filepath.Join(dir, "received")
			child := filepath.Join(dir, "lode-statusline")
			script := "#!/bin/sh\n" +
				"trap 'printf INT > \"$RECEIVED\"; trap - INT; kill -INT $$' INT\n" +
				"trap 'printf TERM > \"$RECEIVED\"; trap - TERM; kill -TERM $$' TERM\n" +
				": > \"$READY\"\n" +
				"while :; do /bin/sleep 1; done\n"
			if err := os.WriteFile(child, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(bin, "statusline")
			command.Env = append(os.Environ(), "PATH="+dir, "READY="+ready, "RECEIVED="+received)
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			waitForFile(t, ready)
			if err := command.Process.Signal(test.signal); err != nil {
				t.Fatal(err)
			}
			err := command.Wait()
			if code := exitStatus(err); code != test.status {
				t.Fatalf("exit code = %d, want %d (%v)", code, test.status, err)
			}
			if got, err := os.ReadFile(received); err != nil || string(got) != test.mark {
				t.Fatalf("forwarded signal = %q, %v; want %s", got, err, test.mark)
			}
		})
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func exitStatus(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 0
}
