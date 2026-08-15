package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects and their repos",
	}
	cmd.AddCommand(newProjectAddCmd(), newProjectListCmd(), newProjectAddRepoCmd(),
		newProjectSetRepoCmd(), newProjectFocusCmd(), newProjectFocusNoteCmd(),
		newProjectDecisionCmd(), newProjectResolveCmd(), newProjectShowCmd())
	return cmd
}

func init() {
	rootCmd.AddCommand(newProjectCmd())
}

func newProjectAddCmd() *cobra.Command {
	var name string
	var key string
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			p, raw, err := c.CreateProject(cmd.Context(), model.CreateProjectInput{
				ID: args[0], Name: name, Key: key,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ProjectTable(cmd.OutOrStdout(), []model.Project{p})
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "project display name (required)")
	cmd.Flags().StringVar(&key, "key", "", "project key: unique uppercase code, immutable (e.g. WL)")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("key")
	return cmd
}

func newProjectListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.ListProjects(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ProjectTable(cmd.OutOrStdout(), resp.Projects)
			return nil
		},
	}
	return cmd
}

// doneStateFlagUsage documents --done-state on the repo subcommands.
const doneStateFlagUsage = "terminal delivery state for the repo: merged, deployed_prod, or released"

func newProjectAddRepoCmd() *cobra.Command {
	var doneState string
	cmd := &cobra.Command{
		Use:   "add-repo <id> <owner/name>",
		Short: "Map a GitHub repo to a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			res, raw, err := c.AddRepo(cmd.Context(), args[0], args[1], doneState)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "added %s to project %s\n", args[1], args[0])
			for _, warning := range res.Warnings {
				fmt.Fprintf(out, "warning: %s\n", warning)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&doneState, "done-state", "", doneStateFlagUsage+" (default: server default)")
	return cmd
}

func newProjectSetRepoCmd() *cobra.Command {
	var doneState string
	cmd := &cobra.Command{
		Use:   "set-repo <owner/name>",
		Short: "Update settings on an already-mapped repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.SetRepoDoneState(cmd.Context(), args[0], doneState)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s done-state: %s\n", args[0], doneState)
			return nil
		},
	}
	cmd.Flags().StringVar(&doneState, "done-state", "", doneStateFlagUsage)
	cmd.MarkFlagRequired("done-state")
	return cmd
}

// printFocus writes the human-readable "focus: a, b" (or "focus: (none)")
// line for a project's focus list.
func printFocus(cmd *cobra.Command, focus []string) {
	if len(focus) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "focus: (none)")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "focus: %s\n", strings.Join(focus, ", "))
}

func newProjectFocusCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "focus <id> [<concern> ...]",
		Short: "Show, set, or clear a project's ranking focus (ordered list of concerns)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			concerns := args[1:]
			if clear && len(concerns) > 0 {
				return fmt.Errorf("--clear takes no concerns")
			}

			c, err := newAPIClient()
			if err != nil {
				return err
			}

			switch {
			case clear:
				p, raw, err := c.SetProjectFocus(cmd.Context(), id, []string{})
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				printFocus(cmd, p.Focus)
				return nil
			case len(concerns) > 0:
				p, raw, err := c.SetProjectFocus(cmd.Context(), id, concerns)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				printFocus(cmd, p.Focus)
				return nil
			default:
				p, err := c.GetProject(cmd.Context(), id)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					raw, err := json.Marshal(map[string]any{"id": p.ID, "focus": p.Focus})
					if err != nil {
						return fmt.Errorf("marshal focus: %w", err)
					}
					printRaw(cmd, raw)
					return nil
				}
				printFocus(cmd, p.Focus)
				return nil
			}
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "clear the project's focus")
	return cmd
}

