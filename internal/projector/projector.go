// Package projector projects the backbone into the data-platform knowledge
// graph (spec 006 §11). Authority stays split: the backbone (this repo,
// Postgres) owns execution facts and — since 025 §5 moved authoring there —
// the design documents, and both are projected read-only into the graph.
// Tasks render into the project's own named graph; each document's canonical
// node renders into its per-document declared graph (007 §1.1's
// declared/<slug>, iri.DeclaredGraph), whose writer is this projector now
// that the backbone is the authoring surface (WL-289). graph-server
// exposes no SPARQL Update, so there is no per-subject patch and no
// read-modify-write of graph state: the write unit is a whole named graph.
// RunOnce re-renders every task and document of a dirty project from the
// backbone and PUTs the complete graphs, replacing what graph-server held.
// Deterministic rendering (graphproj.Document sorts and dedupes rendered
// lines) makes an unchanged re-projection byte-identical, so re-running
// after a crash or a duplicated batch is idempotent. Failures are isolated
// per project: the watermark is global, so a project that cannot be written
// is quarantined in graph_projection_failures and retried on its own backoff
// schedule while the watermark advances past every project that did succeed.
// One task mutation writes no outbox row — SetTaskSkills
// (internal/store/tasks.go) — which is harmless since skills are not
// projected; the only effect is a dct:modified that lags until the task's
// next real event.
package projector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
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
	clock func() time.Time // nil means time.Now; see now()
	rand  func() float64   // nil means rand.Float64; see jitter()
}

// New returns a projector reading at most batch transactions' worth of
// state_log rows per run (store.DirtyProjects).
func New(st *store.Store, gc *graphserver.Client, m *Metrics, batch int) *Projector {
	return &Projector{st: st, gc: gc, m: m, batch: batch}
}

// RunOnce projects every project dirtied since the checkpoint plus every
// quarantined project whose retry is due, then advances the checkpoint. It
// returns how many project graphs were (re-)written and joins the failures of
// the ones that were not.
//
// A project that fails is isolated, not fatal: it is recorded in the
// quarantine table (see failure and retryDelay), the loop continues, and the
// checkpoint still advances past the transactions that dirtied it so no
// healthy project is held back by it. The one case that still leaves the checkpoint alone is
// failing to *write* the quarantine row — without that row the project would
// be forgotten, so the old behaviour (retry the whole batch next run) is the
// safe fallback. Errors reading the checkpoint, the dirty batch or the
// quarantine table abort the run before anything is projected.
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
	quarantined, err := p.st.ProjectionFailures(ctx)
	if err != nil {
		return 0, fmt.Errorf("projector: %w", err)
	}

	now := p.now()
	prior := make(map[string]store.ProjectionFailure, len(quarantined))
	for _, f := range quarantined {
		prior[f.ProjectID] = f
	}
	still := len(quarantined)

	// A dirty project is attempted whatever its backoff says: fresh content
	// is the event most likely to clear a content-specific rejection, and the
	// bypass costs nothing above baseline — a dirty project is one the
	// projector would render and PUT anyway, so the render the backoff saves
	// is only the one for a project with no new events.
	dirty := make(map[string]bool, len(projects))
	for _, id := range projects {
		dirty[id] = true
	}
	targets := slices.Clone(projects)
	for _, f := range quarantined {
		if !dirty[f.ProjectID] && !f.NextAttemptAt.After(now) {
			targets = append(targets, f.ProjectID)
		}
	}

	var errs []error
	quarantineWritten := true
	for _, id := range targets {
		perr := p.projectOne(ctx, id)
		if perr == nil {
			n++
			if _, was := prior[id]; was {
				if cerr := p.st.ClearProjectionFailure(ctx, id); cerr != nil {
					// The stale row costs one redundant re-PUT next
					// time it comes due, which is idempotent.
					errs = append(errs, fmt.Errorf("projector: %w", cerr))
					continue
				}
				still--
				slog.Info("graph projection recovered", "project", id,
					"attempts", prior[id].Attempts)
			}
			continue
		}

		errs = append(errs, perr)
		next := failure(prior[id], id, now, perr, p.jitter())
		if rerr := p.st.RecordProjectionFailure(ctx, next); rerr != nil {
			errs = append(errs, fmt.Errorf("projector: %w", rerr))
			// Only a *first* failure needs the checkpoint held back: a
			// repeat one already has a row remembering the debt, and
			// freezing the watermark over it would reinstate exactly the
			// blast radius this quarantine exists to remove.
			if next.Attempts == 1 {
				quarantineWritten = false
			}
			continue
		}
		if next.Attempts == 1 {
			still++
		}
		p.m.recordProjectFailure()
		slog.Error("graph projection failed for project", "project", id,
			"attempts", next.Attempts, "retry_at", next.NextAttemptAt, "err", perr)
	}

	if through != cp && quarantineWritten {
		if cerr := p.st.SetProjectionCheckpoint(ctx, through); cerr != nil {
			errs = append(errs, fmt.Errorf("projector: %w", cerr))
		}
	}
	p.m.setQuarantined(still)
	return n, errors.Join(errs...)
}

