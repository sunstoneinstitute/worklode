package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// warnDeprecatedTaskKind tells the person that a --kind value has been
// renamed. Named for the TASK kind specifically: "spec" is deprecated as a
// task kind, but `lode doc add --kind spec` and `lode doc list --kind spec`
// are valid document kinds, so a helper named for "kind" generally would be
// a trap. The server does the normalising (see api.normalizeTaskKind); the
// CLI only warns, and it warns on stderr so --json consumers and anything
// parsing stdout are unaffected.
func warnDeprecatedTaskKind(cmd *cobra.Command, kind string) {
	current, ok := ns.NormalizeTaskKind(kind)
	if !ok {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "warning: task kind %q is deprecated, use %q\n", kind, current)
}

// warnDeprecatedTaskKinds warns per element of a --kind list, so a renamed
// kind buried in `--kind design,spec` is still called out.
func warnDeprecatedTaskKinds(cmd *cobra.Command, kinds []string) {
	for _, k := range kinds {
		warnDeprecatedTaskKind(cmd, k)
	}
}

// claimableTaskKinds are the kinds a ranked claim can actually hand out:
// ns.TaskKinds minus the two that store.readyCandidates filters out of the
// ready set. A decision and a rally have nothing to check out, so offering
// them in a claim surface's --kind help would name a value that never
// matches.
var claimableTaskKinds = slices.DeleteFunc(slices.Clone(ns.TaskKinds), func(k string) bool {
	return k == "decision" || k == "rally"
})

// claimKindEnum is the value list both claim surfaces spell out in their
// --kind help. It is ", "-separated because that is the form
// TestAgentSurfaces parses when it checks the --kind values agent docs write.
var claimKindEnum = strings.Join(claimableTaskKinds, ", ")
