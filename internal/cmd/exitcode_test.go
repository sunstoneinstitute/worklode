package cmd

import (
	"errors"
	"os/exec"
	"testing"
)

func TestExitCodeRecognizesChildExit(t *testing.T) {
	err := exec.Command("/bin/sh", "-c", "exit 9").Run()
	if code, ok := ExitCode(err); !ok || code != 9 {
		t.Fatalf("ExitCode(%v) = (%d, %t)", err, code, ok)
	}
	if _, ok := ExitCode(errors.New("not a child")); ok {
		t.Fatal("non-child error recognized")
	}
}
