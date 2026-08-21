package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
	"github.com/sunstoneinstitute/worklode/internal/repourl"
)

// runDeriveLocal computes the repo-local observed documents (go-imports,
// repo-layout) for the repo at root. With dryRun it returns the rendered
// N-Triples; otherwise it Runs each through the deriver contract against c.
// A repo that is not a Go module derives layout only (reported inline).
func runDeriveLocal(ctx context.Context, root, host, owner, name string, dryRun bool, c *graphserver.Client) (string, error) {
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
	layout, err := derive.LayoutTriples(root, host, owner, name, m)
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
		if dryRun {
			fmt.Fprintf(&b, "# %s\n%s", iri.ObservedGraph(source), doc)
			continue
		}
		res, err := derive.Run(ctx, c, iri.ObservedGraph(source), doc)
		if err != nil {
			return b.String(), err
		}
		fmt.Fprintf(&b, "%s: hash=%s skipped=%v\n", res.Graph, res.Hash, res.Skipped)
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
// checkout, in CI or by hand. Server-side derivers (pr-affects, deploy) run
// via POST /api/v1/derive instead.
func newDeriveCmd() *cobra.Command {
	var graphURL string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "derive",
		Short: "Run the repo-local observed-layer derivers (go-imports, repo-layout)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := os.Getwd()
			if err != nil {
				return err
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
			out, err := runDeriveLocal(cmd.Context(), root, "github.com", owner, name, dryRun, c)
			fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
	cmd.Flags().StringVar(&graphURL, "graph-url", "", "graph-server base URL, unauthenticated (default: the LODE_GRAPHSERVER_* env via graphserver.FromEnv)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the N-Triples instead of writing")
	return cmd
}

func init() { rootCmd.AddCommand(newDeriveCmd()) }
