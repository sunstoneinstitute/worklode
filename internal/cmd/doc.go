package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "Sync and list design documents (specs, ADRs, plans)",
	}
	cmd.AddCommand(newDocSyncCmd(), newDocListCmd())
	return cmd
}

func init() {
	rootCmd.AddCommand(newDocCmd())
}

// syncGate is the observed git state the default-branch gate judges (034 §3).
type syncGate struct {
	Branch        string
	DefaultBranch string
	DefaultErr    error
	Clean         bool
}

// checkSyncGate enforces spec 034 §3: without --force, sync only from the
// default branch with a clean tree. Every refusal names --force as the
// escape hatch.
func checkSyncGate(g syncGate, force bool) error {
	if force {
		return nil
	}
	if g.DefaultErr != nil {
		return fmt.Errorf("%w (or pass --force to sync from %s anyway)", g.DefaultErr, g.Branch)
	}
	if g.Branch != g.DefaultBranch {
		return fmt.Errorf("not on the default branch (%s, default %s): the store is a projection of the reviewed corpus; pass --force to push a preview", g.Branch, g.DefaultBranch)
	}
	if !g.Clean {
		return errors.New("working tree is dirty: the store is a projection of the reviewed corpus; commit (or pass --force to push a preview)")
	}
	return nil
}

// corpusToUpserts maps the loader's corpus onto the wire type 1:1.
func corpusToUpserts(docs []designdoc.CorpusDoc) []cli.DocUpsert {
	out := make([]cli.DocUpsert, 0, len(docs))
	for _, d := range docs {
		u := cli.DocUpsert{
			Kind: d.Kind, Ordinal: d.Ordinal, Status: d.Status, Title: d.Title,
			Body: string(d.Source), Frontmatter: d.FrontmatterJSON,
		}
		for _, s := range d.Sections {
			u.Sections = append(u.Sections, cli.DocSection{
				Anchor: s.Anchor, Heading: s.Heading, Depth: s.Depth, Position: s.Position,
			})
		}
		for _, e := range d.Edges {
			u.Edges = append(u.Edges, cli.DocEdge{
				SrcAnchor: e.SrcAnchor, Rel: e.Rel, Target: e.Target, TargetAnchor: e.TargetAnchor,
			})
		}
		out = append(out, u)
	}
	return out
}

func newDocSyncCmd() *cobra.Command {
	var force, dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Push the configured git corpora to the backbone (spec 034)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			corpora, err := cli.CorporaFrom(cwd)
			if err != nil {
				return err
			}
			if corpora.SpecDir == "" && corpora.PlanDir == "" {
				return errors.New(`nothing configured to sync: set spec_corpus and/or plan_corpus in .worklode/config.toml (spec 034 §2)`)
			}

			root, ok := worktree.Root(cwd)
			if !ok {
				return errors.New("not inside a git worktree")
			}
			g := syncGate{}
			if g.Branch, err = worktree.CurrentBranch(root); err != nil {
				return err
			}
			g.DefaultBranch, g.DefaultErr = worktree.DefaultBranch(root)
			if g.Clean, err = worktree.IsClean(root); err != nil {
				return err
			}
			if err := checkSyncGate(g, force); err != nil {
				return err
			}

			docs, err := designdoc.LoadSyncCorpus(corpora.SpecDir, corpora.PlanDir)
			if err != nil {
				return err
			}

			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			if cfg.CurrentProject == "" {
				return errors.New(`no project: set current_project in .worklode/config.toml`)
			}
			rep, raw, err := c.SyncDocs(cmd.Context(), cli.DocSyncInput{
				Project:      cfg.CurrentProject,
				SourceBranch: g.Branch,
				Dirty:        !g.Clean,
				Force:        force,
				DryRun:       dryRun,
				Docs:         corpusToUpserts(docs),
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			renderSyncReport(cmd, rep)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "bypass the default-branch/clean-tree gate; provenance records the source branch and dirty flag")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "report what would change without writing")
	return cmd
}

// renderSyncReport prints the per-doc outcomes and a summary line.
func renderSyncReport(cmd *cobra.Command, rep cli.DocSyncReport) {
	out := cmd.OutOrStdout()
	for _, r := range rep.Results {
		if r.Outcome != "unchanged" {
			fmt.Fprintf(out, "%-9s %s\n", r.Outcome, r.ID)
		}
	}
	verb := "synced"
	if rep.DryRun {
		verb = "would sync"
	}
	fmt.Fprintf(out, "%s %d docs: %d added, %d updated, %d unchanged\n",
		verb, rep.Added+rep.Updated+rep.Unchanged, rep.Added, rep.Updated, rep.Unchanged)
}

func newDocListCmd() *cobra.Command {
	var project, kind, status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List synced design documents from the backbone",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			if project == "" {
				project = cfg.CurrentProject
			}
			resp, raw, err := c.ListDocs(cmd.Context(), project, kind, status)
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
	cmd.Flags().StringVar(&project, "project", "", "filter by project id (default: current project)")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: spec, adr, plan")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (e.g. draft, accepted)")
	return cmd
}
