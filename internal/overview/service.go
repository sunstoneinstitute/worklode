package overview

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// ErrNoGraph is returned by graph-backed reads when no graph-server is
// configured; the API maps it to 503.
var ErrNoGraph = errors.New("knowledge graph not configured (LODE_GRAPHSERVER_URL)")

// Service is the read-only overview surface. Store is always present;
// Graph is nil when LODE_GRAPHSERVER_URL is unset, which disables the
// graph-backed reads but not the frontier (backbone-authoritative).
type Service struct {
	Store *store.Store
	Graph *graphserver.Client
}

// taskDAG joins backbone blocks edges with KG wl:dependsOn edges into
// (before, after) pairs. A dependsOn edge reverses: the dependency comes
// first. With no graph configured the KG half is empty, not an error — the
// backbone half alone is still meaningful.
func (s *Service) taskDAG(ctx context.Context) ([][2]string, error) {
	edges, err := s.Store.AllBlockEdges(ctx)
	if err != nil {
		return nil, err
	}
	pairs := make([][2]string, 0, len(edges))
	for _, e := range edges {
		pairs = append(pairs, [2]string{e.FromTask, e.ToTask})
	}
	if s.Graph != nil {
		rows, err := s.Graph.Select(ctx, taskRequiresQuery)
		if err != nil {
			return nil, fmt.Errorf("kg requires edges: %w", err)
		}
		for _, r := range rows {
			from, to := taskIDFromIRI(r["from"]), taskIDFromIRI(r["to"])
			if from != "" && to != "" {
				pairs = append(pairs, [2]string{to, from}) // dependency precedes dependent
			}
		}
	}
	return pairs, nil
}

// openSubgraph filters pairs to edges between open tasks, asking the store
// which of the DAG's nodes are closed (the per-repo done_state predicate —
// never a hardcoded state tuple). KG-only node ids that name no task row are
// simply never reported closed, so they stay in the subgraph.
func (s *Service) openSubgraph(ctx context.Context, pairs [][2]string) ([][2]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, e := range pairs {
		for _, id := range e {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	closed, err := s.Store.ClosedTaskIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return OpenSubgraph(pairs, closed), nil
}

// taskIDFromIRI inverts iri.Task ("" for a non-task IRI).
func taskIDFromIRI(s string) string {
	const p = iri.IDNS + "task/"
	if len(s) > len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return ""
}

// Frontier returns the ranked ready set (backbone order, spec 007 §3.4)
// annotated with depth/fan-out/is_critical from the combined DAG.
func (s *Service) Frontier(ctx context.Context, projectID string) ([]model.FrontierTask, error) {
	tasks, fanOut, err := s.Store.Frontier(ctx, projectID)
	if err != nil {
		return nil, err
	}
	pairs, err := s.taskDAG(ctx)
	if err != nil {
		return nil, err
	}
	// Depth is historical (full DAG); criticality follows the open subgraph,
	// matching CriticalPath (spec 007 §4's closed-task rule, WL-354).
	full := Analyze(pairs, nil)
	openPairs, err := s.openSubgraph(ctx, pairs)
	if err != nil {
		return nil, err
	}
	open := Analyze(openPairs, nil)
	out := make([]model.FrontierTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, model.FrontierTask{
			ID: t.ID, Title: t.Title, Project: t.Project,
			Priority: t.Priority, Concern: t.Concern,
			FanOut: fanOut[t.ID], Depth: full.Depth[t.ID], IsCritical: open.Critical[t.ID],
		})
	}
	return out, nil
}

// CriticalPath computes the enriched cross-store critical path (overview
// only, D12) plus any cycles found.
//
// Per spec 007 §4's closed-task rule (WL-354): depth is historical — the
// full live DAG, closed predecessors included — while criticality and
// fan-out are computed over the open subgraph, so a chain whose work is
// entirely finished is never reported as the thing holding the project up.
// Each reported node carries its historical depth, so "depth 5" still says
// how long the chain behind it really was.
func (s *Service) CriticalPath(ctx context.Context) (*model.CriticalPath, error) {
	pairs, err := s.taskDAG(ctx)
	if err != nil {
		return nil, err
	}
	full := Analyze(pairs, nil)
	openPairs, err := s.openSubgraph(ctx, pairs)
	if err != nil {
		return nil, err
	}
	open := AnalyzeWithFanOut(openPairs, nil)
	cp := &model.CriticalPath{Cycles: full.Cycles}
	for id, crit := range open.Critical {
		if !crit {
			continue
		}
		cp.Tasks = append(cp.Tasks, model.FrontierTask{
			ID: id, Depth: full.Depth[id], FanOut: open.FanOut[id], IsCritical: true,
		})
		if full.Depth[id] > cp.MaxDepth {
			cp.MaxDepth = full.Depth[id]
		}
	}
	sort.Slice(cp.Tasks, func(i, j int) bool {
		if cp.Tasks[i].Depth != cp.Tasks[j].Depth {
			return cp.Tasks[i].Depth < cp.Tasks[j].Depth
		}
		return cp.Tasks[i].ID < cp.Tasks[j].ID
	})
	return cp, nil
}

// DriftReport runs 4.1 (both directions), optionally including deviations.
func (s *Service) DriftReport(ctx context.Context, acknowledged bool) (*model.Drift, error) {
	if s.Graph == nil {
		return nil, ErrNoGraph
	}
	v, err := Violations(ctx, s.Graph)
	if err != nil {
		return nil, err
	}
	st, err := StaleIntent(ctx, s.Graph)
	if err != nil {
		return nil, err
	}
	d := &model.Drift{Violations: v, StaleIntent: st}
	if acknowledged {
		if d.Acknowledged, err = Acknowledged(ctx, s.Graph); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// GapReport runs 4.2.
func (s *Service) GapReport(ctx context.Context) ([]model.Gap, error) {
	if s.Graph == nil {
		return nil, ErrNoGraph
	}
	return Gaps(ctx, s.Graph)
}

// Roll computes the `lode overview` counts. Graph-backed counts degrade to
// zero with GraphEnabled=false rather than failing the whole screen.
func (s *Service) Roll(ctx context.Context, projectID string) (*model.Overview, error) {
	o := &model.Overview{GraphEnabled: s.Graph != nil}
	fr, err := s.Frontier(ctx, projectID)
	if err != nil {
		return nil, err
	}
	o.FrontierSize = len(fr)
	for i := range fr {
		if fr[i].IsCritical {
			o.CriticalHead = &fr[i]
			break
		}
	}
	cp, err := s.CriticalPath(ctx)
	if err != nil {
		return nil, err
	}
	o.Cycles = cp.Cycles
	if s.Graph != nil {
		d, err := s.DriftReport(ctx, false)
		if err != nil {
			return nil, err
		}
		o.Violations, o.StaleIntent = len(d.Violations), len(d.StaleIntent)
		g, err := s.GapReport(ctx)
		if err != nil {
			return nil, err
		}
		o.Gaps = len(g)
	}
	return o, nil
}
