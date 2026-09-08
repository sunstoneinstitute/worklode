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
//
// specs narrows the read to those spec ids and the plans that cover at
// least one of them — the row/summary fragments (WL-SPEC-66 §5.2) ask for
// one spec at a time rather than the whole project. Every downstream read
// (sections, planning tasks, edges, tasks) then scopes itself to the docs
// progressDocs actually returned, so the narrowing holds all the way down
// instead of only at the first query. An empty specs reads the whole
// project, as ProjectProgress always did.
func (s *Store) ProjectProgress(ctx context.Context, projectID string, specs []int64) (progress.Input, error) {
	in := progress.Input{Project: projectID}

	specDocs, plans, err := s.progressDocs(ctx, projectID, specs)
	if err != nil {
		return progress.Input{}, err
	}
	if err := s.progressSections(ctx, projectID, specDocs); err != nil {
		return progress.Input{}, err
	}
	if err := s.progressPlanningTasks(ctx, projectID, specDocs); err != nil {
		return progress.Input{}, err
	}
	if err := s.progressEdges(ctx, projectID, plans); err != nil {
		return progress.Input{}, err
	}
	tasks, err := s.progressTasks(ctx, projectID, plans)
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
	for _, id := range slices.Sorted(maps.Keys(specDocs)) {
		in.Specs = append(in.Specs, *specDocs[id])
	}
	for _, id := range slices.Sorted(maps.Keys(plans)) {
		in.Plans = append(in.Plans, *plans[id])
	}
	return in, nil
}

// progressDocs reads the project's live specs and plans, rendering each ref
// through model.Doc.FormatRef — the same formatter cli.DocRef uses. When
// specs is non-empty, only those spec ids and the plans that cover at least
// one of them are read (WL-SPEC-66 §5.2's fragment case); an empty specs
// reads every live spec and plan of the project, as this always did.
func (s *Store) progressDocs(ctx context.Context, projectID string, specs []int64) (
	specDocs map[int64]*progress.Spec, plans map[int64]*progress.Plan, err error) {

	specDocs, err = s.progressSpecDocs(ctx, projectID, specs)
	if err != nil {
		return nil, nil, err
	}
	plans, err = s.progressPlanDocs(ctx, projectID, specs)
	if err != nil {
		return nil, nil, err
	}
	return specDocs, plans, nil
}

