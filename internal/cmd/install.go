package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/githooks"
	"github.com/sunstoneinstitute/worklode/internal/harness"
	"github.com/sunstoneinstitute/worklode/internal/skillstore"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// The integrations `lode install`/`lode uninstall` know how to manage. --vcs
// takes a name rather than being a boolean so a second VCS can be added later
// without changing the CLI shape; the agent side is a registry lookup
// (internal/harness), never a constant here.
const vcsGit = "git"

// The two --agent pseudo-ids. auto resolves against the repo at install time;
// all is every registered adapter. Both must stand alone.
const (
	agentAuto = "auto"
	agentAll  = "all"
)

// hookTargets is the set of integrations one install or uninstall run acts on.
// An empty vcs or an empty agents means "skip this one", as does a false
// statusLine. agents is either the single element "auto" — resolved against
// the repo directory by installHooks/uninstallHooks — or a validated list of
// adapter ids.
type hookTargets struct {
	vcs        string
	agents     []string
	statusLine bool
	// skills is --skills: publish the local skill store into every
	// registered adapter's skill directories. Independent of agents — it
	// writes outside the hook config (spec 008 §3.2) and runs for every
	// registered adapter regardless of which ones agents names.
	skills bool
}

// addHookFlags declares the flags shared by `lode install` and `lode uninstall`.
func addHookFlags(cmd *cobra.Command) {
	cmd.Flags().String("vcs", vcsGit, "version control system whose hooks to manage")
	cmd.Flags().StringSlice("agent", []string{agentAuto},
		"coding agent(s) to manage: an adapter id, auto, or all (repeatable)")
	cmd.Flags().Bool("no-vcs", false, "skip the version control system hooks")
	cmd.Flags().Bool("no-agent", false, "skip the coding agent hooks")
	// No backticks in these descriptions: cobra reads them as the argument-name
	// placeholder, which turns a bool flag into "--statusline lode statusline".
	cmd.Flags().Bool("statusline", true,
		"manage the agent's status line, pointing it at 'lode statusline'")
	cmd.Flags().Bool("no-statusline", false, "skip the agent's status line")
	cmd.Flags().Bool("skills", false,
		"publish the Worklode skill store into every harness's skill directories")
	cmd.Flags().String("scope", harness.ScopeLocal,
		"whether to write each harness's personal config or its committed one: local or "+
			"project (Claude Code settings.local.json vs settings.json, Copilot "+
			"~/.copilot/hooks vs .github/hooks); codex and amp have only a user-level "+
			"config and ignore this")
}

// resolveHookTargets turns the parsed flags into the set of integrations to act
// on. Naming an integration and opting out of it in the same run is a
// contradiction rather than a precedence question, so it is rejected instead of
// silently picking a winner.
//
// --scope is validated here too, though it is not part of hookTargets: only
// two of the four adapters have a per-scope location, so leaving the check to
// them would let a typo pass silently for the other two.
func resolveHookTargets(cmd *cobra.Command) (hookTargets, error) {
	flags := cmd.Flags()
	vcs, _ := flags.GetString("vcs")
	agents, _ := flags.GetStringSlice("agent")
	scope, _ := flags.GetString("scope")
	noVCS, _ := flags.GetBool("no-vcs")
	noAgent, _ := flags.GetBool("no-agent")
	statusLine, _ := flags.GetBool("statusline")
	noStatusLine, _ := flags.GetBool("no-statusline")
	skills, _ := flags.GetBool("skills")

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
	if scope != harness.ScopeLocal && scope != harness.ScopeProject {
		return hookTargets{}, fmt.Errorf("unsupported --scope %q (supported: %s, %s)",
			scope, harness.ScopeLocal, harness.ScopeProject)
	}

	if noVCS {
		vcs = ""
	} else if vcs != vcsGit {
		return hookTargets{}, fmt.Errorf("unsupported --vcs %q (supported: %s)", vcs, vcsGit)
	}
	if noAgent {
		agents = nil
	} else {
		var err error
		if agents, err = normalizeAgents(agents); err != nil {
			return hookTargets{}, err
		}
		// pflag reads --agent as CSV, so `--agent ""` (an unset shell
		// variable) parses to an empty slice and never reaches
		// normalizeAgents' validation. Naming no agent at all is not the
		// same as --no-agent, so reject it rather than silently skipping
		// the agent side.
		if len(agents) == 0 && flags.Changed("agent") {
			return hookTargets{}, unsupportedAgentError("")
		}
	}
	// --skills is independent of vcs/agents (it writes outside the hook
	// config), so it counts toward "there is something to do" too — --no-vcs
	// --no-agent --skills is exactly how to publish skills without touching
	// hook config at all, and must not be rejected as a no-op.
	if vcs == "" && len(agents) == 0 && !skills {
		return hookTargets{}, errors.New(
			"nothing to do: --no-vcs and --no-agent were both given (add --skills to publish the skill store)")
	}
	if noStatusLine || len(agents) == 0 {
		statusLine = false
	}
	return hookTargets{vcs: vcs, agents: agents, statusLine: statusLine, skills: skills}, nil
}

