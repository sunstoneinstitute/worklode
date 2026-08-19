package cmd

import (
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

// resolveDocID parses arg as a document id. A backbone document reference can
// also be a number or a slug (025 §14.3), but resolving those is part 3's job
// (the plan that extends this command group); this version takes a numeric
// id only.
func resolveDocID(arg string) (int64, error) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%q is not a document id: this version of `lode doc` takes a numeric id only, not a number, slug, or SPEC/ADR reference", arg)
	}
	return id, nil
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
		Use:   "get <id>",
		Short: "Get a document: its body, sections, and edges",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveDocID(args[0])
			if err != nil {
				return err
			}
			c, err := newAPIClient()
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
		Use:   "edit <id>",
		Short: "Replace a document's body (a draft, or a plan at any status)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveDocID(args[0])
			if err != nil {
				return err
			}
			body, err := readBodyFile(cmd, file)
			if err != nil {
				return err
			}
			c, err := newAPIClient()
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
		Use:   "accept <id>",
		Short: "Accept a document (draft -> accepted); only the assignee may accept it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveDocID(args[0])
			if err != nil {
				return err
			}
			c, err := newAPIClient()
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
		Use:   "revise <id>",
		Short: "Open, update, or land a document's candidate revision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveDocID(args[0])
			if err != nil {
				return err
			}
			c, err := newAPIClient()
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