// progressSpecDocs reads the project's live specs, or just those named by
// specs when it is non-empty.
func (s *Store) progressSpecDocs(ctx context.Context, projectID string, specs []int64) (map[int64]*progress.Spec, error) {
	query := `
SELECT d.id, coalesce(d.number, 0), d.title, d.status, d.updated_at,
       coalesce(p.key, ''), coalesce(d.owner, '')
  FROM docs d
  JOIN projects p ON p.id = d.project_id
 WHERE d.project_id = $1 AND d.kind = 'spec' AND d.deleted_at IS NULL`
	args := []any{projectID}
	if len(specs) > 0 {
		query += " AND d.id = ANY($2)"
		args = append(args, specs)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("progress specs of %s: %w", projectID, err)
	}
	defer rows.Close()

	out := map[int64]*progress.Spec{}
	for rows.Next() {
		var d model.Doc
		var status, owner string
		if err := rows.Scan(&d.ID, &d.Number, &d.Title, &status, &d.UpdatedAt,
			&d.ProjectKey, &owner); err != nil {
			return nil, fmt.Errorf("scan progress spec: %w", err)
		}
		d.Kind = "spec"
		out[d.ID] = &progress.Spec{
			Doc: d.ID, Ref: d.FormatRef(), Title: d.Title, Updated: d.UpdatedAt,
			Status: status, Owner: owner,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("progress specs of %s: %w", projectID, err)
	}
	return out, nil
}

// progressPlanDocs reads the project's live plans, or — when specs is
// non-empty — only the ones with a covers edge into at least one of them.
// A plan covering none of the requested specs contributes nothing to their
// derivation (progress.Derive matches covers by spec id), so leaving it out
// changes nothing about the fragment's answer.
func (s *Store) progressPlanDocs(ctx context.Context, projectID string, specs []int64) (map[int64]*progress.Plan, error) {
	query := `
SELECT d.id, coalesce(d.number, 0), d.title, d.status, coalesce(p.key, ''), coalesce(d.owner, '')
  FROM docs d
  JOIN projects p ON p.id = d.project_id
 WHERE d.project_id = $1 AND d.kind = 'plan' AND d.deleted_at IS NULL`
	args := []any{projectID}
	if len(specs) > 0 {
		query += ` AND EXISTS (
  SELECT 1 FROM doc_edges e
   WHERE e.from_doc = d.id AND e.type = 'covers' AND e.to_doc = ANY($2))`
		args = append(args, specs)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("progress plans of %s: %w", projectID, err)
	}
	defer rows.Close()

	out := map[int64]*progress.Plan{}
	for rows.Next() {
		var d model.Doc
		var status, owner string
		if err := rows.Scan(&d.ID, &d.Number, &d.Title, &status, &d.ProjectKey, &owner); err != nil {
			return nil, fmt.Errorf("scan progress plan: %w", err)
		}
		d.Kind = "plan"
		out[d.ID] = &progress.Plan{
			Doc: d.ID, Ref: d.FormatRef(), Title: d.Title, Status: status, Owner: owner,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("progress plans of %s: %w", projectID, err)
	}
	return out, nil
}

// progressSections attaches each spec's sections in document order. Scoped
// to specs' own keys, not the whole project, so a caller that already
// narrowed progressDocs to one spec reads only that spec's sections.
func (s *Store) progressSections(ctx context.Context, projectID string, specs map[int64]*progress.Spec) error {
	ids := slices.Collect(maps.Keys(specs))
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT sec.doc_id, sec.anchor, sec.heading, sec.depth
  FROM doc_sections sec
  JOIN docs d ON d.id = sec.doc_id
 WHERE d.project_id = $1 AND d.kind = 'spec' AND d.deleted_at IS NULL
   AND d.id = ANY($2)
 ORDER BY sec.doc_id, sec.position`, projectID, ids)
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
	ids := slices.Collect(maps.Keys(specs))
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT t.about_doc, t.id
  FROM tasks t
  JOIN docs d ON d.id = t.about_doc
 WHERE d.project_id = $1 AND d.kind = 'spec' AND d.deleted_at IS NULL
   AND d.id = ANY($2)
   AND t.kind = 'design' AND t.deleted_at IS NULL AND NOT `+taskClosed("t")+`
 ORDER BY t.created_at, t.id`, projectID, ids)
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
	planIDs := slices.Collect(maps.Keys(plans))
	if len(planIDs) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT e.from_doc, e.type, e.to_doc, coalesce(e.to_anchor, ''),
       coalesce(e.coverage, 'full'), coalesce(e.to_external, ''),
       coalesce(t.kind, ''), coalesce(t.number, 0), coalesce(tp.key, '')
  FROM doc_edges e
  JOIN docs d ON d.id = e.from_doc
  LEFT JOIN docs t ON t.id = e.to_doc
  LEFT JOIN projects tp ON tp.id = t.project_id
 WHERE d.project_id = $1 AND d.kind = 'plan' AND d.deleted_at IS NULL
   AND d.id = ANY($2)
   AND e.type IN ('covers', 'requires')
 ORDER BY e.from_doc, e.id`, projectID, planIDs)
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

// progressTasks reads every live task minted from one of plans, in mint
// order. Scoped to plans' own keys, so a caller that already narrowed
// progressDocs to one spec's covering plans reads only their tasks.
func (s *Store) progressTasks(ctx context.Context, projectID string, plans map[int64]*progress.Plan) ([]*progressTask, error) {
	planIDs := slices.Collect(maps.Keys(plans))
	if len(planIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id, t.title, t.state, t.plan_doc, coalesce(t.assignee, '')
  FROM tasks t
  JOIN docs d ON d.id = t.plan_doc
 WHERE d.project_id = $1 AND d.kind = 'plan' AND d.deleted_at IS NULL
   AND d.id = ANY($2)
   AND t.deleted_at IS NULL
 ORDER BY t.created_at, t.id`, projectID, planIDs)
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
	landed, err := s.rallyLandedCount(ctx, rally.ID)
	if err != nil {
		return nil, err
	}
	return &model.RallyBand{
		ID: rally.ID, Title: rally.Title, Members: members, Landed: landed,
	}, nil
}

