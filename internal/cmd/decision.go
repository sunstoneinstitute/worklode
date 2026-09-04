package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// decisionFlags are the fields a posed question carries (025 §10.1),
// shared by add and edit so the two surfaces cannot drift.
type decisionFlags struct {
	key          string
	question     string
	context      string
	group        string
	responseType string
	options      []string
	minPicks     int
	maxPicks     int
}

func addDecisionFlags(cmd *cobra.Command, f *decisionFlags) {
	cmd.Flags().StringVar(&f.key, "key", "", "stable key for the question within the task, e.g. x-distribution")
	cmd.Flags().StringVar(&f.question, "question", "", "the question, phrased as a question")
	cmd.Flags().StringVar(&f.context, "context", "", "markdown context the decider needs")
	cmd.Flags().StringVar(&f.group, "group", "", "optional sub-grouping within the task")
	cmd.Flags().StringVar(&f.responseType, "type", "",
		"single_select, multi_select, single_select_notes, pick_or_freetext, yes_no or freetext "+
			"(default: single_select with --option, otherwise freetext)")
	cmd.Flags().StringArrayVar(&f.options, "option", nil, `an offered choice, "Label" or "Label:description" (repeatable)`)
	cmd.Flags().IntVar(&f.minPicks, "min-picks", 0, "fewest options a multi_select answer may name")
	cmd.Flags().IntVar(&f.maxPicks, "max-picks", 0, "most options a multi_select answer may name")
}

// input builds the wire body from the flags actually set. Everything a flag
// did not touch is left out, so an edit changes only what was named.
func (f *decisionFlags) input(cmd *cobra.Command) (model.DecisionInput, error) {
	in := model.DecisionInput{Key: f.key, Question: f.question, ResponseType: f.responseType}
	for _, raw := range f.options {
		opt, err := cli.ParseDecisionOption(raw)
		if err != nil {
			return in, err
		}
		in.Options = append(in.Options, opt)
	}
	// Naming options without naming a type means a pick-one question; naming
	// neither means free text. Either way the server revalidates.
	if in.ResponseType == "" && len(in.Options) > 0 {
		in.ResponseType = "single_select"
	}
	if cmd.Flags().Changed("group") {
		in.Group = &f.group
	}
	if cmd.Flags().Changed("context") {
		in.Context = &f.context
	}
	if cmd.Flags().Changed("min-picks") {
		in.MinPicks = &f.minPicks
	}
	if cmd.Flags().Changed("max-picks") {
		in.MaxPicks = &f.maxPicks
	}
	return in, nil
}

func newDecisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decision",
		Short: "Decisions: the questions a task poses and the answers they wait on",
	}
	cmd.AddCommand(newDecisionAddCmd(), newDecisionEditCmd())
	return cmd
}

func newDecisionAddCmd() *cobra.Command {
	var f decisionFlags
	cmd := &cobra.Command{
		Use:   "add <task>",
		Short: "Pose a question on a task",
		Long: "Pose a question on a task. Any kind of task may carry questions; a\n" +
			"decision-kind task closes when its last one is answered.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := f.input(cmd)
			if err != nil {
				return err
			}
			if in.ResponseType == "" {
				in.ResponseType = "freetext"
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			d, raw, err := c.AddDecision(cmd.Context(), args[0], in)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DecisionRender(cmd.OutOrStdout(), d)
			return nil
		},
	}
	addDecisionFlags(cmd, &f)
	return cmd
}

func newDecisionEditCmd() *cobra.Command {
	var f decisionFlags
	var task string
	cmd := &cobra.Command{
		Use:   "edit <task>/<key>",
		Short: "Reword, regroup or re-parent an unanswered question",
		Long: "Reword, regroup or re-parent an unanswered question. An answered row is\n" +
			"immutable: pose a new one instead. Naming --type replaces the whole answer\n" +
			"shape (type, options and pick bounds); leaving it out keeps all four.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			onTask, key, err := cli.ParseDecisionRef(args[0])
			if err != nil {
				return err
			}
			in, err := f.input(cmd)
			if err != nil {
				return err
			}
			in.Task = task
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			d, raw, err := c.EditDecision(cmd.Context(), onTask, key, in)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DecisionRender(cmd.OutOrStdout(), d)
			return nil
		},
	}
	addDecisionFlags(cmd, &f)
	cmd.Flags().StringVar(&task, "task", "", "move the question to this task")
	return cmd
}

func init() {
	rootCmd.AddCommand(newDecisionCmd())
}