// Retry cadence for a quarantined project. The first re-attempt is immediate
// — the next 10s poll — because the common failure is transient (graph-server
// restarting, a network blip) and recovers on its own. From the second
// consecutive failure the delay doubles from retryBase, so a project failing
// for a reason that will not fix itself stops costing a full graph render and
// PUT every 10s. retryCap keeps the worst-case detection lag for a project
// that *is* fixed bounded, and any new task activity in the project bypasses
// the wait entirely.
const (
	retryBase = time.Minute
	retryCap  = 30 * time.Minute
)

// retryDelay returns the *ceiling* on how long after the attempts'th
// consecutive failure the project may be re-attempted: 0, 1m, 2m, 4m … capped
// at retryCap. jitteredDelay draws the delay actually used from below it.
func retryDelay(attempts int) time.Duration {
	if attempts <= 1 {
		return 0
	}
	d := retryBase
	for i := 2; i < attempts; i++ {
		if d >= retryCap {
			break
		}
		d *= 2
	}
	return min(d, retryCap)
}

// jitteredDelay spreads the retry across the second half of its backoff
// window: half of retryDelay, plus r (in [0,1)) of the other half.
//
// Without it, a global graph-server outage fails every project at roughly the
// same instant, so their next_attempt_at values cluster and each cap boundary
// fires a full render and PUT for every project at once — a thundering herd
// against the service that just came back. Spreading the due times is the fix
// for the clustering; a per-run cap on retries is deliberately not, because
// ProjectionFailures orders by first_failed_at, so a cap would let a few
// persistently-failing old projects crowd out newer ones indefinitely.
//
// Half the window is kept un-jittered so the backoff curve still has a floor:
// a project that keeps failing is never re-attempted sooner than half its
// current delay, and retryDelay stays the ceiling every caller can reason
// about.
func jitteredDelay(attempts int, r float64) time.Duration {
	d := retryDelay(attempts)
	if d == 0 {
		return 0
	}
	return d/2 + time.Duration(r*float64(d/2))
}

// failure builds the quarantine row for a project that just failed. prior is
// its existing row, zero when there is none, so a first failure starts the
// clock and a repeat one only advances it. r is the jitter draw, in [0,1).
func failure(prior store.ProjectionFailure, id string, now time.Time, err error, r float64) store.ProjectionFailure {
	f := store.ProjectionFailure{
		ProjectID:     id,
		Attempts:      1,
		FirstFailedAt: now,
		LastFailedAt:  now,
		LastError:     err.Error(),
	}
	if prior.Attempts > 0 {
		f.Attempts = prior.Attempts + 1
		f.FirstFailedAt = prior.FirstFailedAt
	}
	f.NextAttemptAt = now.Add(jitteredDelay(f.Attempts, r))
	return f
}

// jitter draws the retry spread for one failure, overridable in tests that
// need a deterministic next_attempt_at.
func (p *Projector) jitter() float64 {
	if p.rand != nil {
		return p.rand()
	}
	return rand.Float64()
}

// now is the projector's clock, overridable in tests that need to reach past
// a backoff without sleeping.
func (p *Projector) now() time.Time {
	if p.clock != nil {
		return p.clock()
	}
	return time.Now().UTC()
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

	// The project's documents render into their own declared graphs
	// (WL-289): one graph per document, replaced whole, in the same attempt
	// — a failure quarantines the project as a unit, documents included.
	// Live documents only; a tombstoned document (044) keeps its last
	// projected graph until a delete path exists, noted in
	// docs/follow-ups.md.
	docs, err := p.st.ListDocs(ctx, store.DocFilter{Project: id})
	if err != nil {
		return fmt.Errorf("list docs for project %s: %w", id, err)
	}
	for _, d := range docs {
		body := graphproj.Document(graphproj.DocTriples(d))
		if _, err := p.gc.PutGraph(ctx, Branch, iri.DeclaredGraph(d.Slug), body); err != nil {
			return fmt.Errorf("put declared graph for doc %s: %w", d.Slug, err)
		}
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
