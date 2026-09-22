package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newClauseCmd is the design-clause entity group (12-spec-refactoring-design-tree.md
// S14, S35). Reading one clause is `lode show WL-CL-<n>`; there is no `lode
// clause show`.
func newClauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clause",
		Short: "Design clauses: edit one, list its versions",
	}
	cmd.AddCommand(newClauseEditCmd(), newClauseVersionsCmd())
	return cmd
}

func init() {
	rootCmd.AddCommand(newClauseCmd())
}

func newClauseEditCmd() *cobra.Command {
	var file, heading string
	cmd := &cobra.Command{
		Use:   "edit <ref>",
		Short: "Replace a clause's body (and heading) from a file; the document is regenerated around it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readBodyFile(cmd, file)
			if err != nil {
				return err
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			// h resolves locally rather than reassigning the closure-captured
			// heading flag variable: RunE can run more than once in a
			// process (tests), and mutating heading would leak the first
			// run's resolved value into the second.
			h := heading
			if h == "" {
				current, _, err := c.GetClause(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				h = current.Heading
			}
			clause, raw, err := c.EditClause(cmd.Context(), args[0], model.EditClauseInput{Heading: strings.TrimSpace(h), Body: body})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ClauseRender(cmd.OutOrStdout(), clause)
			// An edit of an accepted document goes to its candidate
			// revision, so the clause read back is still the old text and
			// the render above looks like nothing happened (S13, S35). Say
			// where the change is waiting.
			if clause.Status == "accepted" && strings.TrimSpace(clause.Body) != strings.TrimSpace(body) && len(clause.ArrangedIn) == 1 {
				ref := clause.ArrangedIn[0].DocRef
				fmt.Fprintf(cmd.OutOrStdout(), "\nSaved to the candidate revision of %s; it lands when you run lode doc revise %s --accept.\n", ref, ref)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", `file holding the new body, the text under the heading ("-" for stdin) (required)`)
	cmd.Flags().StringVar(&heading, "heading", "", "new heading text; unchanged when omitted")
	return cmd
}

func newClauseVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "versions <ref>",
		Short: "List a clause's versions, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			vs, raw, err := c.ListClauseVersions(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ClauseVersionsTable(cmd.OutOrStdout(), vs)
			return nil
		},
	}
}
