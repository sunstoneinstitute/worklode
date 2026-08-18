package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// pagerFn is cli.Pager, indirected through a package var so tests can stub
// it out. cli.Pager touches the real os.Stdout and can exec a real external
// process — a unit test must never do either: run from an interactive
// terminal, `go test` would otherwise block on a real `less` waiting for a
// 'q' keypress that never comes.
var pagerFn = cli.Pager

// withPager wires cmd's output through a pager when requested, returning a
// cleanup func the caller must defer. A no-op — cmd is left completely
// unchanged — whenever pagerFn declines to page (see cli.Pager: not
// requested, stdout isn't a terminal, or the pager couldn't start).
func withPager(cmd *cobra.Command, requested bool) func() {
	w, cleanup := pagerFn(requested)
	if w == nil {
		return func() {}
	}
	original := cmd.OutOrStdout()
	cmd.SetOut(w)
	return func() {
		cmd.SetOut(original)
		cleanup()
	}
}
