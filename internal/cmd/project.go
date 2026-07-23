package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects and their repos",
	}
	cmd.AddCommand(newProjectAddCmd(), newProjectListCmd(), newProjectAddRepoCmd())
	return cmd
}

func init() {
	rootCmd.AddCommand(newProjectCmd())
}

func newProjectAddCmd() *cobra.Command {
	var name string
	var deployGated bool
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
				ID: args[0], Name: name, DeployGated: deployGated,
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
	cmd.Flags().BoolVar(&deployGated, "deploy-gated", false, "require a verified deployment (not just a merged PR) before a task can reach done")
	cmd.MarkFlagRequired("name")
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

func newProjectAddRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-repo <id> <owner/name>",
		Short: "Map a GitHub repo to a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.AddRepo(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added %s to project %s\n", args[1], args[0])
			return nil
		},
	}
	return cmd
}
