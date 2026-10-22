package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Where a command can run (WL-REQ-1863). The MCP `lode` tool runs a CLI
// command only when it is remote: it reads and writes only through /api/v1
// and needs nothing from the caller's machine. Every leaf command says which
// it is, in the constructor that defines it, so a new command cannot ship
// without the decision (TestEveryLeafSaysWhereItRuns).
const (
	// annRunWhere is the annotation key every leaf command carries.
	annRunWhere  = "lode.run"
	runRemoteVal = "remote"
	runLocalVal  = "local"

	// annRemoteWhen makes a local command remote for one invocation shape:
	// "flag:<name>" when that flag is set non-empty, "arg:<value>" when the
	// first positional argument equals value.
	annRemoteWhen = "lode.run.remote-when"
)

// Annotation sets for the constructors. cobra only reads them, so one map is
// shared; a command that needs more keys has its own map.
var (
	runRemote = map[string]string{annRunWhere: runRemoteVal}
	runLocal  = map[string]string{annRunWhere: runLocalVal}
	// runLocalUnlessWorktree: `task claim` reads the current worktree to name
	// the lease's session key, unless --worktree names it.
	runLocalUnlessWorktree = map[string]string{annRunWhere: runLocalVal, annRemoteWhen: "flag:worktree"}
	// runLocalUnlessStdin: `doc lint -` lints stdin; a file path is read from
	// the caller's disk.
	runLocalUnlessStdin = map[string]string{annRunWhere: runLocalVal, annRemoteWhen: "arg:-"}
)

// annotateCobraGenerated marks the commands cobra builds itself, which have no
// constructor here. `completion <shell>` prints a script for the caller's
// shell to install, so it is local; `help` only reads the command tree, so it
// is remote. (cobra adds `help` lazily, so it is annotated only when present.)
func annotateCobraGenerated(root *cobra.Command) {
	for _, c := range root.Commands() {
		switch c.Name() {
		case "completion":
			for _, sh := range c.Commands() {
				sh.Annotations = runLocal
			}
		case "help":
			c.Annotations = runRemote
		}
	}
}

// LocalCommandError is returned when a command that needs the caller's
// machine is asked to run somewhere else (the MCP lode tool).
type LocalCommandError struct {
	// Path is the command path without the root, e.g. "work next".
	Path string
}

func (e *LocalCommandError) Error() string {
	return fmt.Sprintf("refused: lode %s needs the lode binary on the caller's machine", e.Path)
}

// remoteRefusal returns nil when c, parsed with args, may run remotely, and a
// *LocalCommandError naming the command when it may not. Call it after flag
// parsing: `task claim` depends on --worktree and `doc lint` on its argument.
func remoteRefusal(c *cobra.Command, args []string) error {
	if c.Annotations[annRunWhere] == runRemoteVal {
		return nil
	}
	when := c.Annotations[annRemoteWhen]
	switch {
	case strings.HasPrefix(when, "flag:"):
		if f := c.Flags().Lookup(strings.TrimPrefix(when, "flag:")); f != nil && f.Value.String() != "" {
			return nil
		}
	case strings.HasPrefix(when, "arg:"):
		if len(args) > 0 && args[0] == strings.TrimPrefix(when, "arg:") {
			return nil
		}
	}
	_, path, _ := strings.Cut(c.CommandPath(), " ")
	return &LocalCommandError{Path: path}
}
