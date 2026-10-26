package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newDeriveCmd is `lode doc|task|rule derive` (WL-REQ-1860): normalize the
// selected nodes' inline references and re-extract their rows. The server
// decides what changes; this sends the selection and renders the report.
func newDeriveCmd(kind model.NodeKind) *cobra.Command {
	var scope scopeFlags
	var statuses []string
	var nodeKind string
	var all, dryRun, diff, classify, reclassify, stats bool
	cmd := &cobra.Command{
		Use:   "derive [<ref>...]",
		Short: fmt.Sprintf("Normalize and re-extract the inline references of %ss", kind),
		Long: fmt.Sprintf(`Rewrite the inline references in the text of the selected %ss into plain
form, then re-derive their reference rows. Name refs, give a filter, or pass
--all. --dry-run and --diff report what would change and write nothing.
--classify also asks the jev classifier which relation each unclassified
reference states, and writes the typed edge it picks; --reclassify re-asks
those an older classifier version answered. --stats writes nothing and
reports how the selected %ss' classifications fared, by classifier version,
predicate and confidence bucket.`, kind, kind),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && len(statuses) == 0 && nodeKind == "" && !all {
				return errors.New("select nodes: name refs, or pass --status, --kind or --all")
			}
			if reclassify && !classify {
				return errors.New("--reclassify needs --classify")
			}
			if stats && (classify || dryRun || diff) {
				return errors.New("--stats cannot combine with --classify, --dry-run or --diff")
			}
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			if sc.Project == "" {
				return errNoProject
			}
			if stats {
				st, raw, err := c.RefStats(cmd.Context(), sc.Project, model.RefStatsParams{Kind: kind, Refs: args, Status: statuses,
					DocKind: kindFilter(kind, "doc", nodeKind), TaskKind: kindFilter(kind, "task", nodeKind), All: all})
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
				} else {
					cli.RefStatsTable(cmd.OutOrStdout(), st)
				}
				return nil
			}
			in := model.RefDeriveParams{Kind: kind, Refs: args, Status: statuses, All: all, DryRun: dryRun, Diff: diff,
				Classify: classify, Reclassify: reclassify}
			switch kind {
			case "doc":
				in.DocKind = nodeKind
			case "task":
				in.TaskKind = nodeKind
			}
			rep, raw, err := c.DeriveRefs(cmd.Context(), sc.Project, in)
			if err != nil {
				return err
			}
			switch {
			case jsonOut(cmd):
				printRaw(cmd, raw)
			case diff:
				cli.RefDeriveDiff(cmd.OutOrStdout(), rep)
			default:
				cli.RefDeriveTable(cmd.OutOrStdout(), rep)
			}
			var failed []string
			for _, n := range rep.Nodes {
				if n.Error != "" {
					failed = append(failed, n.Ref)
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("%d failed: %s", len(failed), strings.Join(failed, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&statuses, "status", nil, fmt.Sprintf("only %ss in this status (repeatable)", kind))
	if kind != "rule" {
		cmd.Flags().StringVar(&nodeKind, "kind", "", fmt.Sprintf("only %ss of this kind", kind))
	}
	cmd.Flags().BoolVar(&all, "all", false, fmt.Sprintf("every %s in the project", kind))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would change, write nothing")
	cmd.Flags().BoolVar(&diff, "diff", false, "print the text and reference changes, write nothing")
	cmd.Flags().BoolVar(&classify, "classify", false, "also run the jev classifier on references it has not classified")
	cmd.Flags().BoolVar(&stats, "stats", false, "report the classification quality of the selected nodes, write nothing")
	cmd.Flags().BoolVar(&reclassify, "reclassify", false, "with --classify: also re-run references an older classifier version answered")
	addScopeFlags(cmd, &scope, "project id")
	return cmd
}

// kindFilter is nodeKind when the command's kind is want, else empty.
func kindFilter(kind, want model.NodeKind, nodeKind string) string {
	if kind == want {
		return nodeKind
	}
	return ""
}
