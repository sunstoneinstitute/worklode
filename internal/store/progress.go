// progress.go is the single bulk read behind WL-SPEC-66's progress view: one
// project's specs, their sections, the plans covering them, the tasks those
// plans minted, and where each open task sits. Every step is one query over
// the whole project — a per-document round trip here would be a query per
// spec on a corpus that only grows.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/progress"
)

// deliveredRankStates is the SQL "IN (...)" literal list of every state in
// deliveryRanks (tasks.go), sorted — the states progress.TaskClass calls
// "landed". Built from the map the way openWorkExcludedStates is built from
// deliveredStateSet, so SQL and TaskClass cannot disagree about what landed
// means.
var deliveredRankStates = func() string {
	states := slices.Sorted(maps.Keys(deliveryRanks))
	quoted := make([]string, len(states))
	for i, st := range states {
		quoted[i] = "'" + st + "'"
	}
	return strings.Join(quoted, ", ")
}()

// ProjectProgress reads every fact progress.Derive needs about projectID.
// Deleted documents and tasks are out; a covers or requires edge pointing
// outside this backbone is kept only where it can be rendered (requires) and
// dropped where it cannot be resolved to a section (covers).
func (s *Store) ProjectProgress(ctx context.Context, projectID string) (progress.Input, error) {
	in := progress.Input{Project: projectID}

	specs, plans, err := s.progressDocs(ctx, projectID)
	if err != nil {
		return progress.Input{}, err
	}
	if err := s.progressSections(ctx, projectID, specs); err != nil {
		return progress.Input{}, err
	}
	if err := s.progressPlanningTasks(ctx, projectID, specs); err != nil {
		return progress.Input{}, err
	}
	if err := s.progressEdges(ctx, projectID, plans); err != nil {
		return progress.Input{}, err
	}
	tasks, err := s.progressTasks(ctx, projectID)
	if err != nil {
		return progress.Input{}, err
	}
	if err := s.progressPositions(ctx, projectID, tasks); err != nil {
		return progress.Input{}, err
	}
	for _, pt := range tasks {
		if plan := plans[pt.plan]; plan != nil {
			plan.Tasks = append(plan.Tasks, pt.task)
		}
	}
	rally, err := s.progressRally(ctx, projectID)
	if err != nil {
		return progress.Input{}, err
	}
	draft, err := s.DraftRallyBand(ctx, projectID)
	if err != nil {
		return progress.Input{}, err
	}

	in.Rally, in.Draft = rally, draft
	for _, id := range slices.Sorted(maps.Keys(specs)) {
		in.Specs = append(in.Specs, *specs[id])
	}
	for _, id := range slices.Sorted(maps.Keys(plans)) {
		in.Plans = append(in.Plans, *plans[id])
	}
	return in, nil
}

