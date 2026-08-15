package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// The integrations `lode install`/`lode uninstall` know how to manage. Both
// flags take a name rather than being booleans so a second VCS or agent can be
// added later without changing the CLI shape.
const (
	vcsGit          = "git"
	agentClaudeCode = "claude-code"
)

// hookTargets is the set of integrations one install or uninstall run acts on.
// An empty vcs or agent means "skip this one", as does a false statusLine.
type hookTargets struct {
	vcs        string
	agent      string
	statusLine bool
}

// addHookFlags declares the flags shared by `lode install` and `lode uninstall`.
func addHookFlags(cmd *cobra.Command) {
	cmd.Flags().String("vcs", vcsGit, "version control system whose hooks to manage")
	cmd.Flags().String("agent", agentClaudeCode, "coding agent whose hooks to manage")
	cmd.Flags().Bool("no-vcs", false, "skip the version control system hooks")
	cmd.Flags().Bool("no-agent", false, "skip the coding agent hooks")
	// No backticks in these descriptions: cobra reads them as the argument-name
	// placeholder, which turns a bool flag into "--statusline lode statusline".
	cmd.Flags().Bool("statusline", true,
		"manage the agent's status line, pointing it at 'lode statusline'")
	cmd.Flags().Bool("no-statusline", false, "skip the agent's status line")
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
	statusLine, _ := flags.GetBool("statusline")
	noStatusLine, _ := flags.GetBool("no-statusline")

	if noVCS && flags.Changed("vcs") {
		return hookTargets{}, errors.New("--vcs and --no-vcs are mutually exclusive")
	}
	if noAgent && flags.Changed("agent") {
		return hookTargets{}, errors.New("--agent and --no-agent are mutually exclusive")
	}
	if noStatusLine && flags.Changed("statusline") {
		return hookTargets{}, errors.New("--statusline and --no-statusline are mutually exclusive")
	}
	// The status line lives in the agent's settings file, so skipping the agent
	// skips it too. Only an explicit --statusline contradicts --no-agent;
	// inheriting the default does not, or --no-agent alone could never run.
	if noAgent && flags.Changed("statusline") && statusLine {
		return hookTargets{}, errors.New("--statusline and --no-agent are mutually exclusive")
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
	if noStatusLine || agent == "" {
		statusLine = false
	}
	return hookTargets{vcs: vcs, agent: agent, statusLine: statusLine}, nil
}

// installResult is what one `lode install` run did. A nil field means that
// integration was skipped, and is omitted from the JSON entirely.
type installResult struct {
	VCS        *vcsInstall        `json:"vcs,omitempty"`
	Agent      *agentInstall      `json:"agent,omitempty"`
	StatusLine *statusLineInstall `json:"status_line,omitempty"`
}

type vcsInstall struct {
	VCS      string `json:"vcs"`
	HooksDir string `json:"hooks_dir"`
	// Hooks is one entry per managed git hook, in install order, each naming
	// what it chains to.
	Hooks []hookChain `json:"hooks"`
}

type agentInstall struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
}

// statusLineInstall reports an action, unlike its hook sibling, because the
// install can decline: an existing status line is left alone.
type statusLineInstall struct {
	Agent  string `json:"agent"`
	Path   string `json:"path"`
	Action string `json:"action"`
}

// uninstallResult is the same, for `lode uninstall`.
type uninstallResult struct {
	VCS        *vcsUninstall        `json:"vcs,omitempty"`
	Agent      *agentUninstall      `json:"agent,omitempty"`
	StatusLine *statusLineUninstall `json:"status_line,omitempty"`
}

type statusLineUninstall struct {
	Agent  string `json:"agent"`
	Path   string `json:"path"`
	Action string `json:"action"`
}

type vcsUninstall struct {
	VCS      string `json:"vcs"`
	HooksDir string `json:"hooks_dir"`
	// Hooks is one entry per managed git hook, in the same order, each
	// naming what the uninstall did to it.
	Hooks []hookRemoval `json:"hooks"`
}

type agentUninstall struct {
	Agent  string `json:"agent"`
	Path   string `json:"path"`
	Action string `json:"action"`
}

