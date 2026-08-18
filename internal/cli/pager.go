package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Pager starts an external pager when enabled and os.Stdout is an
// interactive terminal, and returns the writer command output should be
// sent to plus a cleanup func to defer once rendering finishes.
//
// Off-TTY (piped, redirected, or otherwise non-interactive) --pager is a
// silent no-op: piping rendered content into `less` when nothing is
// watching the terminal makes no sense, and scripts must never end up
// blocking on a pager waiting for a keypress that will never come.
//
// If stdout IS a terminal but the chosen pager can't actually be started
// (nothing in $PAGER or "less" resolves on $PATH), Pager prints one note to
// stderr and returns (nil, no-op) so the caller falls back to printing
// directly rather than losing the output.
//
// Callers must treat a nil writer as "use my existing output unchanged."
func Pager(enabled bool) (io.Writer, func()) {
	if !enabled {
		return nil, func() {}
	}
	if _, isTTY := terminalFd(os.Stdout); !isTTY {
		return nil, func() {}
	}
	w, cleanup, err := startPager(pagerArgv(), os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lode: pager unavailable (%v); printing directly\n", err)
		return nil, func() {}
	}
	return w, cleanup
}

// pagerArgv is the pager command line: $PAGER's fields when set to a
// non-blank value, else "less -R". -R lets less pass through the ANSI color
// codes cli.Markdown's glamour rendering already emitted, instead of
// showing them as literal escape sequences.
func pagerArgv() []string {
	if p := strings.TrimSpace(os.Getenv("PAGER")); p != "" {
		if fields := strings.Fields(p); len(fields) > 0 {
			return fields
		}
	}
	return []string{"less", "-R"}
}

// startPager execs argv, wiring its stdin to the writer it returns and its
// stdout/stderr to pagerOut/pagerErr. Split out of Pager so tests can drive
// the process plumbing with a stand-in command (e.g. "cat") instead of a
// real pager and a real terminal.
//
// The returned writer is the child's stdin pipe directly (proc.StdinPipe),
// not an io.Pipe wrapping it: an io.Pipe's Write blocks until something
// reads the other end, so if the pager process exits early (the user
// presses 'q' before rendering finishes) a caller still writing to an
// io.Pipe would hang forever with no reader left. proc.StdinPipe is a real
// OS pipe with its own kernel buffer, so a write after the reader is gone
// instead returns EPIPE — and every caller in this codebase already
// discards Write's error return (fmt.Fprintf et al.), so that failure is
// silently swallowed exactly like every other write in these commands.
func startPager(argv []string, pagerOut, pagerErr io.Writer) (io.Writer, func(), error) {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, nil, err
	}
	proc := exec.Command(path, argv[1:]...)
	proc.Stdout = pagerOut
	proc.Stderr = pagerErr
	stdin, err := proc.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := proc.Start(); err != nil {
		return nil, nil, err
	}
	return stdin, func() {
		stdin.Close()
		_ = proc.Wait()
	}, nil
}
