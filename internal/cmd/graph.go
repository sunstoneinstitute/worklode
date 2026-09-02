package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func newGraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "The knowledge-graph projection: its health, and what it owes",
	}
	cmd.AddCommand(newGraphProjectionCmd())
	cmd.AddCommand(newGraphTriplesCmd())
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
	var allProjects bool
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

			projResp, _, err := c.ListProjects(cmd.Context())
			if err != nil {
				return err
			}
			projects := projResp.Projects

			var filter cli.TaskListFilter
			if !allProjects {
				sc := currentScope(cmd.Context(), c, cfg)
				if sc.Project == "" {
					return errNoProject
				}
				filter.Project = sc.Project
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

			// Not filtered by state (--all-projects or not): a done or
			// released task must stay in the graph, or a resolved blocker
			// reads as an open one.
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
	cmd.Flags().BoolVar(&allProjects, "all-projects", false, "export every project instead of just the current one")
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
