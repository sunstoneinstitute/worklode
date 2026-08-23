//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cmd

import (
	"os/exec"
	"testing"
)

func TestExitCodeRecognizesChildExit(t *testing.T) {
	err := exec.Command("/bin/sh", "-c", "exit 9").Run()
	if code, ok := ExitCode(err); !ok || code != 9 {
		t.Fatalf("ExitCode(%v) = (%d, %t)", err, code, ok)
	}
}

func TestExitCodeMapsSignalsToShellStatus(t *testing.T) {
	for _, test := range []struct {
		name    string
		command string
		want    int
	}{
		{name: "interrupt", command: "kill -INT $$", want: 130},
		{name: "terminate", command: "kill -TERM $$", want: 143},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := exec.Command("/bin/sh", "-c", test.command).Run()
			if code, ok := ExitCode(err); !ok || code != test.want {
				t.Fatalf("ExitCode(%v) = (%d, %t), want %d", err, code, ok, test.want)
			}
		})
	}
}
