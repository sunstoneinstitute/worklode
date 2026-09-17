// graph.go is the single bulk read behind the project graph page: one
// project's live tasks, the task edges between them, and the documents those
// tasks reach. Every step is one query over the whole project.
package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// The graph cites tasks and documents, it never renders them, so the two
// reads select an empty body instead of hauling every body across. Reusing
// taskColumns/docColumns keeps scanTask/scanDoc as the only scan plumbing.
var (
	graphTaskColumns = strings.Replace(taskColumns, "body", "''::text AS body", 1)
	graphDocColumns  = strings.Replace(docColumns, "body", "''::text AS body", 1)
)

// ProjectGraph reads one project's work graph (model.ProjectGraph): four
// bulk queries — live tasks, task edges between them, live docs, doc edges
// between them — then a walk from the tasks over links and doc edges that
// keeps the documents it reaches and drops the rest.
//
// An unknown project is not an error: it reads as an empty graph, and the
// API layer 404s on the project before calling this.
func (s *Store) ProjectGraph(ctx context.Context, projectID string) (model.ProjectGraph, error) {
	g := model.ProjectGraph{
		Project: projectID,
		Tasks:   []model.Task{}, TaskEdges: []model.Edge{},
		Docs: []model.Doc{}, DocEdges: []model.GraphDocEdge{}, Links: []model.GraphLink{},
	}
	tasks, err := s.graphTasks(ctx, projectID)
	if err != nil {
		return g, err
	}
	taskEdges, err := s.graphTaskEdges(ctx, projectID)
	if err != nil {
		return g, err
	}
	docs, err := s.graphDocs(ctx, projectID)
	if err != nil {
		return g, err
	}
	docEdges, err := s.graphDocEdges(ctx, projectID)
	if err != nil {
		return g, err
	}

	liveDoc := map[int64]bool{}
	for _, d := range docs {
		liveDoc[d.ID] = true
	}
	liveTask := map[string]bool{}
	for _, t := range tasks {
		liveTask[t.ID] = true
	}

	// Links: task -> doc from the task row, doc -> task from the doc row.
	// Only ends that exist in this project's live set count.
	var links []model.GraphLink
	seed := map[int64]bool{}
	for _, t := range tasks {
		if t.PlanDoc != 0 && liveDoc[t.PlanDoc] {
			links = append(links, model.GraphLink{Task: t.ID, Doc: t.PlanDoc, Type: "planned_in"})
			seed[t.PlanDoc] = true
		}
		if t.AboutDoc != 0 && liveDoc[t.AboutDoc] {
			links = append(links, model.GraphLink{Task: t.ID, Doc: t.AboutDoc, Type: "about"})
			seed[t.AboutDoc] = true
		}
	}
	for _, d := range docs {
		if d.GeneratedByTask != "" && liveTask[d.GeneratedByTask] {
			links = append(links, model.GraphLink{Task: d.GeneratedByTask, Doc: d.ID, Type: "generated_by"})
			seed[d.ID] = true
		}
	}

	// Reachability over doc edges, both directions, from the seeded docs.
	adj := map[int64][]int64{}
	for _, e := range docEdges {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}
	keep := map[int64]bool{}
	queue := make([]int64, 0, len(seed))
	for id := range seed {
		keep[id] = true
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, next := range adj[id] {
			if !keep[next] {
				keep[next] = true
				queue = append(queue, next)
			}
		}
	}

	g.Tasks = append(g.Tasks, tasks...)
	g.TaskEdges = append(g.TaskEdges, taskEdges...)
	for _, d := range docs {
		if keep[d.ID] {
			g.Docs = append(g.Docs, d)
		}
	}
	for _, e := range docEdges {
		if keep[e.From] && keep[e.To] {
			g.DocEdges = append(g.DocEdges, e)
		}
	}
	g.Links = append(g.Links, links...)
	return g, nil
}

func (s *Store) graphTasks(ctx context.Context, projectID string) ([]model.Task, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+graphTaskColumns+` FROM tasks WHERE project_id = $1 AND deleted_at IS NULL ORDER BY id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("graph tasks of %s: %w", projectID, err)
	}
	defer rows.Close()
	var out []model.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan graph task: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph tasks of %s: %w", projectID, err)
	}
	return out, nil
}

func (s *Store) graphTaskEdges(ctx context.Context, projectID string) ([]model.Edge, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT e.from_task, e.to_task, e.type
  FROM task_edges e
  JOIN tasks a ON a.id = e.from_task
  JOIN tasks b ON b.id = e.to_task
 WHERE a.project_id = $1 AND b.project_id = $1
   AND a.deleted_at IS NULL AND b.deleted_at IS NULL
 ORDER BY e.from_task, e.to_task, e.type`, projectID)
	if err != nil {
		return nil, fmt.Errorf("graph task edges of %s: %w", projectID, err)
	}
	defer rows.Close()
	var out []model.Edge
	for rows.Next() {
		var e model.Edge
		if err := rows.Scan(&e.From, &e.To, &e.Type); err != nil {
			return nil, fmt.Errorf("scan graph task edge: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph task edges of %s: %w", projectID, err)
	}
	return out, nil
}

func (s *Store) graphDocs(ctx context.Context, projectID string) ([]model.Doc, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+graphDocColumns+` FROM docs WHERE project_id = $1 AND deleted_at IS NULL ORDER BY id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("graph docs of %s: %w", projectID, err)
	}
	defer rows.Close()
	var out []model.Doc
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, fmt.Errorf("scan graph doc: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph docs of %s: %w", projectID, err)
	}
	return out, nil
}

// graphDocEdges collapses doc_edges to document level: one row per
// (from, to, type) whatever anchors the stored rows name. Both ends must be
// live documents of the project; an edge to another project or to an
// unresolved external reference is not drawn.
func (s *Store) graphDocEdges(ctx context.Context, projectID string) ([]model.GraphDocEdge, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT e.from_doc, e.to_doc, e.type
  FROM doc_edges e
  JOIN docs a ON a.id = e.from_doc
  JOIN docs b ON b.id = e.to_doc
 WHERE a.project_id = $1 AND b.project_id = $1
   AND a.deleted_at IS NULL AND b.deleted_at IS NULL
   AND e.from_doc <> e.to_doc
 ORDER BY e.from_doc, e.to_doc, e.type`, projectID)
	if err != nil {
		return nil, fmt.Errorf("graph doc edges of %s: %w", projectID, err)
	}
	defer rows.Close()
	var out []model.GraphDocEdge
	for rows.Next() {
		var e model.GraphDocEdge
		if err := rows.Scan(&e.From, &e.To, &e.Type); err != nil {
			return nil, fmt.Errorf("scan graph doc edge: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph doc edges of %s: %w", projectID, err)
	}
	return out, nil
}
