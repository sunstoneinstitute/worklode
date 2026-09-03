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
	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
)

func newGraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "The knowledge-graph projection: its health, and what it owes",
	}
	cmd.AddCommand(newGraphProjectionCmd())
	cmd.AddCommand(newGraphTriplesCmd())
	cmd.AddCommand(newGraphDriftCmd())
	cmd.AddCommand(newGraphGapsCmd())
	cmd.AddCommand(newGraphDeriveCmd())
	return cmd
}

func init() { rootCmd.AddCommand(newGraphCmd()) }

func newGraphProjectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projection",
		Short: "The backbone→graph projector's state",
	}
	cmd.AddCommand(newGraphProjectionStatusCmd())
	return cmd
}

func newGraphProjectionStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the projects the projector has quarantined, since when, and why",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.ProjectionFailures(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ProjectionFailureTable(cmd.OutOrStdout(), resp.Failures)
			return nil
		},
	}
}

// newGraphTriplesCmd wires `lode graph triples`: the task graph — tasks,
// projects, and the edges between them — as N-Triples, for loading into an
// external RDF store and querying with SPARQL. Named "triples", not "export":
// internal/cmd/CLAUDE.md's Naming L3 closes the set of non-CRUD verbs a
// command may use, and "export" is not in it; "triples" is a named view (L6),
// the same pattern as the existing "graph projection status".
//
// internal/graphproj already builds and serializes these triples (spec 006
// §11) for the server-side projector; this command is the first thing that
// writes them to a file or stdout instead.
func newGraphTriplesCmd() *cobra.Command {
	var scope scopeFlags
	var output string
	cmd := &cobra.Command{
		Use:   "triples",
		Short: "Write the task graph as N-Triples, for loading into an external RDF store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}

			projResp, _, err := c.ListProjects(cmd.Context())
			if err != nil {
				return err
			}
			projects := projResp.Projects

			filter := cli.TaskListFilter{Project: sc.Project}
			if sc.Project != "" {
				projects = nil
				for _, p := range projResp.Projects {
					if p.ID == sc.Project {
						projects = []model.Project{p}
						break
					}
				}
				if projects == nil {
					return fmt.Errorf("project %s not found", sc.Project)
				}
			}

			// Not filtered by state: a done or released task must stay in
			// the graph, or a resolved blocker reads as an open one.
			tasks, _, err := c.ListTasksDetail(cmd.Context(), filter)
			if err != nil {
				return err
			}

			var triples []graphproj.Triple
			for _, p := range projects {
				triples = append(triples, graphproj.ProjectTriples(p)...)
			}
			for _, t := range tasks.Tasks {
				out, in := taskListDetailEdges(t)
				triples = append(triples, graphproj.TaskTriples(t.Task, out, in)...)
			}
			doc := graphproj.Document(triples)

			if output != "" {
				return os.WriteFile(output, doc, 0o644)
			}
			_, err = cmd.OutOrStdout().Write(doc)
			return err
		},
	}
	addScopeFlags(cmd, &scope, "limit the export to one project id")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to this file instead of stdout")
	return cmd
}

// taskListDetailEdges splits a TaskListDetail's edges into the (out, in)
// []model.Edge pairs graphproj.TaskTriples takes. TaskListDetail.Edges names
// only the far end of each edge (TaskEdgeOut.To, TaskEdgeIn.From) since the
// near end is always the task itself; TaskTriples wants both ends resolved.
func taskListDetailEdges(t model.TaskListDetail) (out, in []model.Edge) {
	out = make([]model.Edge, len(t.Edges.Out))
	for i, e := range t.Edges.Out {
		out[i] = model.Edge{From: t.ID, To: e.To, Type: e.Type}
	}
	in = make([]model.Edge, len(t.Edges.In))
	for i, e := range t.Edges.In {
		in[i] = model.Edge{From: e.From, To: t.ID, Type: e.Type}
	}
	return out, in
}

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
	// A manifest copy-pasted from another repo would otherwise mint
	// <this repo> dct:hasPart <other repo's component IRIs> without a
	// complaint — a silently manufactured cross-repo edge (WL-270). The
	// manifest states which repo it describes; hold it to that.
	if want := host + "/" + owner + "/" + name; strings.TrimSpace(m.Repo) != want {
		return "", fmt.Errorf("%s says repo: %s, but derive is running against %s — the manifest describes another repo",
			manPath, m.Repo, want)
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

// newGraphDeriveCmd wires `lode graph derive`: run the repo-local derivers
// from a checkout, in CI or by hand. The server-side derivers (pr-affects,
// deploy) have no checkout to read and run through POST /api/v1/derive
// instead, which is what --server asks for.
func newGraphDeriveCmd() *cobra.Command {
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
			// The host goes into the repo instance IRI (iri.Repo), so a
			// GitLab or self-hosted remote must not mint id/repo/github.com/…
			// (WL-269). A hostless remote (bare owner/name) keeps the
			// GitHub default.
			host, err := repourl.Host(remote)
			if err != nil {
				return fmt.Errorf("resolve host from origin remote %q: %w", remote, err)
			}
			if host == "" {
				host = "github.com"
			}

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
			out, err := runDeriveLocal(cmd.Context(), root, host, owner, name, dryRun, c,
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

// runDeriveServer is `lode graph derive --server`: ask the backbone to run
// its own derivers and report what each one wrote.
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

// newGraphDriftCmd wires `lode graph drift [--component <iri>] [--acknowledged]`.
// --component filters client-side, so --json re-encodes the filtered value
// instead of passing the server's body through: the payload must be what the
// flags asked for, and a scripted caller still gets JSON.
func newGraphDriftCmd() *cobra.Command {
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
			if jsonOut(cmd) {
				if component == "" {
					printRaw(cmd, raw)
					return nil
				}
				return printJSON(cmd, cli.DriftFiltered(resp, component))
			}
			cli.DriftRender(cmd.OutOrStdout(), resp, component, acknowledged)
			return nil
		},
	}
	cmd.Flags().BoolVar(&acknowledged, "acknowledged", false, "include accepted deviations (active + expired)")
	cmd.Flags().StringVar(&component, "component", "", "filter edges from this component IRI")
	return cmd
}

// newGraphGapsCmd wires `lode graph gaps` (spec 007 §3.2).
func newGraphGapsCmd() *cobra.Command {
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
