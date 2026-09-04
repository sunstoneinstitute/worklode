package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// searchKinds are the subject kinds GET /api/v1/search indexes (040 §9).
var searchKinds = []string{"doc", "task", "skill"}

// newSearchCmd builds `lode search`, the corpus-wide hybrid search (040 §9).
//
// It is one of the two cross-entity readers 061 §1 L7 puts at the top level:
// it answers over documents, tasks and skills at once, so no
// `lode <entity> <verb>` spelling (L1) is true of it. `lode show` is the
// other, and the split is what the caller supplies — show resolves one known
// reference to one subject, search takes an unknown one and returns a ranking.
func newSearchCmd() *cobra.Command {
	var (
		kinds []string
		mode  string
		limit int
		scope scopeFlags
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search documents, tasks and skills by meaning and by exact token",
		Long: "Search the indexed corpus (040 §9). Two retrieval arms run and their\n" +
			"rankings are fused: one over embeddings, which answers a question phrased\n" +
			"differently from the text, and one lexical, which answers an identifier\n" +
			"query the embedding cannot. Every argument joins into one query, so a\n" +
			"question needs no quoting.\n\n" +
			"Each line is an address to act on, its fused score, and the subject's\n" +
			"title:\n\n" +
			"  WL-SPEC-25 §15.2  0.032  The ordered log\n\n" +
			"A server with no embedding provider answers with the lexical arm alone\n" +
			"and says so on stderr; the results are real, just narrower.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.Search(cmd.Context(), cli.SearchFilter{
				Query:   strings.Join(args, " "),
				Kinds:   kinds,
				Mode:    mode,
				Limit:   limit,
				Project: sc.Project,
			})
			if err != nil {
				return err
			}
			// A degraded instance is a notice, never a failure: it answered.
			cli.SearchNotice(cmd.ErrOrStderr(), resp)
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			// The document ids a hit carries are row ids; the reference a
			// reader cites costs one list request, and only when the results
			// hold a document at all.
			var docRefs map[int64]string
			for _, h := range resp.Hits {
				if h.Kind == "doc" {
					docRefs = c.DocRefs(cmd.Context(), sc.Project)
					break
				}
			}
			cli.SearchTable(cmd.OutOrStdout(), resp.Hits, docRefs)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&kinds, "kind", nil,
		"limit to these subject kinds: doc, task, skill (repeatable; default all three)")
	completeFlagValues(cmd, "kind", searchKinds)
	cmd.Flags().StringVar(&mode, "mode", "",
		"retrieval mode: hybrid (default), dense or lexical — for comparing the arms on a real query")
	cmd.Flags().IntVar(&limit, "limit", 0, "max hits to return (default 20)")
	addScopeFlags(cmd, &scope, "search only this project's corpus")
	return cmd
}

func init() { rootCmd.AddCommand(newSearchCmd()) }