// rallyLandedCount counts a rally's members whose state has landed —
// deliveredRankStates, the same ranks that decide "landed" everywhere else
// in this package. Shared by progressRally and ProgressRefs so the two never
// disagree about what counts.
func (s *Store) rallyLandedCount(ctx context.Context, rallyID string) (int, error) {
	var landed int
	if err := s.db.QueryRowContext(ctx, `
SELECT count(*) FROM task_edges e
  JOIN tasks f ON f.id = e.from_task AND f.deleted_at IS NULL
 WHERE e.to_task = $1 AND e.type = 'blocks'
   AND f.state IN (`+deliveredRankStates+`)`, rallyID).Scan(&landed); err != nil {
		return 0, fmt.Errorf("rally landed count for %s: %w", rallyID, err)
	}
	return landed, nil
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

// ProgressRef maps tasks and documents to the project and specs the
// Progress page shows them under (WL-SPEC-66 §5.1): a task through its
// plan_doc or about_doc to the specs that plan covers, a plan through its
// covers edges, a spec to itself.
type ProgressRef struct {
	Project string
	Task    string
	State   string
	Plan    int64
	Specs   []int64
	Rally   *model.RallyBand // set when Task is a rally or a rally member
}

// ProgressRefs resolves tasks and docs to the project and specs WL-SPEC-66's
// Progress page groups them under, one query per id family — no per-id round
// trips. A task's plan_doc wins over its about_doc when both are set; either
// way the referenced doc is joined on kind and deleted_at only, so a plan
// stuck at the "no_record" progress state still resolves through plan_doc —
// that state is progress.Derive's judgment, not a fact this read filters on.
func (s *Store) ProgressRefs(ctx context.Context, tasks []string, docs []int64) ([]ProgressRef, error) {
	if len(tasks) == 0 && len(docs) == 0 {
		return nil, nil
	}

	taskRows, err := s.progressRefTasks(ctx, tasks)
	if err != nil {
		return nil, err
	}

	docIDs := append([]int64{}, docs...)
	for _, t := range taskRows {
		if t.resolveDoc != 0 {
			docIDs = append(docIDs, t.resolveDoc)
		}
	}
	docInfo, err := s.progressRefDocs(ctx, docIDs)
	if err != nil {
		return nil, err
	}

	var planIDs []int64
	for id, d := range docInfo {
		if d.kind == "plan" {
			planIDs = append(planIDs, id)
		}
	}
	covers, err := s.progressRefCovers(ctx, planIDs)
	if err != nil {
		return nil, err
	}

	rallyOf, bands, err := s.progressRefRallies(ctx, tasks)
	if err != nil {
		return nil, err
	}

	var out []ProgressRef
	for _, id := range docs {
		d, ok := docInfo[id]
		if !ok {
			continue
		}
		ref := ProgressRef{Project: d.project}
		if d.kind == "spec" {
			ref.Specs = []int64{id}
		} else {
			ref.Plan, ref.Specs = id, covers[id]
		}
		out = append(out, ref)
	}
	for _, t := range taskRows {
		ref := ProgressRef{
			Project: t.project, Task: t.id, State: t.state, Rally: bands[rallyOf[t.id]],
		}
		if d, ok := docInfo[t.resolveDoc]; ok {
			switch d.kind {
			case "spec":
				ref.Specs = []int64{t.resolveDoc}
			case "plan":
				ref.Plan, ref.Specs = t.resolveDoc, covers[t.resolveDoc]
			}
		}
		out = append(out, ref)
	}
	return out, nil
}

// progressRefTask is one task's identity plus the single doc id its
// plan_doc or about_doc resolves to (plan_doc wins, 0 means neither is set).
type progressRefTask struct {
	id, project, state string
	resolveDoc         int64
}

func (s *Store) progressRefTasks(ctx context.Context, ids []string) ([]progressRefTask, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, project_id, state, coalesce(plan_doc, about_doc, 0)
  FROM tasks
 WHERE id = ANY($1) AND deleted_at IS NULL`, ids)
	if err != nil {
		return nil, fmt.Errorf("progress ref tasks: %w", err)
	}
	defer rows.Close()

	var out []progressRefTask
	for rows.Next() {
		var t progressRefTask
		if err := rows.Scan(&t.id, &t.project, &t.state, &t.resolveDoc); err != nil {
			return nil, fmt.Errorf("scan progress ref task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// progressRefDoc is one doc's project and kind — enough to tell a spec from
// a plan and to place either under its project.
type progressRefDoc struct {
	project, kind string
}

func (s *Store) progressRefDocs(ctx context.Context, ids []int64) (map[int64]progressRefDoc, error) {
	out := map[int64]progressRefDoc{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, project_id, kind
  FROM docs
 WHERE id = ANY($1) AND deleted_at IS NULL`, ids)
	if err != nil {
		return nil, fmt.Errorf("progress ref docs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var d progressRefDoc
		if err := rows.Scan(&id, &d.project, &d.kind); err != nil {
			return nil, fmt.Errorf("scan progress ref doc: %w", err)
		}
		out[id] = d
	}
	return out, rows.Err()
}

// progressRefCovers maps each plan id to the specs it covers, DISTINCT
// because progressPlanBody-style plans name the same spec once per section.
func (s *Store) progressRefCovers(ctx context.Context, planIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	if len(planIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT from_doc, to_doc
  FROM doc_edges
 WHERE type = 'covers' AND from_doc = ANY($1) AND to_doc IS NOT NULL`, planIDs)
	if err != nil {
		return nil, fmt.Errorf("progress ref covers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var from, to int64
		if err := rows.Scan(&from, &to); err != nil {
			return nil, fmt.Errorf("scan progress ref cover: %w", err)
		}
		out[from] = append(out[from], to)
	}
	return out, rows.Err()
}

// progressRefRallyInfo is one rally's identity as read off the tasks row —
// title and state are what progressRefRallyBand needs to pick its branch.
type progressRefRallyInfo struct {
	id, title, state string
}

// progressRefRallies resolves, for each task, the rally it is either a
// member of or itself is: one query joining each task to a 'blocks' edge
// (its membership, if any) and then to whichever of the task itself or that
// edge's target is a live rally row — a task that is neither drops out of
// the join. The bands are then built per distinct rally found, a count
// bounded by how many rallies a project can have (one active, one draft),
// never by how many tasks were asked about.
func (s *Store) progressRefRallies(ctx context.Context, taskIDs []string) (map[string]string, map[string]*model.RallyBand, error) {
	byTask := map[string]string{}
	if len(taskIDs) == 0 {
		return byTask, nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id, r.id, r.title, r.state
  FROM tasks t
  LEFT JOIN task_edges e ON e.from_task = t.id AND e.type = 'blocks'
  JOIN tasks r ON r.id = CASE WHEN t.kind = 'rally' THEN t.id ELSE e.to_task END
              AND r.kind = 'rally' AND r.deleted_at IS NULL
 WHERE t.id = ANY($1) AND t.deleted_at IS NULL`, taskIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("progress ref rallies: %w", err)
	}
	defer rows.Close()

	rallies := map[string]progressRefRallyInfo{}
	for rows.Next() {
		var memberID string
		var info progressRefRallyInfo
		if err := rows.Scan(&memberID, &info.id, &info.title, &info.state); err != nil {
			return nil, nil, fmt.Errorf("scan progress ref rally: %w", err)
		}
		byTask[memberID] = info.id
		rallies[info.id] = info
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("progress ref rallies: %w", err)
	}

	bands := map[string]*model.RallyBand{}
	for id, info := range rallies {
		band, err := s.progressRefRallyBand(ctx, info)
		if err != nil {
			return nil, nil, err
		}
		bands[id] = band
	}
	return byTask, bands, nil
}

// progressRefRallyBand builds one rally's band the way progressRally and
// DraftRallyBand do, branching on the rally's own state: draft counts specs
// (§3.5's footer), anything else counts landed members (§2.1's bar).
func (s *Store) progressRefRallyBand(ctx context.Context, info progressRefRallyInfo) (*model.RallyBand, error) {
	members, err := s.RallyMemberCount(ctx, info.id)
	if err != nil {
		return nil, err
	}
	band := &model.RallyBand{ID: info.id, Title: info.title, Members: members}
	if info.state == "draft" {
		band.Specs, err = s.rallySpecCount(ctx, info.id)
		return band, err
	}
	band.Landed, err = s.rallyLandedCount(ctx, info.id)
	return band, err
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