// installHooks installs every selected integration for the repo containing
// dir. On error it still returns whatever integrations succeeded before the
// failing one, so the caller can report what actually landed rather than
// discarding it. cmd supplies the stream warnings go to.
//
// Enabling the worktree config extension is an enhancement to task-id
// stamping, not a prerequisite for the hooks: a repo where it cannot be
// enabled (a bare clone, say) still gets its pre-commit and agent hooks, so a
// failure there warns and continues rather than aborting the run. `lode next`
// treats the same failure the same way.
func installHooks(cmd *cobra.Command, dir string, targets hookTargets, scope string) (installResult, error) {
	var res installResult
	// The extension is what makes worklode.task-id worktree-scoped, so both
	// the task-id stamping the VCS side supports and the status line's read of
	// that stamp need it. Enable it once for either.
	if targets.vcs != "" || targets.statusLine {
		if err := worktree.EnableWorktreeConfigExtension(dir); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: enable git worktree config extension: %v\n", err)
		}
	}
	if targets.vcs != "" {
		hooksDir, chains, err := installGitHooks(dir)
		if err != nil {
			return res, err
		}
		res.VCS = &vcsInstall{VCS: targets.vcs, HooksDir: hooksDir, Hooks: chains}
	}
	if targets.agent != "" {
		path, err := settingsPathForScope(dir, scope)
		if err != nil {
			return res, err
		}
		if err := installClaudeHooks(path); err != nil {
			return res, err
		}
		res.Agent = &agentInstall{Agent: targets.agent, Path: path}

		if targets.statusLine {
			action, err := installStatusLine(path)
			if err != nil {
				return res, err
			}
			res.StatusLine = &statusLineInstall{Agent: targets.agent, Path: path, Action: action}
		}
	}
	return res, nil
}

// uninstallHooks removes every selected integration from the repo containing
// dir. On error it still returns whatever integrations were already removed
// before the failing one: uninstall is destructive, so silently dropping a
// partial result here is worse than for install.
func uninstallHooks(dir string, targets hookTargets, scope string) (uninstallResult, error) {
	var res uninstallResult
	if targets.vcs != "" {
		hooksDir, removals, err := uninstallGitHooks(dir)
		if err != nil {
			return res, err
		}
		res.VCS = &vcsUninstall{VCS: targets.vcs, HooksDir: hooksDir, Hooks: removals}
	}
	if targets.agent != "" {
		path, err := settingsPathForScope(dir, scope)
		if err != nil {
			return res, err
		}
		action, err := uninstallClaudeHooks(path)
		if err != nil {
			return res, err
		}
		res.Agent = &agentUninstall{Agent: targets.agent, Path: path, Action: action}

		if targets.statusLine {
			slAction, err := uninstallStatusLine(path)
			if err != nil {
				return res, err
			}
			res.StatusLine = &statusLineUninstall{Agent: targets.agent, Path: path, Action: slAction}
		}
	}
	return res, nil
}

func newInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Worklode's hooks for this repo's VCS and coding agent",
		Long: "Installs two integrations. The VCS side writes pre-commit, post-merge and post-commit " +
			"hooks (into the repo's shared hooks directory, honoring core.hooksPath) that invoke " +
			"`lode hook <event>`, chaining any hook already present on the same event, or — for " +
			"pre-commit — the pre-commit framework. pre-commit keeps a working session's lease " +
			"alive; post-merge and post-commit report a merge that lands on the default branch " +
			"here, so a task advances without waiting for a GitHub webhook. The agent " +
			"side writes Worklode's lifecycle hook bindings (session start/end, heartbeat, worktree " +
			"enter) into the repo's Claude Code settings file. Use --no-vcs or --no-agent to skip " +
			"either. Safe to re-run: both converge rather than accumulate.\n\n" +
			"The agent side also points the status line at `lode statusline`, and enables the git " +
			"worktree config extension that lets it read a workspace's own worklode.task-id. That " +
			"is safe to have on by default because it never takes a slot it does not already own: " +
			"the slot holds exactly one command, so a status line someone else configured is " +
			"reported and left alone. Use --no-statusline to skip it entirely.",
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
			res, err := installHooks(cmd, cwd, targets, scope)
			if err != nil {
				// Report whatever succeeded before failing: install is not
				// atomic, and silently dropping that leaves the user thinking
				// nothing happened when part of the repo already changed.
				if reportErr := reportInstall(cmd, res); reportErr != nil {
					return reportErr
				}
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
		Long: "Removes what `lode install` added. The VCS side removes Worklode's pre-commit, " +
			"post-merge and post-commit hooks and restores whatever it preserved, leaving a " +
			"third-party hook it does not " +
			"recognize untouched. The agent side removes every `lode hook` binding from the repo's " +
			"Claude Code settings file, leaving all other settings — including third-party hooks on " +
			"the same events — in place. Use --no-vcs or --no-agent to skip either. A repo with " +
			"nothing installed is not an error.\n\n" +
			"The agent side also removes the status line, but only if it is ours; one someone else " +
			"configured is reported and left alone. Use --no-statusline to leave ours in place.",
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
				// Uninstall is destructive: a failed agent step after the
				// VCS hook was already removed must not be reported as if
				// nothing happened.
				if reportErr := reportUninstall(cmd, res); reportErr != nil {
					return reportErr
				}
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
		for _, h := range res.VCS.Hooks {
			fmt.Fprintf(out, "%s: installed %s hook in %s\n", res.VCS.VCS, h.Hook, res.VCS.HooksDir)
			if h.ChainedTo != "" {
				fmt.Fprintf(out, "%s: %s chains to %s\n", res.VCS.VCS, h.Hook, h.ChainedTo)
			}
		}
	}
	if res.Agent != nil {
		fmt.Fprintf(out, "%s: installed hooks in %s\n", res.Agent.Agent, res.Agent.Path)
	}
	if sl := res.StatusLine; sl != nil {
		switch sl.Action {
		case hookActionInstalled:
			fmt.Fprintf(out, "%s: status line set to `%s` in %s\n", sl.Agent, lodeStatusLineCommand, sl.Path)
		case hookActionKept:
			fmt.Fprintf(out, "%s: kept the status line already configured in %s\n", sl.Agent, sl.Path)
		default:
			fmt.Fprintf(out, "%s: unexpected status line result %q in %s\n", sl.Agent, sl.Action, sl.Path)
		}
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
		for _, h := range res.VCS.Hooks {
			switch h.Action {
			case hookActionRestored:
				fmt.Fprintf(out, "%s: removed %s hook from %s and restored the previous one\n",
					res.VCS.VCS, h.Hook, res.VCS.HooksDir)
			case hookActionRemoved:
				fmt.Fprintf(out, "%s: removed %s hook from %s\n", res.VCS.VCS, h.Hook, res.VCS.HooksDir)
			case hookActionNone:
				fmt.Fprintf(out, "%s: no Worklode %s hook in %s\n", res.VCS.VCS, h.Hook, res.VCS.HooksDir)
			default:
				fmt.Fprintf(out, "%s: unexpected uninstall result %q for %s in %s\n",
					res.VCS.VCS, h.Action, h.Hook, res.VCS.HooksDir)
			}
		}
	}
	if res.Agent != nil {
		switch res.Agent.Action {
		case hookActionRemoved:
			fmt.Fprintf(out, "%s: removed hooks from %s\n", res.Agent.Agent, res.Agent.Path)
		case hookActionNone:
			fmt.Fprintf(out, "%s: no Worklode hooks in %s\n", res.Agent.Agent, res.Agent.Path)
		default:
			fmt.Fprintf(out, "%s: unexpected uninstall result %q in %s\n", res.Agent.Agent, res.Agent.Action, res.Agent.Path)
		}
	}
	if sl := res.StatusLine; sl != nil {
		switch sl.Action {
		case hookActionRemoved:
			fmt.Fprintf(out, "%s: removed the status line from %s\n", sl.Agent, sl.Path)
		case hookActionKept:
			fmt.Fprintf(out, "%s: left the status line in %s alone (not ours)\n", sl.Agent, sl.Path)
		case hookActionNone:
			fmt.Fprintf(out, "%s: no status line in %s\n", sl.Agent, sl.Path)
		default:
			fmt.Fprintf(out, "%s: unexpected status line result %q in %s\n", sl.Agent, sl.Action, sl.Path)
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(newInstallCmd(), newUninstallCmd())
}
