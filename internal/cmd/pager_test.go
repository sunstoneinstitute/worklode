package cmd

import (
	"bytes"
	"io"
	"strings"
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

	// cmd inherits its writer from parent, the way `show` inherits from
	// rootCmd in the real command tree — never set explicitly on cmd
	// itself. That's what lets a nil-clearing cleanup restore inheritance
	// rather than pin a resolved snapshot (see withPager's cleanup comment).
	var original bytes.Buffer
	parent := &cobra.Command{}
	parent.SetOut(&original)
	cmd := &cobra.Command{}
	parent.AddCommand(cmd)

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
		t.Fatal("withPager did not restore inheritance from parent's output after cleanup")
	}
	if !called {
		t.Fatal("withPager's cleanup did not call the underlying pagerFn cleanup")
	}
}

// TestWithPagerCleanupDoesNotPinStaleWriter reproduces the bug found during
// Task 3 integration testing: reusing the same *cobra.Command across
// sequential withPager calls (as runLode's long-lived rootCmd/show tree
// does across many tests in one process) must not leave a stale writer
// pinned on cmd after cleanup. A cleanup that restored a resolved
// OutOrStdout() snapshot instead of clearing to nil would pin the first
// call's writer permanently, silently breaking every later
// cmd.SetOut(...) on an ancestor.
func TestWithPagerCleanupDoesNotPinStaleWriter(t *testing.T) {
	old := pagerFn
	var pagerBuf bytes.Buffer
	pagerFn = func(bool) (io.Writer, func()) { return &pagerBuf, func() {} }
	t.Cleanup(func() { pagerFn = old })

	var first bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&first)

	cleanup := withPager(cmd, true)
	cleanup()

	// Simulate the next test in the same process reusing cmd, the way
	// runLode does with the package-level rootCmd/show tree.
	var second bytes.Buffer
	cmd.SetOut(&second)

	if got := cmd.OutOrStdout(); got != io.Writer(&second) {
		t.Fatalf("withPager's cleanup left a stale writer pinned on cmd: OutOrStdout() = %v, want the second buffer", got)
	}
}

func TestShowPagerFlagWiresOutputThroughPager(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Paged output")
	setupRepoConfig(t, "proj")

	old := pagerFn
	var pagerBuf bytes.Buffer
	pagerFn = func(requested bool) (io.Writer, func()) {
		if !requested {
			t.Fatal("pagerFn called with requested=false; --pager was passed")
		}
		return &pagerBuf, func() {}
	}
	t.Cleanup(func() { pagerFn = old })

	out, err := runLode(t, "show", task.ID, "--pager")
	if err != nil {
		t.Fatalf("lode show %s --pager: %v\noutput: %s", task.ID, err, out)
	}
	if out != "" {
		t.Fatalf("stdout should be empty once paged, got %q", out)
	}
	if !strings.Contains(pagerBuf.String(), task.ID) {
		t.Fatalf("pager buffer = %q; want it to contain the task id", pagerBuf.String())
	}
}

func TestShowPagerFlagDeclinedLeavesOutputOnStdout(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Unpaged output")
	setupRepoConfig(t, "proj")

	old := pagerFn
	pagerFn = func(bool) (io.Writer, func()) { return nil, func() {} }
	t.Cleanup(func() { pagerFn = old })

	out, err := runLode(t, "show", task.ID, "--pager")
	if err != nil {
		t.Fatalf("lode show %s --pager: %v\noutput: %s", task.ID, err, out)
	}
	if !strings.Contains(out, task.ID) {
		t.Fatalf("stdout = %q; want it to contain the task id when pagerFn declines", out)
	}
}
