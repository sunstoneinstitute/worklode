package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// docKinds lists the valid --kind values for `lode doc new`, mirroring
// validDocKinds in internal/api/docs.go (the server re-checks; this catches a
// typo before the round trip).
var docKinds = []string{"spec", "adr", "plan"}

// resolveDocID resolves a document reference to its id (025 §14.3): a
// positive integer is the id itself, taken without a round trip; anything
// else goes to GET /api/v1/docs/resolve. It is the one resolver both `lode
// doc <ref>`'s verbs and `lode task list`'s `--plan`/`--about` call, so the
// surfaces cannot disagree about what a ref names.
//
// The grammar itself — exact slug match, the refusal of an ambiguous slug,
// the fallback to tombstoned documents that `lode doc undelete <slug>` needs
// — is the server's (store.ResolveDocRef). What is left here is the numeric
// shortcut, because an id needs no lookup to become an id, and naming the ref
// in the 404: the server's "not found" says nothing about what was tried.
func resolveDocID(ctx context.Context, c *cli.Client, ref string) (int64, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		return id, nil
	}
	d, err := c.ResolveDoc(ctx, ref)
	if err != nil {
		var cerr *cli.ClientError
		if errors.As(err, &cerr) {
			// A refusal about this ref: 404 says nothing about what was
			// tried, and the ambiguity message already names it.
			if cerr.Status == http.StatusNotFound {
				return 0, fmt.Errorf("no document found with id or slug %q", ref)
			}
			return 0, err
		}
		return 0, fmt.Errorf("resolve document %q: %w", ref, err)
	}
	return d.ID, nil
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
		newDocSubmitCmd(),
		newDocReviseCmd(),
		newDocAnchorsCmd(),
		newDocImportCmd(),
		newDocTodoCmd(),
		newDocDeleteCmd(),
		newDocUndeleteCmd(),
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
			if !slices.Contains(docKinds, kind) {
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
				return errNoProject
			}
			// The task this document is being written under (025 §12). Empty
			// outside a bound worktree, which records no authoring task
			// rather than refusing the create — a human in the cockpit and an
			// agent working ad hoc both author documents legitimately.
			d, raw, err := c.CreateDoc(cmd.Context(), model.CreateDocInput{
				Project: sc.Project, Kind: kind, Number: number, Slug: slug, Body: body, Assignee: assignee,
				GeneratedByTask: currentTaskID(),
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
	var needsPlanning, needsExecution, bareSuperseded, deleted bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List documents: specs, ADRs, and plans",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Ahead of the client: a contradicting selector is an error
			// whatever the server would say, and refusing it here costs no
			// round trip.
			if err := checkDocSelectors(kind, status, needsPlanning, needsExecution, bareSuperseded); err != nil {
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
			resp, raw, err := c.ListDocs(cmd.Context(), cli.DocListFilter{
				Project: sc.Project, Kind: kind, Status: status,
				NeedsPlanning: needsPlanning, NeedsExecution: needsExecution, BareSuperseded: bareSuperseded,
				Deleted: deleted,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			switch {
			case needsPlanning:
				cli.DocPlanningTable(cmd.OutOrStdout(), resp.Docs, resp.PlanningGaps)
			case bareSuperseded:
				cli.DocSupersessionTable(cmd.OutOrStdout(), resp.Docs, resp.SupersessionGaps)
			default:
				cli.DocTable(cmd.OutOrStdout(), resp.Docs)
			}
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "filter by project id")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: spec, adr, plan")
	cmd.Flags().StringVar(&status, "status", "", "filter by status: draft, accepted, superseded")
	cmd.Flags().BoolVar(&needsPlanning, "needs-planning", false,
		"accepted specs with a section no accepted plan covers")
	cmd.Flags().BoolVar(&needsExecution, "needs-execution", false,
		"accepted plans whose task set has an open task")
	cmd.Flags().BoolVar(&bareSuperseded, "bare-superseded", false,
		"superseded documents with a section nothing replaces")
	cmd.Flags().BoolVar(&deleted, "deleted", false,
		"list deleted documents instead of live ones (044 §5)")
	cmd.MarkFlagsMutuallyExclusive("needs-planning", "needs-execution", "bare-superseded")
	return cmd
}

// checkDocSelectors refuses a --kind or --status that contradicts one of the
// derived selectors (026 §2.1, §2.4): each implies a status, and
// needs-planning/needs-execution each imply a single kind while
// bare-superseded implies one of two (spec or adr — a plan carries no
// sections, 025 §6 rule 2). A contradicting restatement would make the
// conjunction always empty, which would read as "nothing to plan"; only a
// contradiction is refused, restating the implied value is fine. The server
// enforces the same rule for clients that are not this one; the mutual
// exclusion of the three selectors themselves is cobra's, declared on the
// command.
func checkDocSelectors(kind, status string, needsPlanning, needsExecution, bareSuperseded bool) error {
	for _, c := range []struct {
		on       bool
		flag     string
		status   string
		kindOK   func(string) bool
		kindWant string
	}{
		{needsPlanning, "--needs-planning", "accepted", func(k string) bool { return k == "spec" }, "spec"},
		{needsExecution, "--needs-execution", "accepted", func(k string) bool { return k == "plan" }, "plan"},
		{bareSuperseded, "--bare-superseded", "superseded",
			func(k string) bool { return k == "spec" || k == "adr" }, "spec or adr"},
	} {
		if !c.on {
			continue
		}
		if kind != "" && !c.kindOK(kind) {
			return fmt.Errorf("%s implies --kind %s; drop --kind or pass %s", c.flag, c.kindWant, c.kindWant)
		}
		if status != "" && status != c.status {
			return fmt.Errorf("%s implies --status %s; drop --status or pass %s", c.flag, c.status, c.status)
		}
	}
	return nil
}

// newDocAnchorsCmd is the author's local pre-accept lint (025 §18, §10): it
// parses a markdown file and reports every anchor defect the backbone would
// refuse — duplicate anchors, an anchor disagreeing with its heading number,
// and a section deeper than designdoc.DepthLimit — plus, for a plan, the
// errors designdoc.PlanTasks reports. No server is involved, so it runs on a
// file that has never been posted.
func newDocAnchorsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "anchors <file>",
		Short: "Lint a markdown file's anchors (and, for a plan, its task definitions)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			doc, err := designdoc.Parse(src)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			findings := lintDocFile(doc)
			out := cmd.OutOrStdout()
			if jsonOut(cmd) {
				if findings == nil {
					findings = []string{}
				}
				return json.NewEncoder(out).Encode(docAnchorsReport{
					File: path, Plan: isPlanFile(doc), Findings: findings,
				})
			}
			if len(findings) == 0 {
				fmt.Fprintf(out, "%s: no problems\n", path)
				return nil
			}
			for _, f := range findings {
				fmt.Fprintf(out, "%s: %s\n", path, f)
			}
			return fmt.Errorf("%s: %d problem(s)", path, len(findings))
		},
	}
	return cmd
}

