package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

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

	if noVCS && flags.Changed("vcs") {
		return hookTargets{}, errors.New("--vcs and --no-vcs are mutually exclusive")
	}
	if noAgent && flags.Changed("agent") {
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

// installResult is what one `lode install` run did. A nil field means that
// integration was skipped, and is omitted from the JSON entirely.
type installResult struct {
	VCS   *vcsInstall   `json:"vcs,omitempty"`
	Agent *agentInstall `json:"agent,omitempty"`
}

type vcsInstall struct {
	VCS       string `json:"vcs"`
	HooksDir  string `json:"hooks_dir"`
	ChainedTo string `json:"chained_to"`
}

type agentInstall struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
}

// uninstallResult is the same, for `lode uninstall`.
type uninstallResult struct {
	VCS   *vcsUninstall   `json:"vcs,omitempty"`
	Agent *agentUninstall `json:"agent,omitempty"`
}

type vcsUninstall struct {
	VCS      string `json:"vcs"`
	HooksDir string `json:"hooks_dir"`
	Action   string `json:"action"`
}

type agentUninstall struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
}

// installHooks installs every selected integration for the repo containing dir.
func installHooks(dir string, targets hookTargets, scope string) (installResult, error) {
	var res installResult
	if targets.vcs != "" {
		hooksDir, chainedTo, err := installGitHooks(dir)
		if err != nil {
			return installResult{}, err
		}
		res.VCS = &vcsInstall{VCS: targets.vcs, HooksDir: hooksDir, ChainedTo: chainedTo}
	}
	if targets.agent != "" {
		path, err := settingsPathForScope(dir, scope)
		if err != nil {
			return installResult{}, err
		}
		if err := installClaudeHooks(path); err != nil {
			return installResult{}, err
		}
		res.Agent = &agentInstall{Agent: targets.agent, Path: path}
	}
	return res, nil
}

// uninstallHooks removes every selected integration from the repo containing dir.
func uninstallHooks(dir string, targets hookTargets, scope string) (uninstallResult, error) {
	var res uninstallResult
	if targets.vcs != "" {
		hooksDir, action, err := uninstallGitHooks(dir)
		if err != nil {
			return uninstallResult{}, err
		}
		res.VCS = &vcsUninstall{VCS: targets.vcs, HooksDir: hooksDir, Action: action}
	}
	if targets.agent != "" {
		path, err := settingsPathForScope(dir, scope)
		if err != nil {
			return uninstallResult{}, err
		}
		if err := uninstallClaudeHooks(path); err != nil {
			return uninstallResult{}, err
		}
		res.Agent = &agentUninstall{Agent: targets.agent, Path: path}
	}
	return res, nil
}

func newInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Worklode's hooks for this repo's VCS and coding agent",
		Long: "Installs two integrations. The VCS side writes a pre-commit hook (into the repo's " +
			"shared hooks directory, honoring core.hooksPath) that invokes `lode hook pre-commit`, " +
			"chaining any pre-commit hook already present or the pre-commit framework. The agent " +
			"side writes Worklode's lifecycle hook bindings (session start/end, heartbeat, worktree " +
			"enter) into the repo's Claude Code settings file. Use --no-vcs or --no-agent to skip " +
			"either. Safe to re-run: both converge rather than accumulate.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := resolveHookTargets(cmd)
			if err != nil {
				return err
			}
			scope, _ := cmd.Flags().GetString("scope")
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determine working directory: %w", err)
			}
			res, err := installHooks(cwd, targets, scope)
			if err != nil {
				return err
			}
			return reportInstall(cmd, res)
		},
	}
	addHookFlags(cmd)
	return cmd
}

func newUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Worklode's hooks from this repo's VCS and coding agent",
		Long: "Removes what `lode install` added. The VCS side removes Worklode's pre-commit hook " +
			"and restores whatever it preserved, leaving a third-party pre-commit hook it does not " +
			"recognize untouched. The agent side removes every `lode hook` binding from the repo's " +
			"Claude Code settings file, leaving all other settings — including third-party hooks on " +
			"the same events — in place. Use --no-vcs or --no-agent to skip either. A repo with " +
			"nothing installed is not an error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := resolveHookTargets(cmd)
			if err != nil {
				return err
			}
			scope, _ := cmd.Flags().GetString("scope")
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determine working directory: %w", err)
			}
			res, err := uninstallHooks(cwd, targets, scope)
			if err != nil {
				return err
			}
			return reportUninstall(cmd, res)
		},
	}
	addHookFlags(cmd)
	return cmd
}

// reportInstall prints one line per integration, or the whole result as JSON.
func reportInstall(cmd *cobra.Command, res installResult) error {
	if jsonOut(cmd) {
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		printRaw(cmd, b)
		return nil
	}
	out := cmd.OutOrStdout()
	if res.VCS != nil {
		fmt.Fprintf(out, "%s: installed pre-commit hook in %s\n", res.VCS.VCS, res.VCS.HooksDir)
		if res.VCS.ChainedTo != "" {
			fmt.Fprintf(out, "%s: chains to %s\n", res.VCS.VCS, res.VCS.ChainedTo)
		}
	}
	if res.Agent != nil {
		fmt.Fprintf(out, "%s: installed hooks in %s\n", res.Agent.Agent, res.Agent.Path)
	}
	return nil
}

// reportUninstall is reportInstall for the removal side.
func reportUninstall(cmd *cobra.Command, res uninstallResult) error {
	if jsonOut(cmd) {
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		printRaw(cmd, b)
		return nil
	}
	out := cmd.OutOrStdout()
	if res.VCS != nil {
		switch res.VCS.Action {
		case hookActionRestored:
			fmt.Fprintf(out, "%s: removed pre-commit hook from %s and restored the previous one\n",
				res.VCS.VCS, res.VCS.HooksDir)
		case hookActionRemoved:
			fmt.Fprintf(out, "%s: removed pre-commit hook from %s\n", res.VCS.VCS, res.VCS.HooksDir)
		default:
			fmt.Fprintf(out, "%s: no Worklode pre-commit hook in %s\n", res.VCS.VCS, res.VCS.HooksDir)
		}
	}
	if res.Agent != nil {
		fmt.Fprintf(out, "%s: removed hooks from %s\n", res.Agent.Agent, res.Agent.Path)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(newInstallCmd(), newUninstallCmd())
}