// newProjectFocusNoteCmd is `lode project focus-note <id>`: set or clear the
// cockpit's curated pinned-focus note. --clear (or an empty --note) clears it.
func newProjectFocusNoteCmd() *cobra.Command {
	var note, by string
	var clear bool
	cmd := &cobra.Command{
		Use:   "focus-note <id>",
		Short: "Set or clear a project's pinned-focus note (cockpit card)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear && (note != "" || by != "") {
				return fmt.Errorf("--clear takes no --note or --by")
			}
			if !clear && note == "" {
				return fmt.Errorf("provide --note, or --clear to remove the pinned focus")
			}

			c, err := newAPIClient()
			if err != nil {
				return err
			}
			// A cleared note is the empty string; PinProjectFocus reads that as
			// an explicit clear on the server.
			p, raw, err := c.PinProjectFocus(cmd.Context(), args[0], note, by)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ProjectTable(cmd.OutOrStdout(), []model.Project{p})
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "the pinned-focus note")
	cmd.Flags().StringVar(&by, "by", "", "who pinned it (actor id or display name)")
	cmd.Flags().BoolVar(&clear, "clear", false, "clear the pinned focus")
	return cmd
}

// newProjectDecisionCmd is `lode project decision <id>`: set or clear the
// cockpit's curated next-decision card. --clear (or an empty --title) clears
// it; --rests-on carries the readiness note.
func newProjectDecisionCmd() *cobra.Command {
	var title, accountable, restsOn string
	var clear bool
	cmd := &cobra.Command{
		Use:   "decision <id>",
		Short: "Set or clear a project's next-decision card (cockpit card)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear && (title != "" || accountable != "" || restsOn != "") {
				return fmt.Errorf("--clear takes no --title, --accountable, or --rests-on")
			}
			if !clear && title == "" {
				return fmt.Errorf("provide --title, or --clear to remove the next decision")
			}

			c, err := newAPIClient()
			if err != nil {
				return err
			}
			// A cleared title is the empty string; SetProjectNextDecision reads
			// that as an explicit clear on the server.
			p, raw, err := c.SetProjectNextDecision(cmd.Context(), args[0], title, accountable, restsOn)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ProjectTable(cmd.OutOrStdout(), []model.Project{p})
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "the decision title")
	cmd.Flags().StringVar(&accountable, "accountable", "", "who is accountable for the decision")
	cmd.Flags().StringVar(&restsOn, "rests-on", "", "readiness note: what the decision rests on")
	cmd.Flags().BoolVar(&clear, "clear", false, "clear the next decision")
	return cmd
}

// resolveResult is the --json form of `lode project resolve`.
type resolveResult struct {
	Project string `json:"project"`
	Key     string `json:"key,omitempty"`
	Source  string `json:"source"`
	Path    string `json:"path,omitempty"`
	Remote  string `json:"remote,omitempty"`
	Cached  bool   `json:"cached"`
}

func newProjectResolveCmd() *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "resolve",
		Short: "Show which project this directory scopes to, and why",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			if refresh {
				cli.ForgetRemote(cmd.Context(), c, wd)
			}
			sc := cli.ResolveScope(cmd.Context(), c, cfg, wd)
			if sc.Project != "" && sc.Key == "" {
				sc.Key = cli.ProjectKey(cmd.Context(), c, sc.Project)
			}

			if jsonOut(cmd) {
				b, err := json.Marshal(resolveResult{
					Project: sc.Project, Key: sc.Key, Source: string(sc.Source),
					Path: sc.Path, Remote: sc.Remote, Cached: sc.Cached,
				})
				if err != nil {
					return fmt.Errorf("encode result: %w", err)
				}
				printRaw(cmd, b)
				return nil
			}

			o := cmd.OutOrStdout()
			if sc.Project == "" {
				fmt.Fprintln(o, "no current project: commands run across every project")
				fmt.Fprintln(o, `set current_project in .worklode/config.toml, or map this repo with "lode project add-repo"`)
				return nil
			}
			fmt.Fprintf(o, "%s%s — from %s\n", sc.Project, keySuffix(sc.Key), scopeOrigin(sc))
			return nil
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-query the server instead of using the cached answer")
	return cmd
}

// defaultCostDays is the cost window `lode project show` uses when --days is
// not given.
const defaultCostDays = 30

func newProjectShowCmd() *cobra.Command {
	var project string
	var days int
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show a project's repos, focus, and token cost",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectShow(cmd, project, days)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project id (default: the current project)")
	cmd.Flags().IntVar(&days, "days", defaultCostDays, "cost window in days, counting today; 0 for all history")
	return cmd
}