// docAnchorsReport is `lode doc anchors --json`'s stdout contract. Findings is
// empty when the file is clean; Plan says whether the plan-task check ran.
type docAnchorsReport struct {
	File     string   `json:"file"`
	Plan     bool     `json:"plan"`
	Findings []string `json:"findings"`
}

// lintDocFile collects every finding for one parsed file. The anchor lint and
// the depth gate are the store's own accept-time checks (designdoc.LintAnchors
// and designdoc.DepthViolations), reused rather than restated. The rest of the
// accept-time diff needs a prior version, which a file on disk does not have.
func lintDocFile(doc *designdoc.Document) []string {
	findings := designdoc.LintAnchors(doc)
	findings = append(findings, designdoc.DepthViolations(doc, designdoc.DepthLimit)...)
	if isPlanFile(doc) {
		if _, err := designdoc.PlanTasks(doc); err != nil {
			findings = append(findings, err.Error())
		}
	}
	return findings
}

// isPlanFile reports whether the file identifies itself as a plan. There is no
// server round trip here, so the frontmatter is the only evidence: an explicit
// `kind: plan`, or one of the keys only a plan carries — `covers`/`implements`
// (026 §5) and the plan-ordering `blocks`/`blockedBy` (025 §5). A file that
// says nothing is not treated as a plan: skipping the plan-task check is
// better than guessing one onto a spec whose "## Tasks" heading is prose.
func isPlanFile(doc *designdoc.Document) bool {
	fm := doc.Frontmatter
	if fm == nil {
		return false
	}
	return fm.Kind == "plan" || len(fm.CoverageEntries()) > 0 ||
		len(fm.Blocks) > 0 || len(fm.BlockedBy) > 0
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
		Short: "Accept a document (draft -> accepted, or a plan again to mint what it declares); only the assignee may accept it",
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

// newDocSubmitCmd puts a document up for review. Submission is an event, not a
// status (025 §15.4): the document does not move, and what the event means —
// minting a review task, say — is the doc-lifecycle watcher's to decide.
func newDocSubmitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit <id-or-slug>",
		Short: "Submit a document for review (records a review event; the document's status does not change)",
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
			d, raw, err := c.SubmitDoc(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "submitted doc %d for review\n", d.ID)
			return nil
		},
	}
	return cmd
}