// normalizeAgents dedupes --agent (preserving the order given) and validates
// it against the registry. The pseudo-ids stand alone: `all` alongside an
// explicit id contradicts the intent of naming one, and `auto` alongside one
// contradicts the intent of detecting. `auto` stays unresolved here — there is
// no repo directory at flag-parsing time.
func normalizeAgents(agents []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, id := range agents {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range out {
		switch id {
		case agentAuto, agentAll:
			if len(out) > 1 {
				return nil, fmt.Errorf("--agent %s cannot be combined with another agent", id)
			}
		default:
			if _, ok := harness.Get(id); !ok {
				return nil, unsupportedAgentError(id)
			}
		}
	}
	if len(out) == 1 && out[0] == agentAll {
		return harness.IDs(), nil
	}
	return out, nil
}

// unsupportedAgentError is the one wording for an --agent value the registry
// does not carry, so an empty value and a misspelled one read alike.
func unsupportedAgentError(id string) error {
	return fmt.Errorf("unsupported --agent %q (supported: %s, auto, all)",
		id, strings.Join(harness.IDs(), ", "))
}

// resolveAgents turns the "auto" placeholder into the harnesses actually
// configured for dir. Detecting none is a successful no-op, not an error: spec
// 008 §4 writes nothing for a harness that is not there.
func resolveAgents(agents []string, dir string) []string {
	if len(agents) == 1 && agents[0] == agentAuto {
		return harness.Detected(dir)
	}
	return agents
}

// detectDir is the directory Detect must be given: the git worktree root
// containing dir. Adapters look for repo-level signals (.claude/,
// .github/copilot-instructions.md) at the top of the repo, so passing the
// process working directory would make `--agent auto` resolve differently
// depending on which subdirectory the command ran from. Outside a git repo
// dir stands in for the root — auto-detection must never fail an install.
func detectDir(dir string) string {
	if root, ok := worktree.Root(dir); ok {
		return root
	}
	return dir
}

// eventNames renders harness.Events as strings for the JSON report.
func eventNames(evs []harness.Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, string(e))
	}
	return out
}

// installResult is what one `lode install` run did. A nil field means that
// integration was skipped, and is omitted from the JSON entirely.
type installResult struct {
	VCS        *vcsInstall         `json:"vcs,omitempty"`
	Agents     []agentInstall      `json:"agents,omitempty"`
	StatusLine []statusLineInstall `json:"status_line,omitempty"`
	// Instructions is the repo-level AGENTS.md/CLAUDE.md pair, not a
	// per-harness integration: nil when there was no repo root to write to.
	Instructions *instructionsResult `json:"instructions,omitempty"`
	// Skills is one entry per --skills publish target (spec 008 acceptance
	// 9), empty/omitted when --skills was not given.
	Skills []skillstore.PublishResult `json:"skills,omitempty"`
}

