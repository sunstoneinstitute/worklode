package cmd

import (
	"fmt"
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

// claimableTaskKinds matches the policy used by the store's ready candidates.
var claimableTaskKinds = ns.TaskKindsMatching(func(d ns.TaskKindDescriptor) bool {
	return d.Claimable
})

// claimKindEnum is the value list both claim surfaces spell out in their
// --kind help. It is ", "-separated because that is the form
// TestAgentSurfaces parses when it checks the --kind values agent docs write.
var claimKindEnum = strings.Join(claimableTaskKinds, ", ")