// newDocDeleteCmd is `lode doc delete` (044 §5): tombstone a document that
// should not have existed — a wrong corpus number, a duplicate import. The
// row and its events survive; only the ways of finding it stop. Whether the
// justification is required depends on the instance environment and is the
// server's call (044 §3), so nothing is validated or prompted for here.
func newDocDeleteCmd() *cobra.Command {
	var justification string
	cmd := &cobra.Command{
		Use:   "delete <id-or-slug>",
		Short: "Delete a document: hide a row that should not have existed",
		Long: "Delete a document. The row is tombstoned, not removed: its events stay\n" +
			"in the log, references to it still resolve, and `lode doc undelete`\n" +
			"restores it. A prod instance refuses a delete carrying no\n" +
			"--justification.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			d, raw, err := c.DeleteDoc(cmd.Context(), id, justification)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted doc %d: %s\n", d.ID, d.Slug)
			if d.Tombstone != nil && d.Tombstone.Justification != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "reason: %s\n", d.Tombstone.Justification)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&justification, "justification", "",
		"why this document should not have existed (required on a prod instance)")
	return cmd
}

// newDocUndeleteCmd clears a document's tombstone. No justification on either
// instance — only hiding a record is worth making someone stop and type
// (044 §3).
func newDocUndeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "undelete <id-or-slug>",
		Short: "Restore a deleted document, clearing its tombstone",
		Long: "Restore a deleted document. A slug resolves here even though the\n" +
			"document has left every live list, because the lookup falls back to\n" +
			"the tombstoned ones; `lode doc list --deleted` shows what is there.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			d, raw, err := c.UndeleteDoc(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "undeleted doc %d: %s\n", d.ID, d.Slug)
			return nil
		},
	}
	return cmd
}

// newDocReviseCmd is one command over the four candidate-revision verbs
// (025 §7.2): bare opens a candidate, --file updates its body, --accept lands
// it as the document's next version, --discard withdraws it without landing.
// The three flags are mutually exclusive — landing a body written in the same
// breath would skip the read a candidate revision exists for, and discarding
// one in the same breath as editing or landing it is incoherent.
func newDocReviseCmd() *cobra.Command {
	var file string
	var accept, discard bool
	cmd := &cobra.Command{
		Use:   "revise <id-or-slug>",
		Short: "Open, update, land, or discard a document's candidate revision",
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

			var raw []byte
			var msg string
			switch {
			case accept:
				d, r, err := c.AcceptDocRevision(cmd.Context(), id)
				if err != nil {
					return err
				}
				raw, msg = r, fmt.Sprintf("accepted revision on doc %d: now version %d", d.ID, d.Version)
			case discard:
				d, r, err := c.DiscardDocRevision(cmd.Context(), id)
				if err != nil {
					return err
				}
				raw, msg = r, fmt.Sprintf("discarded the candidate revision on doc %d", d.ID)
			case cmd.Flags().Changed("file"):
				body, err := readBodyFile(cmd, file)
				if err != nil {
					return err
				}
				rev, r, err := c.UpdateDocRevision(cmd.Context(), id, body)
				if err != nil {
					return err
				}
				raw, msg = r, fmt.Sprintf("updated candidate revision on doc %d", rev.Doc)
			default:
				rev, r, err := c.ReviseDoc(cmd.Context(), id)
				if err != nil {
					return err
				}
				raw, msg = r, fmt.Sprintf("opened a candidate revision on doc %d", rev.Doc)
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", `replace the open candidate's body with this file ("-" for stdin)`)
	cmd.Flags().BoolVar(&accept, "accept", false, "land the open candidate as the document's next version")
	cmd.Flags().BoolVar(&discard, "discard", false, "withdraw the open candidate without landing it")
	cmd.MarkFlagsMutuallyExclusive("file", "accept", "discard")
	return cmd
}
