package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

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
			for _, sk := range skills {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", sk.Name, sk.Description)
			}
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
	return &cobra.Command{
		Use:   "install <name>[@<hash>]",
		Short: "Install a skill into the local store (~/.worklode/skills)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, hash, _ := strings.Cut(args[0], "@")
			if name == "" {
				return fmt.Errorf("skill name is required")
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
			root, err := skillstore.Root()
			if err != nil {
				return err
			}
			p, err := skillstore.Ensure(root, name, hash, func() ([]byte, error) {
				return c.SkillArchive(cmd.Context(), name, hash)
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		},
	}
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
			raw, err := c.SyncSkills(cmd.Context())
			if err != nil {
				return err
			}
			printRaw(cmd, raw)
			return nil
		},
	}
}
