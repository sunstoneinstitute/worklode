package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newTaskInstructCmd is `lode task instruct <id> <message>`: queue a steering
// instruction against a task, delivered to whichever actor next claims its
// lease (migration 0056). Separate file from instructions.go, which manages
// AGENTS.md/CLAUDE.local.md instruction files for `lode install` — an
// unrelated concept that happens to share the name.
func newTaskInstructCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "instruct <id> <message>",
		Short:             "Queue a steering instruction for whichever actor next holds the task's lease",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			ins, raw, err := c.Instruct(cmd.Context(), id, args[1])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.InstructionTable(cmd.OutOrStdout(), []model.Instruction{ins})
			return nil
		},
	}
}