// runProjectShow is `project show`'s body, shared with the `lode show
// --project <id>`/`--kind project <id>` dispatcher (show.go). project is the
// project id to show, or "" to resolve the current one from scope (as `lode
// project show` with no flag does); days is the cost window, counting today
// (see costWindow).
func runProjectShow(cmd *cobra.Command, project string, days int) error {
	c, cfg, err := newAPIClientWithConfig()
	if err != nil {
		return err
	}
	id := project
	if id == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		id = cli.ResolveScope(cmd.Context(), c, cfg, wd).Project
	}
	if id == "" {
		o := cmd.OutOrStdout()
		fmt.Fprintln(o, "no current project: pass --project <id> to name one")
		fmt.Fprintln(o, `set current_project in .worklode/config.toml, or map this repo with "lode project add-repo"`)
		return nil
	}

	from, to := costWindow(days)
	detail, raw, err := c.ProjectDetail(cmd.Context(), id, from, to)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		printRaw(cmd, raw)
		return nil
	}
	printProjectDetail(cmd, detail, costWindowLabel(days))
	return nil
}

// costWindow turns --days into the [from, to] the API takes. Both ends are
// zero for days <= 0, which asks the server for all history.
func costWindow(days int) (from, to time.Time) {
	if days <= 0 {
		return time.Time{}, time.Time{}
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	return today.AddDate(0, 0, -(days - 1)), today
}

func costWindowLabel(days int) string {
	if days <= 0 {
		return "all time"
	}
	return fmt.Sprintf("last %d days", days)
}

// printProjectDetail renders `lode project show`: the project's identity,
// focus, and repos, then one cost block per currency.
func printProjectDetail(cmd *cobra.Command, d model.ProjectDetail, window string) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s%s — %s\n", d.ID, keySuffix(d.Key), d.Name)
	printFocus(cmd, d.Focus)
	if len(d.Repos) > 0 {
		fmt.Fprintln(out, "repos:")
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		for _, r := range d.Repos {
			fmt.Fprintf(tw, "  %s\tdone: %s\n", r.Repo, r.DoneState)
		}
		tw.Flush()
	}
	printCost(out, d.Cost, window)
}

// printCost writes one block per currency: a headline total, a row per day,
// and — when some tokens were billed on a model with no price on file — the
// shortfall that headline therefore omits.
func printCost(out io.Writer, cost model.ProjectCost, window string) {
	if len(cost.Totals) == 0 {
		fmt.Fprintf(out, "\ncost, %s: none recorded\n", window)
		return
	}
	// No currency symbol: a vendor need not bill in dollars, and one block per
	// currency already names it in the header. "$12.000000 EUR" is the kind of
	// wrong a symbol table earns you.
	for _, total := range cost.Totals {
		fmt.Fprintf(out, "\ncost, %s: %s %s\n", window, cli.Money(total.CostAmount), total.Currency)
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		for _, d := range cost.Days {
			if d.Currency != total.Currency {
				continue
			}
			fmt.Fprintf(tw, "  %s\t%s\tin %s\tcache-w %s\tcache-r %s\tout %s\n",
				d.Day, cli.Money(d.CostAmount),
				cli.HumanTokens(d.InputTokens),
				cli.HumanTokens(d.CacheWrite5mTokens+d.CacheWrite1hTokens),
				cli.HumanTokens(d.CacheReadTokens),
				cli.HumanTokens(d.OutputTokens))
		}
		tw.Flush()
		if total.UnpricedTokens > 0 {
			fmt.Fprintf(out, "note: %s tokens from models with no price on file are excluded from the total.\n",
				cli.HumanTokens(total.UnpricedTokens))
		}
	}
}

// keySuffix renders " (WL)" for a known task-id key, or nothing.
func keySuffix(key string) string {
	if key == "" {
		return ""
	}
	return " (" + key + ")"
}

// scopeOrigin describes where a scope came from, for humans.
func scopeOrigin(sc cli.Scope) string {
	switch sc.Source {
	case cli.ScopeRepoConfig, cli.ScopeUserConfig:
		return fmt.Sprintf("%s %s", sc.Source, sc.Path)
	case cli.ScopeGitRemote:
		cached := ""
		if sc.Cached {
			cached = " (cached)"
		}
		return fmt.Sprintf("git remote %s%s", sc.Remote, cached)
	default:
		return string(sc.Source)
	}
}
