package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
)

// runDeriveLocal computes the repo-local observed documents (go-imports,
// repo-layout) for the repo at root. With dryRun it returns the rendered
// N-Triples; otherwise it Runs each through the deriver contract against c,
// passing opts through. A repo that is not a Go module derives layout only
// (reported inline). A document with no triples is called out either way —
// legitimate for some sources, a broken input for the rest, and invisible
// otherwise.
func runDeriveLocal(ctx context.Context, root, host, owner, name string, dryRun bool, c *graphserver.Client, opts derive.Options) (string, error) {
	manPath := filepath.Join(root, ".worklode", "components.yaml")
	data, err := os.ReadFile(manPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w (spec 007 §1: every derived repo needs a component-boundary manifest)", manPath, err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", manPath, err)
	}

	docs := map[string][]byte{} // observed source → document
	layout, err := derive.LayoutTriples(ctx, root, host, owner, name, m)
	if err != nil {
		return "", err
	}
	docs["repo-layout"] = layout

	var notes []string
	if stream, err := derive.GoListDeps(ctx, root); err != nil {
		notes = append(notes, fmt.Sprintf("go-imports skipped: %v", err))
	} else {
		imports, err := derive.ImportsTriples(stream, root, m)
		if err != nil {
			return "", err
		}
		docs["go-imports"] = imports
	}

	var b strings.Builder
	for _, source := range []string{"go-imports", "repo-layout"} {
		doc, ok := docs[source]
		if !ok {
			continue
		}
		graph := iri.RepoObservedGraph(source, host, owner, name)
		if dryRun {
			if len(doc) == 0 {
				fmt.Fprintf(&b, "# %s\n# (empty: the deriver produced no triples)\n", graph)
				continue
			}
			fmt.Fprintf(&b, "# %s\n%s", graph, doc)
			continue
		}
		res, err := derive.Run(ctx, c, graph, doc, opts)
		if errors.Is(err, derive.ErrWouldEmptyGraph) {
			return b.String(), fmt.Errorf("%w; re-run with --allow-empty to write it anyway", err)
		}
		if err != nil {
			return b.String(), err
		}
		fmt.Fprintf(&b, "%s: hash=%s skipped=%v empty=%v\n", res.Graph, res.Hash, res.Skipped, res.Empty)
	}
	for _, n := range notes {
		fmt.Fprintln(&b, n)
	}
	return b.String(), nil
}

