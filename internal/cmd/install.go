package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// The integrations `lode install`/`lode uninstall` know how to manage. Both
// flags take a name rather than being booleans so a second VCS or agent can be
// added later without changing the CLI shape.
const (
	vcsGit          = "git"
	agentClaudeCode = "claude-code"
)

// hookTargets is the pair of integrations one install or uninstall run acts on.
// An empty field means "skip this one".
type hookTargets struct {
	vcs   string
	agent string
}

// addHookFlags declares the flags shared by `lode install` and `lode uninstall`.
func addHookFlags(cmd *cobra.Command) {
	cmd.Flags().String("vcs", vcsGit, "version control system whose hooks to manage")
	cmd.Flags().String("agent", agentClaudeCode, "coding agent whose hooks to manage")
	cmd.Flags().Bool("no-vcs", false, "skip the version control system hooks")
	cmd.Flags().Bool("no-agent", false, "skip the coding agent hooks")
	cmd.Flags().String("scope", scopeLocal,
		"which agent settings file to write: local (settings.local.json) or project (settings.json)")
}

// resolveHookTargets turns the parsed flags into the set of integrations to act
// on. Naming an integration and opting out of it in the same run is a
// contradiction rather than a precedence question, so it is rejected instead of
// silently picking a winner.
func resolveHookTargets(cmd *cobra.Command) (hookTargets, error) {
	flags := cmd.Flags()
	vcs, _ := flags.GetString("vcs")
	agent, _ := flags.GetString("agent")
	noVCS, _ := flags.GetBool("no-vcs")
	noAgent, _ := flags.GetBool("no-agent")

	switch {
	case noVCS && flags.Changed("vcs"):
		return hookTargets{}, errors.New("--vcs and --no-vcs are mutually exclusive")
	case noAgent && flags.Changed("agent"):
		return hookTargets{}, errors.New("--agent and --no-agent are mutually exclusive")
	}

	if noVCS {
		vcs = ""
	} else if vcs != vcsGit {
		return hookTargets{}, fmt.Errorf("unsupported --vcs %q (supported: %s)", vcs, vcsGit)
	}
	if noAgent {
		agent = ""
	} else if agent != agentClaudeCode {
		return hookTargets{}, fmt.Errorf("unsupported --agent %q (supported: %s)", agent, agentClaudeCode)
	}
	if vcs == "" && agent == "" {
		return hookTargets{}, errors.New("nothing to do: --no-vcs and --no-agent were both given")
	}
	return hookTargets{vcs: vcs, agent: agent}, nil
}
