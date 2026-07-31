package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects and their repos",
	}
	cmd.AddCommand(newProjectAddCmd(), newProjectListCmd(), newProjectAddRepoCmd(),
		newProjectSetRepoCmd(), newProjectFocusCmd(), newProjectResolveCmd())
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
			p, raw, err := c.CreateProject(cmd.Context(), cli.CreateProjectInput{
				ID: args[0], Name: name, Key: key,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ProjectTable(cmd.OutOrStdout(), []cli.Project{p})
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
