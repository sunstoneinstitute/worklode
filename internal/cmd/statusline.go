package cmd

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/statusline"
)

// This file wires `lode statusline` to the logic in internal/statusline. It is
// deliberately thin, and deliberately un-namespaced: the payload contract is
// Claude Code's, Cursor CLI adopted it verbatim, and the harnesses that render
// built-in items instead (Codex, Gemini CLI) take no command at all. There is
// no second dialect to dispatch on, so there is no harness flag and no
// per-harness subcommand — the harness is named once, at install time.

// statuslineTimeout bounds the wait for a payload. The harness re-runs this on
// every assistant message, so a stalled read is worse than a missing line.
const statuslineTimeout = 3 * time.Second

func init() {
	rootCmd.AddCommand(newStatuslineCmd())
}

func newStatuslineCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Render one status line from a coding agent's status-line payload",
		Long: "Reads the harness's status-line JSON on stdin and prints one line: the model, the " +
			"in-progress todo, the project, the task or branch, and a context-usage meter " +
			"normalized against the auto-compact buffer.\n\n" +
			"A workspace stamped with worklode.task-id — every worktree `lode next` creates — shows " +
			"that id and the branch's slug as separate words (`worklode WL-7 fix-the-thing`). One " +
			"that is not shows its branch, marked with a worktree symbol when it is a linked " +
			"worktree.\n\n" +
			"Intended to be run by the harness, not by hand — `lode install` binds it, " +
			"and enables the git worktree config extension the task-id read depends on. " +
			"It makes no network call, because the harness re-runs it on every assistant message, " +
			"and every segment degrades to empty rather than failing.\n\n" +
			"Claude Code and Cursor CLI both send a payload this understands; harnesses that render " +
			"a fixed set of built-in items instead (Codex CLI, Gemini CLI) cannot call it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The harness renders whatever this prints. A failure must cost
			// the user a blank status line, never an error in their prompt,
			// so every error is swallowed here rather than returned.
			_ = statusline.Run(cmd.InOrStdin(), cmd.OutOrStdout(), statuslineTimeout, statusline.Options{})
			return nil
		},
	}
}
