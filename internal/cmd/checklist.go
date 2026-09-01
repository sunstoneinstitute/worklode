package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// newTaskChecklistCmd is the named-view half of checklist support (L6): the
// paired write is the "checklist" field on `lode task set` in task.go, never
// a flag here.
func newTaskChecklistCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "checklist <id>",
		Short: "Show the checklist items parsed from the task's body",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			items, raw, err := c.GetChecklist(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ChecklistRender(cmd.OutOrStdout(), items)
			return nil
		},
	}
}
