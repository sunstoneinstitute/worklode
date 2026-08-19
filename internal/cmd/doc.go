package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// docKinds lists the valid --kind values for `lode doc new`, mirroring
// validDocKinds in internal/api/docs.go (the server re-checks; this catches a
// typo before the round trip).
var docKinds = []string{"spec", "adr", "plan"}

func validDocKind(k string) bool {
	for _, v := range docKinds {
		if v == k {
			return true
		}
	}
	return false
}

// resolveDocID resolves a document reference to its id (025 §14.3): a
// positive integer is the id itself, taken without a round trip; anything
// else is matched against every document's slug over GET /api/v1/docs
// (exact match only — corpus-number and SPEC/ADR shorthand resolution stay
// unbuilt). It is the one resolver both `lode doc <ref>`'s verbs and `lode
// task list --plan` call, so the two surfaces cannot disagree about what a
// ref names. An unmatched or ambiguous slug is an error naming what was
// tried.
func resolveDocID(ctx context.Context, c *cli.Client, ref string) (int64, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		return id, nil
	}
	resp, _, err := c.ListDocs(ctx, cli.DocListFilter{})
	if err != nil {
		return 0, fmt.Errorf("resolve document %q: %w", ref, err)
	}
	var matches []int64
	for _, d := range resp.Docs {
		if d.Slug == ref {
			matches = append(matches, d.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return 0, fmt.Errorf("no document found with id or slug %q", ref)
	default:
		return 0, fmt.Errorf("slug %q matches %d documents; pass a numeric id to disambiguate", ref, len(matches))
	}
}

func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "Create and inspect design documents: specs, ADRs, and plans",
	}
	cmd.AddCommand(
		newDocNewCmd(),
		newDocListCmd(),
		newDocGetCmd(),
		newDocEditCmd(),
		newDocAcceptCmd(),
		newDocReviseCmd(),
	)
	return cmd
}

func init() {
	rootCmd.AddCommand(newDocCmd())
}

func newDocNewCmd() *cobra.Command {
	var scope scopeFlags
	var kind, slug, assignee, file string
	var number int
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a document (spec, ADR, or plan) in draft",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validDocKind(kind) {
				return fmt.Errorf("unknown kind %q; valid kinds: %s", kind, strings.Join(docKinds, ", "))
			}
			body, err := readBodyFile(cmd, file)
			if err != nil {
				return err
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
				return fmt.Errorf(`no project: pass --project or --repo, set current_project in .worklode/config.toml or ~/.config/worklode/config.toml, or map this repo with "lode project add-repo"`)
			}
			d, raw, err := c.CreateDoc(cmd.Context(), model.CreateDocInput{
				Project: sc.Project, Kind: kind, Number: number, Slug: slug, Body: body, Assignee: assignee,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DocTable(cmd.OutOrStdout(), []model.Doc{d})
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id")
	cmd.Flags().StringVar(&kind, "kind", "", "document kind: spec, adr, or plan (required)")
	cmd.Flags().StringVar(&slug, "slug", "", "document slug (required)")
	cmd.Flags().IntVar(&number, "number", 0, "corpus number (omit for a plan, which carries none)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "actor id to assign the document to (default: yourself)")
	cmd.Flags().StringVar(&file, "file", "", `markdown source file, frontmatter included ("-" for stdin) (required)`)
	cmd.MarkFlagRequired("kind")
	cmd.MarkFlagRequired("slug")
	cmd.MarkFlagRequired("file")
	return cmd
}

func newDocListCmd() *cobra.Command {
	var scope scopeFlags
	var kind, status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List documents: specs, ADRs, and plans",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.ListDocs(cmd.Context(), cli.DocListFilter{
				Project: sc.Project, Kind: kind, Status: status,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DocTable(cmd.OutOrStdout(), resp.Docs)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "filter by project id")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: spec, adr, plan")
	cmd.Flags().StringVar(&status, "status", "", "filter by status: draft, accepted, superseded")
	return cmd
}

// newDocGetCmd reads back one document: body, sections, and edges. It is
// named "get" rather than "show" deliberately: 026 §3 consolidated document
// reading into `lode show`, and internal/cmd/show_test.go's
// TestDocHasNoShowVerb pins that `lode doc` must never grow a "show" child.
// `lode doc`'s write verbs need a read to be usable on their own, and `lode
// show` cannot reach a backbone document yet — its resolver is
// filesystem-based (026 §0). Extending it is part 3's job, tracked as
// WL-129.
func newDocGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id-or-slug>",
		Short: "Get a document: its body, sections, and edges",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			d, raw, err := c.GetDoc(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DocDetailRender(cmd.OutOrStdout(), d)
			return nil
		},
	}
	return cmd
}

func newDocEditCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "edit <id-or-slug>",
		Short: "Replace a document's body (a draft, or a plan at any status)",
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
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			d, raw, err := c.UpdateDocBody(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DocTable(cmd.OutOrStdout(), []model.Doc{d})
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", `markdown source file, frontmatter included ("-" for stdin) (required)`)
	cmd.MarkFlagRequired("file")
	return cmd
}

func newDocAcceptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accept <id-or-slug>",
		Short: "Accept a document (draft -> accepted); only the assignee may accept it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			d, raw, err := c.AcceptDoc(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "accepted doc %d: status %s\n", d.ID, d.Status)
			if len(d.Tasks) > 0 {
				ids := make([]string, len(d.Tasks))
				for i, task := range d.Tasks {
					ids[i] = task.ID
				}
				fmt.Fprintf(cmd.OutOrStdout(), "minted tasks: %s\n", strings.Join(ids, ", "))
			}
			return nil
		},
	}
	return cmd
}

// newDocReviseCmd is one command over the three candidate-revision verbs
// (025 §7.2): bare opens a candidate, --file updates its body, --accept lands
// it as the document's next version. --file and --accept together is refused
// by MarkFlagsMutuallyExclusive — landing a body written in the same breath
// would skip the read a candidate revision exists for.
func newDocReviseCmd() *cobra.Command {
	var file string
	var accept bool
	cmd := &cobra.Command{
		Use:   "revise <id-or-slug>",
		Short: "Open, update, or land a document's candidate revision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}

			if accept {
				d, raw, err := c.AcceptDocRevision(cmd.Context(), id)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "accepted revision on doc %d: now version %d\n", d.ID, d.Version)
				return nil
			}

			if cmd.Flags().Changed("file") {
				body, err := readBodyFile(cmd, file)
				if err != nil {
					return err
				}
				rev, raw, err := c.UpdateDocRevision(cmd.Context(), id, body)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "updated candidate revision on doc %d\n", rev.Doc)
				return nil
			}

			rev, raw, err := c.ReviseDoc(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "opened a candidate revision on doc %d\n", rev.Doc)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", `replace the open candidate's body with this file ("-" for stdin)`)
	cmd.Flags().BoolVar(&accept, "accept", false, "land the open candidate as the document's next version")
	cmd.MarkFlagsMutuallyExclusive("file", "accept")
	return cmd
}