// gitRemoteOrigin returns the origin remote URL of the repo at dir, or ""
// when dir is not in a git repo, has no origin, git is not installed, or ctx
// ends first — a hung git must not hang the command. Mirrors the unexported
// gitRemoteURL in internal/cli/gitremote.go (unexported in another package,
// so copied here rather than shared).
func gitRemoteOrigin(ctx context.Context, dir string) string {
	out, err := gitexec.CmdContext(ctx, dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// newDeriveCmd wires `lode derive`: run the repo-local derivers from a
// checkout, in CI or by hand. The server-side derivers (pr-affects, deploy)
// have no checkout to read and run through POST /api/v1/derive instead, which
// is what --server asks for.
func newDeriveCmd() *cobra.Command {
	var graphURL string
	var dryRun bool
	var allowEmpty bool
	var server bool
	cmd := &cobra.Command{
		Use:   "derive",
		Short: "Run the repo-local observed-layer derivers (go-imports, repo-layout), or --server for the server-side ones",
		Long: `Run the repo-local observed-layer derivers (go-imports, repo-layout) over
the current checkout and write the resulting graphs.

With --server, run the server-side derivers (pr-affects, deploy) through
POST /api/v1/derive instead. Those read the backbone's own facts rather than a
checkout, so the repo-local flags (--dry-run, --graph-url, --allow-empty) do
not apply to them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if server {
				return runDeriveServer(cmd)
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			// The manifest and the walk are rooted at the repo, so a run from
			// a subdirectory must resolve upwards. A non-git cwd falls through
			// and fails on the missing manifest, as before.
			root, ok := gitexec.Line(cwd, "rev-parse", "--show-toplevel")
			if !ok {
				root = cwd
			}
			remote := gitRemoteOrigin(cmd.Context(), root)
			coord, err := repourl.Normalize(remote)
			if err != nil {
				return fmt.Errorf("resolve repo from origin remote %q: %w", remote, err)
			}
			owner, name, _ := strings.Cut(coord, "/")

			var c *graphserver.Client
			if !dryRun {
				switch {
				case graphURL != "":
					c = graphserver.New(graphURL, nil)
				case os.Getenv("LODE_GRAPHSERVER_URL") != "":
					if c, err = graphserver.FromEnv(); err != nil {
						return err
					}
				default:
					return errors.New("no graph endpoint: set --graph-url or LODE_GRAPHSERVER_URL (or use --dry-run)")
				}
			}
			out, err := runDeriveLocal(cmd.Context(), root, "github.com", owner, name, dryRun, c,
				derive.Options{AllowEmpty: allowEmpty})
			fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	cmd.Flags().StringVar(&graphURL, "graph-url", "", "graph-server base URL, unauthenticated (default: the LODE_GRAPHSERVER_* env via graphserver.FromEnv)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the N-Triples instead of writing")
	cmd.Flags().BoolVar(&allowEmpty, "allow-empty", false,
		"let a deriver that produced no triples replace a graph that currently holds content (refused by default: an empty result is usually broken inputs)")
	cmd.Flags().BoolVar(&server, "server", false,
		"run the server-side derivers (pr-affects, deploy) through POST /api/v1/derive instead of the repo-local ones")
	// The other three are repo-local concepts: there is no checkout to dry-run
	// against and no client-side graph endpoint when the server derives.
	cmd.MarkFlagsMutuallyExclusive("server", "dry-run")
	cmd.MarkFlagsMutuallyExclusive("server", "graph-url")
	cmd.MarkFlagsMutuallyExclusive("server", "allow-empty")
	return cmd
}

// runDeriveServer is `lode derive --server`: ask the backbone to run its own
// derivers and report what each one wrote.
func runDeriveServer(cmd *cobra.Command) error {
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	resp, raw, err := c.RunDerive(cmd.Context())
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		printRaw(cmd, raw)
		return nil
	}
	cli.DeriveResultTable(cmd.OutOrStdout(), resp.Results)
	return nil
}

// newOverviewCmd wires `lode overview` — the one-screen roll-up. `lode
// status` is a different view and stays as it is.
func newOverviewCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "One-screen roll-up: drift counts, gaps, frontier, critical head",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.Overview(cmd.Context(), sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.OverviewRender(cmd.OutOrStdout(), resp)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "roll up one project")
	return cmd
}

// newDriftCmd wires `lode drift [--component <iri>] [--acknowledged]`.
// --component filters client-side, so it also suppresses the raw --json
// passthrough: a filtered payload is a shape the server never sent.
func newDriftCmd() *cobra.Command {
	var acknowledged bool
	var component string
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Architectural drift: violations and stale intent (spec 007 §3.1)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			resp, raw, err := c.Drift(cmd.Context(), acknowledged)
			if err != nil {
				return err
			}
			if jsonOut(cmd) && component == "" {
				printRaw(cmd, raw)
				return nil
			}
			cli.DriftRender(cmd.OutOrStdout(), resp, component, acknowledged)
			return nil
		},
	}
	cmd.Flags().BoolVar(&acknowledged, "acknowledged", false, "include accepted deviations (active + expired)")
	cmd.Flags().StringVar(&component, "component", "", "filter edges from this component IRI")
	return cmd
}

// newGapsCmd wires `lode gaps` (spec 007 §3.2).
func newGapsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gaps",
		Short: "Doc gaps and unmatched-path coverage gaps",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			resp, raw, err := c.Gaps(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.GapTable(cmd.OutOrStdout(), resp.Gaps)
			return nil
		},
	}
}

// newFrontierCmd wires `lode frontier` (alias `ready`): the ranked ready
// set, pre-sorted by the D9 ordering the backbone computes (spec 007 §3.4).
func newFrontierCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:     "frontier",
		Aliases: []string{"ready"},
		Short:   "Ready, unblocked tasks in pickup order",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.Frontier(cmd.Context(), sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.FrontierTable(cmd.OutOrStdout(), resp.Tasks)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "list one project's frontier")
	return cmd
}

// newCriticalPathCmd wires `lode critical-path [--task <id>]`; cycles are
// findings, not silent drops (spec 007 §Cycle handling). --task narrows the
// table to that task's row (its depth and fan-out), client-side, which is why
// it suppresses the raw --json passthrough.
func newCriticalPathCmd() *cobra.Command {
	var task string
	cmd := &cobra.Command{
		Use:   "critical-path",
		Short: "Estimate-free critical path over blocks + requires (D12)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			resp, raw, err := c.CriticalPath(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) && task == "" {
				printRaw(cmd, raw)
				return nil
			}
			cli.CriticalPathRender(cmd.OutOrStdout(), resp, task)
			return nil
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "show only this task's criticality")
	return cmd
}

func init() {
	rootCmd.AddCommand(newDeriveCmd(), newOverviewCmd(), newDriftCmd(),
		newGapsCmd(), newFrontierCmd(), newCriticalPathCmd())
}
