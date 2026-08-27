package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestPagerArgvDefaultsToLess(t *testing.T) {
	t.Setenv("PAGER", "")
	got := pagerArgv()
	if len(got) != 2 || got[0] != "less" || got[1] != "-R" {
		t.Fatalf("pagerArgv() = %v, want [less -R]", got)
	}
}

func TestPagerArgvUsesPagerEnv(t *testing.T) {
	t.Setenv("PAGER", "moar -x  -f ")
	got := pagerArgv()
	want := []string{"moar", "-x", "-f"}
	if len(got) != len(want) {
		t.Fatalf("pagerArgv() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pagerArgv() = %v, want %v", got, want)
		}
	}
}

func TestPagerArgvBlankPagerFallsBack(t *testing.T) {
	t.Setenv("PAGER", "   ")
	got := pagerArgv()
	if len(got) != 2 || got[0] != "less" {
		t.Fatalf("pagerArgv() = %v, want fallback to less", got)
	}
}

func TestPagerDisabledIsNoop(t *testing.T) {
	w, cleanup := Pager(false)
	if w != nil {
		t.Fatalf("Pager(false) writer = %v, want nil", w)
	}
	cleanup() // must not panic
}

func TestStartPagerPipesContentThrough(t *testing.T) {
	var out bytes.Buffer
	w, cleanup, err := startPager([]string{"cat"}, &out, &out)
	if err != nil {
		t.Fatalf("startPager(cat): %v", err)
	}
	if _, err := w.Write([]byte("hello, pager\n")); err != nil {
		t.Fatalf("write to pager stdin: %v", err)
	}
	cleanup()
	if got := out.String(); got != "hello, pager\n" {
		t.Fatalf("pager output = %q, want %q", got, "hello, pager\n")
	}
}

func TestStartPagerUnknownCommandErrors(t *testing.T) {
	_, _, err := startPager([]string{"lode-pager-that-does-not-exist"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("startPager with an unknown command should return an error")
	}
}

// TestPagerWriterReportsTTYFd pins the writer's contract: Fd() reports the
// real terminal's fd, not anything derived from the underlying io.Writer —
// and it does so before the pager has ever started, since width-fitting
// happens before the first write. It deliberately does not assert that
// terminalFd() treats it as a terminal — term.IsTerminal depends on the real
// fd's actual terminal-ness at test-run time, which under `go test` is not a
// terminal, so that assertion would be flaky.
func TestPagerWriterReportsTTYFd(t *testing.T) {
	var buf bytes.Buffer
	w := &lazyPager{ttyFd: 42, start: func() (io.Writer, func(), error) { return &buf, func() {}, nil }}
	if got := w.Fd(); got != 42 {
		t.Fatalf("lazyPager.Fd() = %d, want 42", got)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := buf.String(); got != "hello" {
		t.Fatalf("underlying buffer = %q, want %q", got, "hello")
	}
}

// WL-331: the pager must not launch for a command that fails before
// producing output. lazyPager defers the exec to the first byte written.
func TestLazyPagerStartsOnlyOnFirstWrite(t *testing.T) {
	var starts int
	var out bytes.Buffer
	stopped := false
	l := &lazyPager{
		ttyFd: 7,
		start: func() (io.Writer, func(), error) {
			starts++
			return &out, func() { stopped = true }, nil
		},
	}

	// No write: cleanup must not have started (or stopped) anything.
	l.cleanup()
	if starts != 0 || stopped {
		t.Fatalf("cleanup without writes: starts=%d stopped=%v, want 0/false", starts, stopped)
	}

	// First write starts the pager exactly once; later writes reuse it.
	if _, err := l.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || out.String() != "ab" {
		t.Fatalf("starts=%d out=%q, want 1/%q", starts, out.String(), "ab")
	}
	if l.Fd() != 7 {
		t.Fatalf("Fd() = %d, want the real terminal's 7", l.Fd())
	}
	l.cleanup()
	if !stopped {
		t.Fatal("cleanup after a write must stop the started pager")
	}
}

// A pager that cannot start falls back to direct printing with one stderr
// note — discovered at first write now, since that is when the exec happens.
func TestLazyPagerFallsBackWhenStartFails(t *testing.T) {
	var fallback, note bytes.Buffer
	l := &lazyPager{
		start:    func() (io.Writer, func(), error) { return nil, nil, errors.New("no less") },
		fallback: &fallback,
		note:     &note,
	}
	for _, s := range []string{"x", "y"} {
		if _, err := l.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if fallback.String() != "xy" {
		t.Fatalf("fallback = %q, want %q", fallback.String(), "xy")
	}
	if n := strings.Count(note.String(), "pager unavailable"); n != 1 {
		t.Fatalf("stderr note printed %d times: %q", n, note.String())
	}
	l.cleanup() // must not panic with nothing started
}
