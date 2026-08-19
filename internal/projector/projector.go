// Package projector projects the backbone into the data-platform knowledge
// graph (spec 006 §11). Authority stays split: the backbone (this repo,
// Postgres) owns execution facts, Task is the one bridge between the two
// systems, and it is projected read-only into the graph — design facts
// (specs, ADRs, plans) are never projected from the backbone. graph-server
// exposes no SPARQL Update, so there is no per-subject patch and no
// read-modify-write of graph state: the write unit is the whole project
// graph. RunOnce re-renders every task of a dirty project from the backbone
// and PUTs the complete graph, replacing what graph-server held for it.
// Deterministic rendering (graphproj.Document sorts and dedupes rendered
// lines) makes an unchanged re-projection byte-identical, so re-running
// after a crash or a duplicated batch is idempotent.
package projector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Branch is the fixed graph-server branch the work graph lives on
// (spec 006 §13.2 item 5).
const Branch = "main"

// Projector re-renders dirty projects from the backbone and replaces their
// named graphs — idempotent per project graph: deterministic rendering
// (graphproj.Document) makes an unchanged re-projection byte-identical, so
// re-running after a crash or duplicated batch is safe.
type Projector struct {
	st    *store.Store
	gc    *graphserver.Client
	m     *Metrics // nil-safe; Task 4
	batch int
}

// New returns a projector reading at most batch state_log rows per run.
func New(st *store.Store, gc *graphserver.Client, m *Metrics, batch int) *Projector {
	return &Projector{st: st, gc: gc, m: m, batch: batch}
}

// RunOnce projects every project dirtied since the checkpoint, then advances
// the checkpoint. It returns how many project graphs were (re-)written. On
// error the checkpoint is left untouched so the next run retries the same
// batch.
func (p *Projector) RunOnce(ctx context.Context) (n int, err error) {
	start := time.Now()
	defer func() {
		result := "ok"
		if err != nil {
			result = "error"
		}
		p.m.recordRun(result, time.Since(start))
	}()

	cp, err := p.st.ProjectionCheckpoint(ctx)
	if err != nil {
		return 0, fmt.Errorf("projector: %w", err)
	}
	projects, through, err := p.st.DirtyProjects(ctx, cp, p.batch)
	if err != nil {
		return 0, fmt.Errorf("projector: %w", err)
	}

	for _, id := range projects {
		if err := p.projectOne(ctx, id); err != nil {
			return 0, fmt.Errorf("projector: %w", err)
		}
	}

	if through != cp {
		if err := p.st.SetProjectionCheckpoint(ctx, through); err != nil {
			return 0, fmt.Errorf("projector: %w", err)
		}
	}
	return len(projects), nil
}

// projectOne renders one project's whole graph from the backbone and
// replaces it on graph-server. A project that has vanished since it was
// marked dirty is skipped: no delete path exists today, so this is a guard,
// not a feature.
func (p *Projector) projectOne(ctx context.Context, id string) error {
	proj, err := p.st.GetProject(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get project %s: %w", id, err)
	}

	tasks, err := p.st.ListTasks(ctx, store.TaskFilter{Project: id})
	if err != nil {
		return fmt.Errorf("list tasks for project %s: %w", id, err)
	}

	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	edges, err := p.st.ListEdgesForTasks(ctx, ids)
	if err != nil {
		return fmt.Errorf("list edges for project %s: %w", id, err)
	}

	triples := graphproj.ProjectTriples(model.Project{ID: proj.ID, Name: proj.Name})
	for _, t := range tasks {
		te := edges[t.ID]
		triples = append(triples, graphproj.TaskTriples(t, toModelEdges(te.Out), toModelEdges(te.In))...)
	}

	doc := graphproj.Document(triples)
	if _, err := p.gc.PutGraph(ctx, Branch, iri.ProjectGraph(id), doc); err != nil {
		return fmt.Errorf("put graph for project %s: %w", id, err)
	}
	p.m.recordProject()
	return nil
}

// toModelEdges converts store edges (FromTask/ToTask) into the model.Edge
// shape graphproj.TaskTriples takes (From/To).
func toModelEdges(edges []store.Edge) []model.Edge {
	out := make([]model.Edge, len(edges))
	for i, e := range edges {
		out[i] = model.Edge{From: e.FromTask, To: e.ToTask, Type: e.Type}
	}
	return out
}
