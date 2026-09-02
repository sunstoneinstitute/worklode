package cmd

import (
	"fmt"

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
