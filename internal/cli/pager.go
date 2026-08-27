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
// Callers must treat a nil writer as "use my existing output unchanged."
//
// The external pager is exec'd lazily, on the first byte written, so a
// command that fails before producing any output prints its error and exits
// without ever launching an empty pager (WL-331). A pager that turns out to
// be unstartable at that point (nothing in $PAGER or "less" resolves on
// $PATH) prints one note to stderr and the writer falls back to printing
// directly rather than losing the output.
func Pager(enabled bool) (io.Writer, func()) {
	if !enabled {
		return nil, func() {}
	}
	fd, isTTY := terminalFd(os.Stdout)
	if !isTTY {
		return nil, func() {}
	}
	l := &lazyPager{
		ttyFd:    uintptr(fd),
		start:    func() (io.Writer, func(), error) { return startPager(pagerArgv(), os.Stdout, os.Stderr) },
		fallback: os.Stdout,
		note:     os.Stderr,
	}
	return l, l.cleanup
}

// lazyPager defers exec'ing the external pager to the first byte written:
// an error path that renders nothing never launches a pager with nothing to
// show (WL-331). It also plays the old pagerWriter's role — Fd() reports the
// real terminal's fd rather than the pipe's, so callers that TTY-detect
// their writer (terminalFd, used by Markdown and the table/render
// width-fitting) style and word-wrap exactly as they would writing directly
// to that terminal.
type lazyPager struct {
	ttyFd    uintptr
	start    func() (io.Writer, func(), error)
	fallback io.Writer // where writes land when the pager cannot start
	note     io.Writer // stderr, for the one pager-unavailable note

	started bool
	w       io.Writer
	stop    func()
}

func (l *lazyPager) Write(p []byte) (int, error) {
	if !l.started {
		l.started = true
		w, stop, err := l.start()
		if err != nil {
			// Discovered at first write now, since that is when the exec
			// happens; same fallback the eager version had at setup time.
			fmt.Fprintf(l.note, "lode: pager unavailable (%v); printing directly\n", err)
			l.w = l.fallback
		} else {
			l.w, l.stop = w, stop
		}
	}
	return l.w.Write(p)
}

func (l *lazyPager) Fd() uintptr { return l.ttyFd }

// cleanup stops the pager if one was ever started; with no writes it is a
// no-op, which is the whole point.
func (l *lazyPager) cleanup() {
	if l.stop != nil {
		l.stop()
		l.stop = nil
	}
}

// pagerArgv is the pager command line: $PAGER's fields when set to a
// non-blank value, else "less -R". Content reaching the pager's stdin is
// already ANSI-styled by cli.Markdown's rendering (the lazy writer makes
// the writer report the real terminal's fd, so Markdown styles and
// word-wraps exactly as it would writing directly to that terminal); -R lets
// less pass those escape codes through to the terminal instead of showing
// them literally or stripping them.
func pagerArgv() []string {
	if p := strings.TrimSpace(os.Getenv("PAGER")); p != "" {
		return strings.Fields(p)
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