type vcsInstall struct {
	VCS      string `json:"vcs"`
	HooksDir string `json:"hooks_dir"`
	// Hooks is one entry per managed git hook, in install order, each naming
	// what it chains to.
	Hooks []githooks.Chain `json:"hooks"`
}

type agentInstall struct {
	Agent string   `json:"agent"`
	Path  string   `json:"path"`
	Bound []string `json:"bound,omitempty"`
	// UnboundEvents names the Worklode events this harness could not bind
	// (spec 024 acceptance 4). Coverage degrades to the git pre-commit
	// heartbeat, which the vcs stanza reports.
	UnboundEvents []string `json:"unbound_events,omitempty"`
	// Notes is adapter-specific advice the report must show, such as a
	// harness whose hooks stay inert until the user approves them.
	Notes []string `json:"notes,omitempty"`
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
	VCS          *vcsUninstall         `json:"vcs,omitempty"`
	Agents       []agentUninstall      `json:"agents,omitempty"`
	StatusLine   []statusLineUninstall `json:"status_line,omitempty"`
	Instructions *instructionsResult   `json:"instructions,omitempty"`
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
	Hooks []githooks.Removal `json:"hooks"`
}

type agentUninstall struct {
	Agent  string `json:"agent"`
	Path   string `json:"path"`
	Action string `json:"action"`
}

// installAgentHooks writes one harness's hook bindings, and its status line
// when the harness has one and this run targets it. The status line is another
// key in the same config file as the hooks, so an adapter that has one writes
// both in a single pass rather than reading and rewriting the file twice; the
// result carries both outcomes.
func installAgentHooks(h harness.Harness, dir, scope string, statusLine bool) (harness.HookInstall, error) {
	if sl, ok := h.(harness.StatusLiner); ok && statusLine {
		return sl.InstallWithStatusLine(dir, scope)
	}
	return h.InstallHooks(dir, scope)
}

// uninstallAgentHooks is installAgentHooks for the removal side.
func uninstallAgentHooks(h harness.Harness, dir, scope string, statusLine bool) (harness.HookUninstall, error) {
	if sl, ok := h.(harness.StatusLiner); ok && statusLine {
		return sl.UninstallWithStatusLine(dir, scope)
	}
	return h.UninstallHooks(dir, scope)
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
		hooksDir, chains, err := githooks.Install(dir)
		if err != nil {
			return res, err
		}
		res.VCS = &vcsInstall{VCS: targets.vcs, HooksDir: hooksDir, Hooks: chains}
	}
	agents := resolveAgents(targets.agents, detectDir(dir))
	if len(targets.agents) > 0 && len(agents) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(),
			"no coding agent detected; skipped (name one with --agent <id> to install anyway)")
	}
	for _, id := range agents {
		h, ok := harness.Get(id)
		if !ok {
			return res, fmt.Errorf("unknown agent %q", id)
		}
		hooks, err := installAgentHooks(h, dir, scope, targets.statusLine)
		if err != nil {
			return res, fmt.Errorf("install %s hooks: %w", id, err)
		}
		res.Agents = append(res.Agents, agentInstall{
			Agent: id, Path: hooks.Path, Bound: hooks.Bound,
			UnboundEvents: eventNames(hooks.Unbound), Notes: hooks.Notes,
		})

		// Only a harness with a status-line slot gets a stanza; an adapter
		// without one contributes nothing rather than an empty action.
		if sl := hooks.StatusLine; sl != nil {
			res.StatusLine = append(res.StatusLine,
				statusLineInstall{Agent: id, Path: sl.Path, Action: sl.Action})
		}
	}

	if targets.skills {
		if err := installSkills(&res, dir); err != nil {
			return res, err
		}
	}

	// The managed block is repo-level, not per-harness, so it is written once
	// whatever the agent selection was (spec 008 §17.7). It anchors at the
	// *main* worktree, not dir's own root: AGENTS.md/CLAUDE.md are tracked
	// files, so installing from a task worktree would otherwise dirty that
	// task's branch with an unrelated change — a linked worktree inherits the
	// main checkout's instruction files (WL-219). Outside a git repo there is
	// no root to anchor to; warn and carry on, the same posture the worktree
	// config extension takes.
	if root, ok := worktree.MainRoot(dir); ok {
		instr, err := ensureInstructions(root)
		if err != nil {
			return res, err
		}
		res.Instructions = instr
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: %s is not inside a git repository; skipped the %s block\n", dir, agentsFile)
	}
	return res, nil
}

