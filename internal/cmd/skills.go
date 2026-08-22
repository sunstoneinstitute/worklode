package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/harness"
	"github.com/sunstoneinstitute/worklode/internal/skillstore"
)

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Org-wide agent skills: list, recommend, install, sync",
	}
	cmd.AddCommand(newSkillsListCmd(), newSkillsRecommendCmd(), newSkillsInstallCmd(), newSkillsSyncCmd())
	return cmd
}

func init() { rootCmd.AddCommand(newSkillsCmd()) }

func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List org skills known to the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			skills, raw, err := c.Skills(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.SkillTable(cmd.OutOrStdout(), skills)
			return nil
		},
	}
}

func newSkillsRecommendCmd() *cobra.Command {
	var taskID, text, file string
	var limit int
	cmd := &cobra.Command{
		Use:   "recommend",
		Short: "Recommend skills for a task or free text",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			set := 0
			for _, v := range []string{taskID, text, file} {
				if v != "" {
					set++
				}
			}
			if set != 1 {
				return fmt.Errorf("exactly one of --task, --text, --file is required")
			}
			if file != "" {
				b, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				text = string(b)
				if strings.TrimSpace(text) == "" {
					return fmt.Errorf("%s is empty", file)
				}
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			rec, raw, err := c.RecommendSkills(cmd.Context(), taskID, text, limit)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			for _, p := range rec.Pinned {
				fmt.Fprintf(out, "pinned\t%s\t%s\n", p.Name, p.Description)
			}
			for _, m := range rec.Matches {
				fmt.Fprintf(out, "%.2f\t%s\t%s\n", m.Score, m.Name, m.Description)
			}
			for _, w := range rec.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "recommend for this task id")
	cmd.Flags().StringVar(&text, "text", "", "recommend for this free text")
	cmd.Flags().StringVar(&file, "file", "", "recommend for this file's contents")
	cmd.Flags().IntVar(&limit, "limit", 5, "max matches")
	return cmd
}

func newSkillsInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <name>[@<hash>]",
		Short: "Install a skill into the local store (~/.worklode/store)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, hash, _ := strings.Cut(args[0], "@")
			if name == "" {
				return fmt.Errorf("skill name is required")
			}
			link, _ := cmd.Flags().GetString("link")
			linkIDs, err := resolveLinkAgents(link)
			if err != nil {
				return err
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			if hash == "" {
				sk, _, err := c.Skill(cmd.Context(), name)
				if err != nil {
					return err
				}
				hash = sk.Hash
				if sk.Deleted {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s was removed from its source repo\n", name)
				}
			}
			dirs, err := skillstore.DefaultDirs()
			if err != nil {
				return err
			}
			p, err := skillstore.Ensure(dirs, name, hash, func() ([]byte, error) {
				return c.SkillArchive(cmd.Context(), name, hash)
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return publishLinked(cmd, dirs, linkIDs, name)
		},
	}
	cmd.Flags().String("link", "",
		"publish this skill into a harness's personal skill directory: an adapter id or all")
	return cmd
}

// resolveLinkAgents turns --link's value into the adapter ids to publish
// into: empty means none, "all" means every registered adapter
// (harness.IDs()), anything else must name one.
func resolveLinkAgents(link string) ([]string, error) {
	switch link {
	case "":
		return nil, nil
	case agentAll:
		return harness.IDs(), nil
	default:
		if _, ok := harness.Get(link); !ok {
			return nil, unsupportedLinkError(link)
		}
		return []string{link}, nil
	}
}

// unsupportedLinkError is the one wording for a --link value the registry
// does not carry.
func unsupportedLinkError(id string) error {
	return fmt.Errorf("unsupported --link %q (supported: %s, all)", id, strings.Join(harness.IDs(), ", "))
}

// publishLinked publishes the just-installed skill into every named
// adapter's personal skill targets: a PerSkill target gets just that one
// skill's link (skillstore.PublishOneSkill), any other target becomes a
// symlink to the whole store (skillstore.PublishDirLink). Personal targets
// only (harness.ScopeLocal) — there is no repo here to scope a project-level
// target to. A publish error on one target — or a harness whose own
// SkillTargets resolution fails — is reported on stderr and the loop
// continues: install.go's installSkills takes the same record-and-continue
// stance for the equivalent `install --skills` loop, and one bad target
// (or adapter) must not stop `--link all` from reaching the rest.
func publishLinked(cmd *cobra.Command, dirs skillstore.Dirs, ids []string, name string) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	for _, id := range ids {
		h, ok := harness.Get(id)
		if !ok {
			continue
		}
		targets, err := h.SkillTargets("", harness.ScopeLocal)
		if err != nil {
			fmt.Fprintf(errOut, "%s: skill targets: %v\n", id, err)
			continue
		}
		for _, t := range targets {
			var (
				pr   skillstore.PublishResult
				perr error
			)
			if t.PerSkill {
				pr, perr = skillstore.PublishOneSkill(dirs, t.Dir, name)
			} else {
				pr, perr = skillstore.PublishDirLink(dirs, t.Dir)
			}
			if perr != nil {
				fmt.Fprintf(errOut, "%s: publish %s to %s: %v\n", id, name, t.Dir, perr)
				continue
			}
			fmt.Fprintf(out, "%s: %s %s\n", id, pr.Action, pr.Path)
		}
	}
	return nil
}

func newSkillsSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Trigger a full server-side skill sync (admin)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			report, raw, err := c.SyncSkills(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.SkillSyncRender(cmd.OutOrStdout(), report)
			return nil
		},
	}
}
