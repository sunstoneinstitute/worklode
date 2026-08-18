package cmd

import (
	"bytes"
	"io"
	"testing"

	"github.com/spf13/cobra"
)

func TestWithPagerNoopWhenDeclined(t *testing.T) {
	old := pagerFn
	pagerFn = func(bool) (io.Writer, func()) { return nil, func() {} }
	t.Cleanup(func() { pagerFn = old })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	cleanup := withPager(cmd, true)
	defer cleanup()

	if cmd.OutOrStdout() != io.Writer(&buf) {
		t.Fatal("withPager swapped cmd's output when pagerFn declined")
	}
}

func TestWithPagerSwapsAndRestoresOutput(t *testing.T) {
	old := pagerFn
	var pagerBuf bytes.Buffer
	called := false
	pagerFn = func(requested bool) (io.Writer, func()) {
		if !requested {
			t.Fatal("pagerFn called with requested=false")
		}
		return &pagerBuf, func() { called = true }
	}
	t.Cleanup(func() { pagerFn = old })

	var original bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&original)

	cleanup := withPager(cmd, true)
	if _, err := cmd.OutOrStdout().Write([]byte("paged\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	cleanup()

	if pagerBuf.String() != "paged\n" {
		t.Fatalf("pager buffer = %q, want %q", pagerBuf.String(), "paged\n")
	}
	if original.Len() != 0 {
		t.Fatalf("original buffer should be untouched, got %q", original.String())
	}
	if cmd.OutOrStdout() != io.Writer(&original) {
		t.Fatal("withPager did not restore the original output after cleanup")
	}
	if !called {
		t.Fatal("withPager's cleanup did not call the underlying pagerFn cleanup")
	}
}