// skillTargets collects the union of every registered adapter's skill
// directories — harness.IDs(), not just those detected or named by --agent:
// acceptance 9 (spec 008) wants every doorway open from one command, the
// links are inert for a harness that is not installed, and one installed
// later then just works. Deduped by Dir, first-seen wins: today three of the
// four adapters report ~/.agents/skills with PerSkill=false and claude-code
// alone reports a distinct Dir with PerSkill=true, so no two adapters ever
// disagree about the same Dir's PerSkill-ness — first-seen-wins is safe only
// because that invariant holds. The check below turns a future adapter that
// broke it into an error here rather than a silently wrong publish action.
func skillTargets(dir string) ([]harness.SkillTarget, error) {
	seen := map[string]bool{} // Dir -> the PerSkill value it was first seen with
	var out []harness.SkillTarget
	for _, id := range harness.IDs() {
		h, ok := harness.Get(id)
		if !ok {
			continue
		}
		targets, err := h.SkillTargets(dir, harness.ScopeLocal)
		if err != nil {
			return nil, fmt.Errorf("skill targets for %s: %w", id, err)
		}
		for _, t := range targets {
			if perSkill, ok := seen[t.Dir]; ok {
				if perSkill != t.PerSkill {
					return nil, fmt.Errorf(
						"skill target %s: %s reports PerSkill=%v, disagreeing with an earlier adapter",
						t.Dir, id, t.PerSkill)
				}
				continue
			}
			seen[t.Dir] = t.PerSkill
			out = append(out, t)
		}
	}
	return out, nil
}

// installSkills publishes the local skill store into every target
// skillTargets names: a PerSkill target gets one link per skill inside it
// (skillstore.PublishPerSkill), any other target becomes a symlink to the
// store's links dir (skillstore.PublishDirLink). A publish error on one
// target is recorded in that target's result and the loop continues,
// mirroring installHooks' own install-is-not-atomic reporting stance.
func installSkills(res *installResult, dir string) error {
	dirs, err := skillstore.DefaultDirs()
	if err != nil {
		return err
	}
	targets, err := skillTargets(dir)
	if err != nil {
		return err
	}
	for _, t := range targets {
		var (
			pr   skillstore.PublishResult
			perr error
		)
		if t.PerSkill {
			pr, perr = skillstore.PublishPerSkill(dirs, t.Dir)
			// PublishPerSkill reports "linked" for an individual entry; a
			// PerSkill target normalizes that to "per-skill" so reportInstall
			// never claims the whole dir (e.g. ~/.claude/skills) was replaced
			// with a symlink into the store (spec 008 §17.3).
			if pr.Action == "linked" {
				pr.Action = "per-skill"
			}
		} else {
			pr, perr = skillstore.PublishDirLink(dirs, t.Dir)
		}
		if perr != nil {
			pr.Path = t.Dir
			pr.Action = "skipped"
			pr.Skips = append(pr.Skips, perr.Error())
		}
		res.Skills = append(res.Skills, pr)
	}
	return nil
}