// progressDocs reads the project's live specs and plans, rendering each ref
// through model.Doc.FormatRef — the same formatter cli.DocRef uses.
func (s *Store) progressDocs(ctx context.Context, projectID string) (
	specs map[int64]*progress.Spec, plans map[int64]*progress.Plan, err error) {

	rows, err := s.db.QueryContext(ctx, `
SELECT d.id, d.kind, coalesce(d.number, 0), d.title, d.status, d.updated_at,
       coalesce(p.key, ''), coalesce(d.owner, '')
  FROM docs d
  JOIN projects p ON p.id = d.project_id
 WHERE d.project_id = $1 AND d.kind IN ('spec', 'plan') AND d.deleted_at IS NULL`,
		projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("progress docs of %s: %w", projectID, err)
	}
	defer rows.Close()

	specs, plans = map[int64]*progress.Spec{}, map[int64]*progress.Plan{}
	for rows.Next() {
		var d model.Doc
		var status, owner string
		var updated = &d.UpdatedAt
		if err := rows.Scan(&d.ID, &d.Kind, &d.Number, &d.Title, &status, updated,
			&d.ProjectKey, &owner); err != nil {
			return nil, nil, fmt.Errorf("scan progress doc: %w", err)
		}
		if d.Kind == "spec" {
			specs[d.ID] = &progress.Spec{
				Doc: d.ID, Ref: d.FormatRef(), Title: d.Title, Updated: d.UpdatedAt,
				Status: status, Owner: owner,
			}
			continue
		}
		plans[d.ID] = &progress.Plan{
			Doc: d.ID, Ref: d.FormatRef(), Title: d.Title, Status: status, Owner: owner,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("progress docs of %s: %w", projectID, err)
	}
	return specs, plans, nil
}

// progressSections attaches each spec's sections in document order.
func (s *Store) progressSections(ctx context.Context, projectID string, specs map[int64]*progress.Spec) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT sec.doc_id, sec.anchor, sec.heading, sec.depth
  FROM doc_sections sec
  JOIN docs d ON d.id = sec.doc_id
 WHERE d.project_id = $1 AND d.kind = 'spec' AND d.deleted_at IS NULL
 ORDER BY sec.doc_id, sec.position`, projectID)
	if err != nil {
		return fmt.Errorf("progress sections of %s: %w", projectID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var docID int64
		var sec progress.Section
		if err := rows.Scan(&docID, &sec.Anchor, &sec.Heading, &sec.Depth); err != nil {
			return fmt.Errorf("scan progress section: %w", err)
		}
		if spec := specs[docID]; spec != nil {
			spec.Sections = append(spec.Sections, sec)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("progress sections of %s: %w", projectID, err)
	}
	return nil
}

// progressPlanningTasks attaches each spec's open planning task, one query
// for the project rather than OpenTaskForDoc per spec. Same rule as
// OpenTaskForDoc (kind design, about_doc the spec, not closed, oldest first),
// so the link the page draws names the task POST .../progress/plan would
// return (066 §3.4).
func (s *Store) progressPlanningTasks(ctx context.Context, projectID string, specs map[int64]*progress.Spec) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.about_doc, t.id
  FROM tasks t
  JOIN docs d ON d.id = t.about_doc
 WHERE d.project_id = $1 AND d.kind = 'spec' AND d.deleted_at IS NULL
   AND t.kind = 'design' AND t.deleted_at IS NULL AND NOT `+taskClosed("t")+`
 ORDER BY t.created_at, t.id`, projectID)
	if err != nil {
		return fmt.Errorf("progress planning tasks of %s: %w", projectID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var docID int64
		var taskID string
		if err := rows.Scan(&docID, &taskID); err != nil {
			return fmt.Errorf("scan progress planning task: %w", err)
		}
		if spec := specs[docID]; spec != nil && spec.PlanningTask == "" {
			spec.PlanningTask = taskID
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("progress planning tasks of %s: %w", projectID, err)
	}
	return nil
}

// progressEdges attaches each plan's covers and requires edges. A covers
// edge is section-scoped, so one that resolves to no document in this
// backbone names no section here and is dropped; a requires edge renders as
// the target's ref, falling back to the raw external reference.
func (s *Store) progressEdges(ctx context.Context, projectID string, plans map[int64]*progress.Plan) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT e.from_doc, e.type, e.to_doc, coalesce(e.to_anchor, ''),
       coalesce(e.coverage, 'full'), coalesce(e.to_external, ''),
       coalesce(t.kind, ''), coalesce(t.number, 0), coalesce(tp.key, '')
  FROM doc_edges e
  JOIN docs d ON d.id = e.from_doc
  LEFT JOIN docs t ON t.id = e.to_doc
  LEFT JOIN projects tp ON tp.id = t.project_id
 WHERE d.project_id = $1 AND d.kind = 'plan' AND d.deleted_at IS NULL
   AND e.type IN ('covers', 'requires')
 ORDER BY e.from_doc, e.id`, projectID)
	if err != nil {
		return fmt.Errorf("progress edges of %s: %w", projectID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var fromDoc int64
		var edgeType, anchor, coverage, external string
		var toDoc sql.NullInt64
		var target model.Doc
		if err := rows.Scan(&fromDoc, &edgeType, &toDoc, &anchor, &coverage, &external,
			&target.Kind, &target.Number, &target.ProjectKey); err != nil {
			return fmt.Errorf("scan progress edge: %w", err)
		}
		plan := plans[fromDoc]
		if plan == nil {
			continue
		}
		if edgeType == "requires" {
			ref := external
			if toDoc.Valid {
				ref = target.FormatRef()
			}
			plan.Requires = append(plan.Requires, ref)
			continue
		}
		if !toDoc.Valid {
			continue
		}
		plan.Covers = append(plan.Covers, progress.Cover{
			Spec: toDoc.Int64, Anchor: anchor, Level: coverage,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("progress edges of %s: %w", projectID, err)
	}
	return nil
}

// progressTask is one minted task, the plan that minted it, and the
// assignee Position needs but progress.Task does not carry. Positions are
// computed over these before anything is appended to a plan.
type progressTask struct {
	task     progress.Task
	plan     int64
	assignee string
}

// progressTasks reads every live task minted from one of the project's
// plans, in mint order.
func (s *Store) progressTasks(ctx context.Context, projectID string) ([]*progressTask, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id, t.title, t.state, t.plan_doc, coalesce(t.assignee, '')
  FROM tasks t
  JOIN docs d ON d.id = t.plan_doc
 WHERE d.project_id = $1 AND d.kind = 'plan' AND d.deleted_at IS NULL
   AND t.deleted_at IS NULL
 ORDER BY t.created_at, t.id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("progress tasks of %s: %w", projectID, err)
	}
	defer rows.Close()

	var out []*progressTask
	for rows.Next() {
		var pt progressTask
		if err := rows.Scan(&pt.task.ID, &pt.task.Title, &pt.task.State, &pt.plan,
			&pt.assignee); err != nil {
			return nil, fmt.Errorf("scan progress task: %w", err)
		}
		out = append(out, &pt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("progress tasks of %s: %w", projectID, err)
	}
	return out, nil
}

// progressPositions fills every task's Position. A landed task needs no
// facts — its delivery state outranks any PR or CI row — so leases, PRs and
// CI runs are only looked up for the open ones, in three bulk reads.
func (s *Store) progressPositions(ctx context.Context, projectID string, tasks []*progressTask) error {
	open := map[string]*progressTask{}
	for _, pt := range tasks {
		if progress.TaskClass(pt.task.State) != "landed" {
			open[pt.task.ID] = pt
		}
	}
	if len(open) == 0 {
		for _, pt := range tasks {
			pt.task.Position = progress.Position(progress.PositionFacts{
				State: pt.task.State, Assignee: pt.assignee, Now: s.Now(),
			})
		}
		return nil
	}

	facts, err := s.ListProjectWorkFacts(ctx, projectID)
	if err != nil {
		return err
	}
	leases := map[string]*progress.LeaseFact{}
	for _, f := range facts {
		if f.Lease == nil || open[f.Task.ID] == nil {
			continue
		}
		leases[f.Task.ID] = &progress.LeaseFact{
			Actor: f.Lease.ActorID, Since: f.Lease.AcquiredAt,
		}
	}

	openPRs, err := s.OpenPRsForProject(ctx, projectID)
	if err != nil {
		return err
	}
	prs := map[string]*progress.PRFact{}
	shas := map[string]RepoSHA{}
	var keys []RepoSHA
	// OpenPRsForProject is newest first, so the first PR seen for a task is
	// the one that wins.
	for _, pr := range openPRs {
		if pr.TaskID == nil || open[*pr.TaskID] == nil || prs[*pr.TaskID] != nil {
			continue
		}
		prs[*pr.TaskID] = &progress.PRFact{Number: int(pr.Number), URL: pr.URL}
		key := RepoSHA{Repo: pr.Repo, SHA: pr.HeadSHA}
		shas[*pr.TaskID] = key
		keys = append(keys, key)
	}

	runs, err := s.CIRunsForSHAs(ctx, keys)
	if err != nil {
		return err
	}
	for _, pt := range tasks {
		f := progress.PositionFacts{
			State: pt.task.State, Assignee: pt.assignee, Now: s.Now(),
		}
		if open[pt.task.ID] != nil {
			f.Lease, f.PR = leases[pt.task.ID], prs[pt.task.ID]
			if latest := latestRun(runs[shas[pt.task.ID]]); latest != nil {
				f.CI = &progress.CIFact{Status: latest.Status}
				if latest.Conclusion != nil {
					f.CI.Conclusion = *latest.Conclusion
				}
			}
		}
		pt.task.Position = progress.Position(f)
	}
	return nil
}

// latestRun is the run reported most recently for a SHA — the one whose
// status describes the checks now, not an earlier workflow's verdict.
func latestRun(runs []CIRun) *CIRun {
	var out *CIRun
	for i := range runs {
		if out == nil || runs[i].UpdatedAt.After(out.UpdatedAt) {
			out = &runs[i]
		}
	}
	return out
}

// progressRally reads the project's active rally band, or nil when it has
// none.
func (s *Store) progressRally(ctx context.Context, projectID string) (*model.RallyBand, error) {
	rally, err := s.ActiveRally(ctx, projectID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	members, err := s.RallyMemberCount(ctx, rally.ID)
	if err != nil {
		return nil, err
	}
	var landed int
	if err := s.db.QueryRowContext(ctx, `
SELECT count(*) FROM task_edges e
  JOIN tasks f ON f.id = e.from_task AND f.deleted_at IS NULL
 WHERE e.to_task = $1 AND e.type = 'blocks'
   AND f.state IN (`+deliveredRankStates+`)`, rally.ID).Scan(&landed); err != nil {
		return nil, fmt.Errorf("rally landed count for %s: %w", rally.ID, err)
	}
	return &model.RallyBand{
		ID: rally.ID, Title: rally.Title, Members: members, Landed: landed,
	}, nil
}

// DraftRallyBand reads the project's draft rally as WL-SPEC-66 §3.5's
// footer shows it — member count and the number of specs those members come
// from — or nil when the project has none. Landed is left at zero: a draft
// rally ranks nothing, so nothing in it has landed under it.
func (s *Store) DraftRallyBand(ctx context.Context, projectID string) (*model.RallyBand, error) {
	rally, err := s.DraftRally(ctx, projectID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	members, err := s.RallyMemberCount(ctx, rally.ID)
	if err != nil {
		return nil, err
	}
	specs, err := s.rallySpecCount(ctx, rally.ID)
	if err != nil {
		return nil, err
	}
	return &model.RallyBand{
		ID: rally.ID, Title: rally.Title, Members: members, Specs: specs,
	}, nil
}

// rallySpecCount counts the distinct specs a rally's members work on: the
// specs covered by the plan each member was minted from or is about, plus the
// specs a member names directly (§3.4's planning task does). A member that
// resolves to no spec — a task filed by hand into the rally — counts for
// none, which is honest: the footer says how many specs are represented, not
// how many members lack one.
func (s *Store) rallySpecCount(ctx context.Context, rallyID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
WITH members AS (
  SELECT f.plan_doc, f.about_doc
    FROM task_edges e
    JOIN tasks f ON f.id = e.from_task AND f.deleted_at IS NULL
   WHERE e.to_task = $1 AND e.type = 'blocks'
), plans AS (
  SELECT plan_doc AS doc FROM members WHERE plan_doc IS NOT NULL
  UNION
  SELECT m.about_doc FROM members m
    JOIN docs d ON d.id = m.about_doc AND d.kind = 'plan' AND d.deleted_at IS NULL
)
SELECT count(*) FROM (
  SELECT e.to_doc AS spec FROM plans p
    JOIN doc_edges e ON e.from_doc = p.doc AND e.type = 'covers'
   WHERE e.to_doc IS NOT NULL
  UNION
  SELECT m.about_doc FROM members m
    JOIN docs d ON d.id = m.about_doc AND d.kind = 'spec' AND d.deleted_at IS NULL
) s`, rallyID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("rally spec count for %s: %w", rallyID, err)
	}
	return n, nil
}

// ProjectHasSpecs reports whether the project has at least one live spec —
// the fact WL-SPEC-66 §2 makes the Progress sidebar entry and the route
// itself conditional on (065 §1: a surface appears when the project has
// facts for it).
func (s *Store) ProjectHasSpecs(ctx context.Context, projectID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM docs
   WHERE project_id = $1 AND kind = 'spec' AND deleted_at IS NULL)`,
		projectID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("project %s has specs: %w", projectID, err)
	}
	return exists, nil
}
