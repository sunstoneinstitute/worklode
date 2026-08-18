package cli

import (
	"bytes"
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

// TestPagerWriterReportsTTYFd pins pagerWriter's contract: Fd() reports the
// wrapped fd (the real terminal's, in production), not anything derived from
// the underlying io.Writer. It deliberately does not assert that terminalFd()
// treats a pagerWriter as a terminal — term.IsTerminal depends on the real
// fd's actual terminal-ness at test-run time, which under `go test` is not a
// terminal, so that assertion would be flaky.
func TestPagerWriterReportsTTYFd(t *testing.T) {
	var buf bytes.Buffer
	w := pagerWriter{Writer: &buf, ttyFd: 42}
	if got := w.Fd(); got != 42 {
		t.Fatalf("pagerWriter.Fd() = %d, want 42", got)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := buf.String(); got != "hello" {
		t.Fatalf("underlying buffer = %q, want %q", got, "hello")
	}
}