// uninstallHooks removes every selected integration from the repo containing
// dir. On error it still returns whatever integrations were already removed
// before the failing one: uninstall is destructive, so silently dropping a
// partial result here is worse than for install.
func uninstallHooks(dir string, targets hookTargets, scope string) (uninstallResult, error) {
	var res uninstallResult
	if targets.vcs != "" {
		hooksDir, removals, err := githooks.Uninstall(dir)
		if err != nil {
			return res, err
		}
		res.VCS = &vcsUninstall{VCS: targets.vcs, HooksDir: hooksDir, Hooks: removals}
	}
	for _, id := range resolveAgents(targets.agents, detectDir(dir)) {
		h, ok := harness.Get(id)
		if !ok {
			return res, fmt.Errorf("unknown agent %q", id)
		}
		hooks, err := uninstallAgentHooks(h, dir, scope, targets.statusLine)
		if err != nil {
			return res, fmt.Errorf("uninstall %s hooks: %w", id, err)
		}
		res.Agents = append(res.Agents,
			agentUninstall{Agent: id, Path: hooks.Path, Action: hooks.Action})

		if sl := hooks.StatusLine; sl != nil {
			res.StatusLine = append(res.StatusLine,
				statusLineUninstall{Agent: id, Path: sl.Path, Action: sl.Action})
		}
	}

	// Mirrors installHooks, main worktree included: the block was written
	// there, so that is where it has to be stripped from. It has no
	// *cobra.Command to warn on, and a repo root that has vanished has nothing
	// to remove anyway, so this side skips silently: installing is where the
	// user needs to hear about it.
	if root, ok := worktree.MainRoot(dir); ok {
		instr, err := removeInstructions(root)
		if err != nil {
			return res, err
		}
		res.Instructions = instr
	}
	return res, nil
}

func newInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Worklode's hooks for this repo's VCS and coding agent",
		Long: "Installs two integrations. The VCS side writes pre-commit, commit-msg, post-merge and " +
			"post-commit hooks (into the repo's shared hooks directory, honoring core.hooksPath) " +
			"that invoke `lode hook <event>`, chaining any hook already present on the same event, " +
			"or — for pre-commit — the pre-commit framework. pre-commit keeps a working session's " +
			"lease alive; commit-msg stamps a Worklode-Task trailer into commits made in a task " +
			"worktree, so the commit says which task it belongs to even after a rebase or squash; " +
			"post-merge and post-commit report a merge that lands on the default branch " +
			"here, so a task advances without waiting for a GitHub webhook. The agent " +
			"side writes Worklode's lifecycle hook bindings (session start/end, heartbeat, worktree " +
			"enter) into each targeted coding agent's own configuration, and names any event that " +
			"agent cannot express. Use --no-vcs or --no-agent to skip " +
			"either. Safe to re-run: both converge rather than accumulate.\n\n" +
			"--agent is repeatable and defaults to auto, which installs into every harness detected " +
			"for this repo or user; all installs into every supported harness. Naming a harness " +
			"explicitly installs it even when undetected — asking for it is the detection signal.\n\n" +
			"The agent side also points the status line at `lode statusline`, and enables the git " +
			"worktree config extension that lets it read a workspace's own worklode.task-id. That " +
			"is safe to have on by default because it never takes a slot it does not already own: " +
			"the slot holds exactly one command, so a status line someone else configured is " +
			"reported and left alone. Use --no-statusline to skip it entirely.\n\n" +
			"--skills publishes the local skill store (~/.worklode/skills) into every registered " +
			"harness's skill directory in one pass — Codex, Copilot and Amp through ~/.agents/skills, " +
			"Claude Code through ~/.claude/skills/<name> — for every registered adapter, not just " +
			"ones detected for this repo, so a harness installed later just works. It is off by " +
			"default because it writes outside the hook config; a real directory already at a " +
			"target is never replaced, only linked into per-skill.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := resolveHookTargets(cmd)
			if err != nil {
				return err
			}
			scope, _ := cmd.Flags().GetString("scope")
			cwd, err := workingDir()
			if err != nil {
				return err
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
			"commit-msg, post-merge and post-commit hooks and restores whatever it preserved, leaving a " +
			"third-party hook it does not " +
			"recognize untouched. The agent side removes every `lode hook` binding from each " +
			"targeted coding agent's configuration, leaving all other settings — including " +
			"third-party hooks on the same events — in place. Use --no-vcs or --no-agent to skip " +
			"either. A repo with nothing installed is not an error.\n\n" +
			"--agent is repeatable and defaults to auto (every detected harness); all covers every " +
			"supported harness, and naming one explicitly acts on it whether or not it is " +
			"detected.\n\n" +
			"The agent side also removes the status line, but only if it is ours; one someone else " +
			"configured is reported and left alone. Use --no-statusline to leave ours in place.\n\n" +
			"--skills is accepted but does nothing here: skill links are inert data outside the " +
			"hook config, publishing them was an explicit opt-in, and removing entries under " +
			"~/.claude/skills risks deleting content the user put there themselves. Uninstall never " +
			"removes what --skills published.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := resolveHookTargets(cmd)
			if err != nil {
				return err
			}
			scope, _ := cmd.Flags().GetString("scope")
			cwd, err := workingDir()
			if err != nil {
				return err
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
		return printJSON(cmd, res)
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
	for _, a := range res.Agents {
		line := fmt.Sprintf("%s: installed hooks in %s", a.Agent, a.Path)
		// An adapter that binds nothing writes nothing, so naming its
		// settings file would report a write that never happened.
		if len(a.Bound) == 0 {
			line = fmt.Sprintf("%s: bound no hooks", a.Agent)
		}
		if len(a.UnboundEvents) > 0 {
			line += fmt.Sprintf(" (no binding for: %s; git pre-commit still covers the heartbeat)",
				strings.Join(a.UnboundEvents, ", "))
		}
		fmt.Fprintln(out, line)
		for _, note := range a.Notes {
			fmt.Fprintf(out, "%s: %s\n", a.Agent, note)
		}
	}
	for _, sl := range res.StatusLine {
		switch sl.Action {
		case harness.ActionInstalled:
			fmt.Fprintf(out, "%s: status line set to `%s` in %s\n", sl.Agent, harness.StatusLineCommand, sl.Path)
		case harness.ActionKept:
			fmt.Fprintf(out, "%s: kept the status line already configured in %s\n", sl.Agent, sl.Path)
		default:
			fmt.Fprintf(out, "%s: unexpected status line result %q in %s\n", sl.Agent, sl.Action, sl.Path)
		}
	}
	if len(res.Skills) > 0 {
		dirs, _ := skillstore.DefaultDirs() // best-effort: only for the "linked" line's arrow
		for _, s := range res.Skills {
			switch s.Action {
			case "linked":
				fmt.Fprintf(out, "skills: linked %s -> %s\n", s.Path, dirs.Links)
			case "per-skill":
				fmt.Fprintf(out, "skills: linked skills individually in %s\n", s.Path)
			case "copied":
				fmt.Fprintf(out, "skills: copied skills into %s (symlinks unavailable)\n", s.Path)
			case "unchanged":
				fmt.Fprintf(out, "skills: %s already up to date\n", s.Path)
			case "skipped":
				reason := "exists, not ours"
				if len(s.Skips) > 0 {
					reason = strings.Join(s.Skips, ", ")
				}
				fmt.Fprintf(out, "skills: skipped %s (%s)\n", s.Path, reason)
			default:
				fmt.Fprintf(out, "skills: unexpected publish result %q in %s\n", s.Action, s.Path)
			}
		}
	}
	if i := res.Instructions; i != nil {
		switch i.AgentsMD {
		case instrCreated:
			fmt.Fprintf(out, "%s: created with the Worklode block\n", agentsFile)
		case instrAdded:
			fmt.Fprintf(out, "%s: added the Worklode block\n", agentsFile)
		case instrUpdated:
			fmt.Fprintf(out, "%s: refreshed the Worklode block\n", agentsFile)
		case instrUnchanged:
			fmt.Fprintf(out, "%s: the Worklode block is already current\n", agentsFile)
		default:
			fmt.Fprintf(out, "%s: unexpected result %q\n", agentsFile, i.AgentsMD)
		}
		switch i.ClaudeMD {
		case instrCreated:
			fmt.Fprintf(out, "%s: created, importing %s\n", claudeFile, agentsFile)
		case instrSuggested:
			fmt.Fprintf(out, "%s: claude-code reads this file; add %q to it to import the Worklode block\n",
				claudeFile, claudeImportLine)
		case instrSatisfied:
			fmt.Fprintf(out, "%s: already carries the Worklode block\n", claudeFile)
		default:
			fmt.Fprintf(out, "%s: unexpected result %q\n", claudeFile, i.ClaudeMD)
		}
	}
	return nil
}

// reportUninstall is reportInstall for the removal side.
func reportUninstall(cmd *cobra.Command, res uninstallResult) error {
	if jsonOut(cmd) {
		return printJSON(cmd, res)
	}
	out := cmd.OutOrStdout()
	if res.VCS != nil {
		for _, h := range res.VCS.Hooks {
			switch h.Action {
			case githooks.ActionRestored:
				fmt.Fprintf(out, "%s: removed %s hook from %s and restored the previous one\n",
					res.VCS.VCS, h.Hook, res.VCS.HooksDir)
			case githooks.ActionRemoved:
				fmt.Fprintf(out, "%s: removed %s hook from %s\n", res.VCS.VCS, h.Hook, res.VCS.HooksDir)
			case githooks.ActionNone:
				fmt.Fprintf(out, "%s: no Worklode %s hook in %s\n", res.VCS.VCS, h.Hook, res.VCS.HooksDir)
			default:
				fmt.Fprintf(out, "%s: unexpected uninstall result %q for %s in %s\n",
					res.VCS.VCS, h.Action, h.Hook, res.VCS.HooksDir)
			}
		}
	}
	for _, a := range res.Agents {
		switch a.Action {
		case harness.ActionRemoved:
			fmt.Fprintf(out, "%s: removed hooks from %s\n", a.Agent, a.Path)
		case harness.ActionNone:
			fmt.Fprintf(out, "%s: no Worklode hooks in %s\n", a.Agent, a.Path)
		default:
			fmt.Fprintf(out, "%s: unexpected uninstall result %q in %s\n", a.Agent, a.Action, a.Path)
		}
	}
	for _, sl := range res.StatusLine {
		switch sl.Action {
		case harness.ActionRemoved:
			fmt.Fprintf(out, "%s: removed the status line from %s\n", sl.Agent, sl.Path)
		case harness.ActionKept:
			fmt.Fprintf(out, "%s: left the status line in %s alone (not ours)\n", sl.Agent, sl.Path)
		case harness.ActionNone:
			fmt.Fprintf(out, "%s: no status line in %s\n", sl.Agent, sl.Path)
		default:
			fmt.Fprintf(out, "%s: unexpected status line result %q in %s\n", sl.Agent, sl.Action, sl.Path)
		}
	}
	if i := res.Instructions; i != nil {
		switch i.AgentsMD {
		case instrRemoved:
			fmt.Fprintf(out, "%s: removed the Worklode block\n", agentsFile)
		case instrNone:
			fmt.Fprintf(out, "%s: no Worklode block\n", agentsFile)
		default:
			fmt.Fprintf(out, "%s: unexpected result %q\n", agentsFile, i.AgentsMD)
		}
		switch i.ClaudeMD {
		case instrRemoved:
			fmt.Fprintf(out, "%s: removed (it held nothing but the %s import)\n", claudeFile, agentsFile)
		case instrNone:
			fmt.Fprintf(out, "%s: left alone (authored prose)\n", claudeFile)
		default:
			fmt.Fprintf(out, "%s: unexpected result %q\n", claudeFile, i.ClaudeMD)
		}
	}
	return nil
}

func init() {
	rootCmd.AddCommand(newInstallCmd(), newUninstallCmd())
}
