package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// warnDeprecatedKind tells the person that a --kind value has been renamed.
// The server does the normalising (see api.normalizeTaskKind); the CLI only
// warns, and it warns on stderr so --json consumers and anything parsing
// stdout are unaffected.
func warnDeprecatedKind(cmd *cobra.Command, kind string) {
	current, ok := ns.NormalizeTaskKind(kind)
	if !ok {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "warning: task kind %q is deprecated, use %q\n", kind, current)
}
