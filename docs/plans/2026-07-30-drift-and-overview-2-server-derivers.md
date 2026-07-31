# Drift & overview 2/3 (spec 007): server-side derivers — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 3. Task numbering is global across the series: this plan
holds Tasks 7–9; `2026-07-30-drift-and-overview-1-repo-derivers.md` (Tasks
1–6) must be merged first;
`2026-07-30-drift-and-overview-3-overview-and-surfaces.md` (Tasks 10–15)
follows.

**Goal:** Land the DB-backed derivers: the store reads they (and part 3's
frontier mirror) need, deriver 4 (deploy projection) and deriver 3
(pr-affects with the GitHub `RepoReader`). Server-internal — the invocation
surface (`POST /api/v1/derive` and the CLI trigger) ships in part 3.

**Architecture:** Both derivers stay pure row/file→triple functions in
`internal/derive`, written through part 1's runner (atomic GSP PUT + hash
short-circuit). Deriver 4 projects store rows through `internal/graphproj`'s
row→triple functions into `observed/deploy`; deriver 3 joins task-bound PRs
with changed files and the target repo's manifest, both fetched at derive
time through a narrow `RepoReader` (GitHub implementation over installation
tokens). The new store reads also export the ranked frontier
(`store.Frontier`) that part 3 mirrors.

**Tech Stack:** Go 1.26, PostgreSQL via `database/sql`, standard-library
testing, `httptest` fakes for the GitHub API and the graph endpoint.

**Spec:** `docs/specs/007-drift-and-overview.md`, read with its amendments:
`docs/specs/014-design-documents-as-graph-objects.md` §5–§6, §10 and
`docs/specs/015-runtime-layer.md` §2–§6. All `ls:`/`lsc:`/`lsid:` prefixes in
the spec read as `wl:`/`wlc:`/`wlid:` (014 §1). See part 1's header for the
full series scope, sibling-plan prerequisites, prior-art map, design calls,
and what is owned elsewhere.

**Prerequisites (landed by part 1):** the `internal/derive` runner
(`derive.Run` — hash short-circuit + atomic GSP PUT into one `observed/*`
graph; `Result`), `iri.DeclaredGraph`/`ObservedGraph`/`Repo`,
`graph.Client.Replace`, and derivers 1–2 with `lode derive`.

Design calls this plan inherits (recorded in part 1, restated because they
shape Tasks 8–9):

- **PR files and manifests are fetched at derive time** through the
  `RepoReader` interface (GitHub implementation over
  `githubauth.AppAuth.InstallationToken`). Spec 007's claim that PR
  changed-file lists are "already ingested by `internal/hooks/github.go`" is
  false in this codebase — nothing in the schema or hooks carries file
  lists — so deriver 3 fetches them from the GitHub API instead; derivers
  stay pull-based and no new ingestion or table is added.
- **PR→Task join** uses the shipped relational binding
  (`pull_requests.task_id` via branch `wt/<id>-<slug>` / body ref,
  `internal/store/changes.go:99,118`), not the spec's resolved-Q1 Issue
  mirror — mirroring does not exist yet (004/008). Swap the join when it
  lands.
- **No migration.** The deriver no-op short circuit stores the input hash as
  a triple inside the deriver's own graph; nothing here touches Postgres
  schema.
- **Serialization:** N-Triples via `graphproj.Render` for every deriver; GSP
  PUT with `Content-Type: application/n-triples`.

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/derive/praffects.go` | deriver 3: task-bound PRs × changed files × manifest → `wl:affects` |
| `internal/derive/praffects_test.go` | fake `RepoReader`; join, mapping, missing-manifest skip |
| `internal/derive/deploy.go` | deriver 4: store rows → `graphproj` runtime triples in `observed/deploy` |
| `internal/derive/deploy_test.go` | seeded store rows → exact triple set (needs Postgres) |
| `internal/derive/github.go` | `RepoReader` over the GitHub API with installation tokens |
| `internal/derive/github_test.go` | httptest GitHub: files pagination, contents decode, 404 |
| `internal/store/overview_test.go` | new store reads, incl. frontier/claim-order mirror |

**Modified files**

| Path | Change |
|---|---|
| `internal/store/ranking.go` | export `Frontier` (ranked ready set + fan-out, no claim) |
| `internal/store/tasks.go` | add `AllBlockEdges` |
| `internal/store/changes.go` | add `TaskPRs` |
| `internal/store/artifacts.go` | add `AllDeployments`, `AllArtifactsByID` |
| `internal/store/delivery.go` | add `HasMainCommit`, `AllReleaseFrontiers` |

**Test commands**

- Pure packages (no services): `go test ./internal/derive/...` (deriver 3 and
  the `RepoReader` run against fakes)
- Postgres-backed (deriver 4 + store reads):
  `docker compose up -d postgres && go test ./internal/store/... ./internal/derive/...`

---

## Task 7: Store reads for the server-side derivers and the frontier

**Files:**
- Modify: `internal/store/changes.go`, `internal/store/artifacts.go`, `internal/store/delivery.go`, `internal/store/tasks.go`, `internal/store/ranking.go`
- Test: `internal/store/overview_test.go` (create)

- [ ] **Step 1: Write the failing test**

`internal/store/overview_test.go` (`package store`; use the existing
`OpenTestStore` and task/edge helpers from `tasks_test.go` /
`ranking_test.go` — create tasks through the same fixture path those tests
use):

```go
package store

import (
	"context"
	"testing"
)

func TestTaskPRs(t *testing.T) {
	s := OpenTestStore(t)
	// Seed one PR bound to a task and one unbound, through the event log as
	// hooks do (mirror the seeding style of changes_test.go).
	seedTaskWithID(t, s, "WL-1")
	seedPR(t, s, "acme/app", 1, "wt/WL-1-fix")   // branch join → task_id=WL-1
	seedPR(t, s, "acme/app", 2, "unrelated-branch")

	prs, err := s.TaskPRs(context.Background())
	if err != nil {
		t.Fatalf("TaskPRs: %v", err)
	}
	if len(prs) != 1 || prs[0].Repo != "acme/app" || prs[0].Number != 1 || prs[0].TaskID != "WL-1" {
		t.Fatalf("TaskPRs = %+v; want the one task-bound PR", prs)
	}
}

func TestAllBlockEdges(t *testing.T) {
	s := OpenTestStore(t)
	seedTaskWithID(t, s, "WL-1")
	seedTaskWithID(t, s, "WL-2")
	seedEdge(t, s, "WL-1", "WL-2", "blocks")
	seedEdge(t, s, "WL-2", "WL-1", "child_of") // must not appear

	edges, err := s.AllBlockEdges(context.Background())
	if err != nil {
		t.Fatalf("AllBlockEdges: %v", err)
	}
	if len(edges) != 1 || edges[0].FromTask != "WL-1" || edges[0].ToTask != "WL-2" {
		t.Fatalf("AllBlockEdges = %+v; want exactly WL-1 blocks WL-2", edges)
	}
}

func TestFrontierMirrorsClaimNextOrder(t *testing.T) {
	s := OpenTestStore(t)
	seedReadyTask(t, s, "WL-1", "low")
	seedReadyTask(t, s, "WL-2", "critical")

	tasks, fanOut, err := s.Frontier(context.Background(), "")
	if err != nil {
		t.Fatalf("Frontier: %v", err)
	}
	if len(tasks) != 2 || tasks[0].ID != "WL-2" {
		t.Fatalf("Frontier order = %v; critical priority must sort first", ids(tasks))
	}
	_ = fanOut

	// The mirror contract: Frontier's head equals ClaimNext's dry-run pick.
	res, err := s.ClaimNext(context.Background(), ClaimNextOpts{DryRun: true})
	if err != nil || res.Task == nil {
		t.Fatalf("ClaimNext dry run: %+v, %v", res, err)
	}
	if res.Task.ID != tasks[0].ID {
		t.Fatalf("frontier head %s != claim-next pick %s", tasks[0].ID, res.Task.ID)
	}
}

func TestAllDeploymentsAndFrontiers(t *testing.T) {
	s := OpenTestStore(t)
	seedDeployment(t, s, "prod", "flux_kustomization", "graph-server")

	ds, err := s.AllDeployments(context.Background())
	if err != nil || len(ds) != 1 {
		t.Fatalf("AllDeployments = %v, %v; want 1 row", ds, err)
	}
	if _, err := s.AllArtifactsByID(context.Background()); err != nil {
		t.Fatalf("AllArtifactsByID: %v", err)
	}
	if _, err := s.AllReleaseFrontiers(context.Background()); err != nil {
		t.Fatalf("AllReleaseFrontiers: %v", err)
	}
	if ok, err := s.HasMainCommit(context.Background(), "acme/app", "deadbeef"); err != nil || ok {
		t.Fatalf("HasMainCommit(unknown) = %v, %v; want false, nil", ok, err)
	}
}
```

Write the small `seed*`/`ids` helpers at the bottom of the file against the
actual fixture helpers the store tests already use (`tasks_test.go`,
`ranking_test.go`, `changes_test.go`, `artifacts_test.go` each contain the
insertion pattern to copy — event-log wrapped, exactly as production writes).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run 'TestTaskPRs|TestAllBlock|TestFrontier|TestAllDeployments'`
Expected: FAIL — undefined methods.

- [ ] **Step 3: Write the implementations**

`internal/store/changes.go`:

```go
// PRRef is the minimal PR identity the pr-affects deriver needs.
type PRRef struct {
	Repo   string
	Number int64
	TaskID string
}

// TaskPRs returns every pull request bound to a task, ordered by repo then
// number. Unbound PRs are invisible to the deriver: with no task join there
// is no wl:affects subject.
func (s *Store) TaskPRs(ctx context.Context) ([]PRRef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo, number, task_id FROM pull_requests
		 WHERE task_id IS NOT NULL ORDER BY repo, number`)
	if err != nil {
		return nil, fmt.Errorf("task prs: %w", err)
	}
	defer rows.Close()
	var out []PRRef
	for rows.Next() {
		var p PRRef
		if err := rows.Scan(&p.Repo, &p.Number, &p.TaskID); err != nil {
			return nil, fmt.Errorf("scan task pr: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
```

`internal/store/tasks.go`:

```go
// AllBlockEdges returns every 'blocks' edge, for the overview critical-path
// join (spec 007: the DAG spans backbone blocks + KG requires).
func (s *Store) AllBlockEdges(ctx context.Context) ([]Edge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_task, to_task FROM task_edges WHERE type = 'blocks'`)
	if err != nil {
		return nil, fmt.Errorf("all block edges: %w", err)
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		e := Edge{Type: "blocks"}
		if err := rows.Scan(&e.FromTask, &e.ToTask); err != nil {
			return nil, fmt.Errorf("scan block edge: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

`internal/store/ranking.go` (compose the existing private pieces; keep
`ClaimNext` untouched):

```go
// Frontier returns the ready, unblocked, unleased tasks in the exact rank
// order ClaimNext consumes, plus the blocking fan-out map — the read-only
// overview mirror of the authoritative frontier (spec 007 §4.5). It claims
// nothing.
func (s *Store) Frontier(ctx context.Context, projectID string) ([]Task, map[string]int, error) {
	candidates, err := s.readyCandidates(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	fanOut, err := s.BlockingFanOut(ctx)
	if err != nil {
		return nil, nil, err
	}
	projectIDs := make([]string, 0, len(candidates))
	for _, t := range candidates {
		projectIDs = append(projectIDs, t.ProjectID)
	}
	focus, err := s.projectFocusMap(ctx, projectIDs)
	if err != nil {
		return nil, nil, err
	}
	in := make([]rankInput, len(candidates))
	for i, t := range candidates {
		in[i] = rankInput{Task: t, Focus: focus[t.ProjectID], FanOut: fanOut[t.ID]}
	}
	return rankTasks(in, false), fanOut, nil
}
```

`internal/store/artifacts.go`:

```go
// AllDeployments returns every deployments row, for the deploy deriver's
// full-replace projection.
func (s *Store) AllDeployments(ctx context.Context) ([]Deployment, error) {
	// Same SELECT and scanDeployment as ListDeployments, without the
	// environment filter and ordered by (environment, target_kind, target_name).
}

// AllArtifactsByID returns every artifacts row keyed by id, so the deploy
// deriver can resolve deployments.artifact_id → prov:used in one pass.
func (s *Store) AllArtifactsByID(ctx context.Context) (map[int64]Artifact, error) {
	// SELECT the artifact columns scanArtifact reads; build the map.
}
```

(Both bodies are mechanical copies of the adjacent `ListDeployments` /
`scanArtifact` code with the filter dropped — write them in full, matching
the existing column lists exactly.)

`internal/store/delivery.go`:

```go
// ReleaseFrontier row for the deploy deriver's wl:covers projection.
type ReleaseFrontierRow struct {
	Repo string
	Tag  string
	SHA  string
}

// AllReleaseFrontiers returns each repo's release frontier rows joined to
// the frontier commit's sha.
func (s *Store) AllReleaseFrontiers(ctx context.Context) ([]ReleaseFrontierRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rf.repo, rf.tag, mc.sha
		FROM release_frontiers rf JOIN main_commits mc ON mc.id = rf.main_id
		ORDER BY rf.repo, rf.tag`)
	// scan loop as above
}

// HasMainCommit reports whether sha is a recorded main_commits row for
// repo — the CommitKnown guard graphproj.ArtifactTriples requires (015 §6).
func (s *Store) HasMainCommit(ctx context.Context, repo, sha string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM main_commits WHERE repo = $1 AND sha = $2`,
		repo, sha).Scan(&n)
	return n > 0, err
}
```

Match `release_frontiers`/`main_commits` column names against
`deploy/base/migrations/0005_delivery.up.sql:29-66` before finalizing the
SQL.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestTaskPRs|TestAllBlock|TestFrontier|TestAllDeployments' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add overview and deriver store reads"
```

---

## Task 8: Deriver 4 — deploy projection

**Files:**
- Create: `internal/derive/deploy.go`
- Test: `internal/derive/deploy_test.go`

- [ ] **Step 1: Write the failing test**

`internal/derive/deploy_test.go` (needs Postgres; skip like the store suite
by going through `store.OpenTestStore`):

```go
package derive_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestDeployTriplesProjectsRows(t *testing.T) {
	s := store.OpenTestStore(t)
	seedArtifactAndDeployment(t, s) // helper: one docker_image artifact +
	// one prod flux_kustomization deployment referencing it, seeded through
	// the event log exactly as artifacts_test.go does.

	doc, err := derive.DeployTriples(context.Background(), s)
	if err != nil {
		t.Fatalf("DeployTriples: %v", err)
	}
	got := string(doc)
	for _, want := range []string{
		"ontology#Deployment", "ontology#Artifact",
		"id/environment/prod", "id/environment/dev",
		"ontology#toEnvironment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestDeployTriplesDeterministic(t *testing.T) {
	s := store.OpenTestStore(t)
	seedArtifactAndDeployment(t, s)
	a, err := derive.DeployTriples(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	b, err := derive.DeployTriples(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("re-deriving unchanged rows is not byte-identical")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/derive/ -run TestDeploy`
Expected: FAIL — `undefined: derive.DeployTriples`

- [ ] **Step 3: Write the implementation**

```go
package derive

import (
	"context"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// DeployTriples derives the observed/deploy document (spec 007 deriver 4,
// vocabulary and guards per spec 015 §2–§6): projection of the already-
// ingested artifacts, deployments, environments, commit links and release
// frontiers. Projection, not new build (D6) — every triple comes from a row.
func DeployTriples(ctx context.Context, s *store.Store) ([]byte, error) {
	var ts []graphproj.Triple
	ts = append(ts, graphproj.EnvironmentTriples()...)

	artifacts, err := s.AllArtifactsByID(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}
	for _, a := range artifacts {
		a := a
		known := func(sha string) bool {
			ok, err := s.HasMainCommit(ctx, a.Repo, sha)
			return err == nil && ok
		}
		ts = append(ts, graphproj.ArtifactTriples(a, known)...)
		if a.Repo != "" && a.SourceSHA != "" && known(a.SourceSHA) {
			ts = append(ts, graphproj.CommitTriples(graphproj.GitHubHost, a.Repo, a.SourceSHA)...)
		}
	}

	deployments, err := s.AllDeployments(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}
	for _, d := range deployments {
		var artifact *store.Artifact
		if d.ArtifactID != nil {
			if a, ok := artifacts[*d.ArtifactID]; ok {
				artifact = &a
			}
		}
		ts = append(ts, graphproj.DeploymentTriples(d, artifact)...)
	}

	frontiers, err := s.AllReleaseFrontiers(ctx)
	if err != nil {
		return nil, fmt.Errorf("deploy deriver: %w", err)
	}
	for _, f := range frontiers {
		ts = append(ts, graphproj.ReleaseCoversTriples(f.Repo, f.Tag, f.SHA)...)
	}
	return graphproj.Render(ts), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `docker compose up -d postgres && go test ./internal/derive/ -run TestDeploy -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/derive
git commit -m "Derive the observed deploy projection from ingested rows"
```

---

## Task 9: Deriver 3 — pr-affects (with the GitHub RepoReader)

**Files:**
- Create: `internal/derive/praffects.go`, `internal/derive/github.go`
- Test: `internal/derive/praffects_test.go`, `internal/derive/github_test.go`

- [ ] **Step 1: Write the failing deriver test**

```go
package derive_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/derive"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// fakeRepoReader serves manifests and PR file lists from maps.
type fakeRepoReader struct {
	manifests map[string]string   // repo → components.yaml
	files     map[string][]string // "repo#number" → changed paths
}

func (f *fakeRepoReader) FileAt(_ context.Context, repo, path string) ([]byte, error) {
	if path != ".worklode/components.yaml" {
		return nil, errors.New("unexpected path " + path)
	}
	m, ok := f.manifests[repo]
	if !ok {
		return nil, derive.ErrNotFound
	}
	return []byte(m), nil
}

func (f *fakeRepoReader) PRFiles(_ context.Context, repo string, number int64) ([]string, error) {
	return f.files[repoNum(repo, number)], nil
}

func repoNum(repo string, n int64) string { return repo + "#" + string(rune('0'+n)) }

func TestPRAffectsTriples(t *testing.T) {
	prs := []store.PRRef{
		{Repo: "sunstoneinstitute/research-stack", Number: 1, TaskID: "WL-7"},
		{Repo: "sunstoneinstitute/unmapped", Number: 2, TaskID: "WL-8"},
	}
	rr := &fakeRepoReader{
		manifests: map[string]string{"sunstoneinstitute/research-stack": importsManifest},
		files: map[string][]string{
			repoNum("sunstoneinstitute/research-stack", 1): {
				"internal/ingest/x.go", "internal/graph/y.go", "README.md",
			},
		},
	}
	doc, skipped, err := derive.PRAffectsTriples(context.Background(), prs, rr)
	if err != nil {
		t.Fatalf("PRAffectsTriples: %v", err)
	}
	got := string(doc)
	task := "<https://worklode.io/ns/id/task/WL-7>"
	for _, comp := range []string{"research-stack/ingest", "research-stack/graphsrv"} {
		if !strings.Contains(got,
			task+" <https://worklode.io/ns/ontology#affects> <https://worklode.io/ns/id/component/github.com/sunstoneinstitute/"+comp+"> .") {
			t.Errorf("missing wl:affects to %s in:\n%s", comp, got)
		}
	}
	if strings.Contains(got, "WL-8") {
		t.Errorf("PR in a manifest-less repo produced triples:\n%s", got)
	}
	if len(skipped) != 1 || skipped[0] != "sunstoneinstitute/unmapped" {
		t.Fatalf("skipped = %v; want the manifest-less repo reported", skipped)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/derive/ -run TestPRAffects`
Expected: FAIL — `undefined: derive.PRAffectsTriples`

- [ ] **Step 3: Write the deriver**

`internal/derive/praffects.go`:

```go
package derive

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const wlAffects = "https://worklode.io/ns/ontology#affects"

// ErrNotFound is returned by RepoReader implementations for a missing file.
var ErrNotFound = errors.New("not found")

// RepoReader is the slice of the forge API the pr-affects deriver needs.
// Spec 007 deriver 3's inputs are pulled fresh on each run — derivers are
// cheap to re-run and hold no state.
type RepoReader interface {
	// FileAt fetches a file at the repo's default branch head.
	FileAt(ctx context.Context, repo, path string) ([]byte, error)
	// PRFiles lists a pull request's changed file paths.
	PRFiles(ctx context.Context, repo string, number int64) ([]string, error)
}

// PRAffectsTriples derives the observed/pr-affects document: for every
// task-bound PR, each changed path is mapped to a component through the
// repo's manifest and emitted as <task> wl:affects <component>. Repos
// without a manifest are skipped and reported, never fatal.
func PRAffectsTriples(ctx context.Context, prs []store.PRRef, rr RepoReader) (doc []byte, skippedRepos []string, err error) {
	manifests := map[string]*manifest.Manifest{}
	skipped := map[string]bool{}
	var ts []graphproj.Triple
	for _, pr := range prs {
		m, ok := manifests[pr.Repo]
		if !ok && !skipped[pr.Repo] {
			data, ferr := rr.FileAt(ctx, pr.Repo, ".worklode/components.yaml")
			switch {
			case errors.Is(ferr, ErrNotFound):
				skipped[pr.Repo] = true
			case ferr != nil:
				return nil, nil, fmt.Errorf("manifest for %s: %w", pr.Repo, ferr)
			default:
				if m, ferr = manifest.Parse(data); ferr != nil {
					return nil, nil, fmt.Errorf("manifest for %s: %w", pr.Repo, ferr)
				}
				manifests[pr.Repo] = m
			}
		}
		m = manifests[pr.Repo]
		if m == nil {
			continue
		}
		files, ferr := rr.PRFiles(ctx, pr.Repo, pr.Number)
		if ferr != nil {
			return nil, nil, fmt.Errorf("files of %s#%d: %w", pr.Repo, pr.Number, ferr)
		}
		for _, f := range files {
			if c, ok := m.Match(f); ok {
				ts = append(ts, graphproj.Triple{S: iri.Task(pr.TaskID), P: wlAffects, O: c.IRI})
			}
		}
	}
	for r := range skipped {
		skippedRepos = append(skippedRepos, r)
	}
	sort.Strings(skippedRepos)
	return graphproj.Render(ts), skippedRepos, nil
}
```

- [ ] **Step 4: Write the GitHub RepoReader with its test**

`internal/derive/github_test.go` — an `httptest` server asserting: `FileAt`
GETs `/repos/{repo}/contents/{path}`, decodes the base64 `content` field,
maps 404 to `ErrNotFound`; `PRFiles` GETs `/repos/{repo}/pulls/{n}/files`
with `per_page=100` and follows `Link: rel="next"` pagination, collecting
`filename` fields; both send `Authorization: Bearer <installation token>`.
Write the three tests in the established `recordingServer` style.

`internal/derive/github.go`:

```go
package derive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

// GitHubReader implements RepoReader over the GitHub REST API using App
// installation tokens (internal/githubauth/app.go:94). BaseURL is
// overridable in tests, mirroring AppAuth.
type GitHubReader struct {
	Auth    *githubauth.AppAuth
	BaseURL string // default https://api.github.com
}

func (g *GitHubReader) get(ctx context.Context, repo, url string, out any) (status int, next string, err error) {
	tok, err := g.Auth.InstallationToken(ctx, repo)
	if err != nil {
		return 0, "", fmt.Errorf("installation token for %s: %w", repo, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
	if resp.StatusCode == http.StatusOK && out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return resp.StatusCode, "", fmt.Errorf("decode %s: %w", url, err)
		}
	}
	return resp.StatusCode, nextLink(resp.Header.Get("Link")), nil
}

// FileAt implements RepoReader via the contents API.
func (g *GitHubReader) FileAt(ctx context.Context, repo, path string) ([]byte, error) {
	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	status, _, err := g.get(ctx, repo,
		g.base()+"/repos/"+repo+"/contents/"+path, &payload)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET contents %s/%s: HTTP %d", repo, path, status)
	}
	return base64.StdEncoding.DecodeString(payload.Content)
}

// PRFiles implements RepoReader via the PR files API, following pagination.
func (g *GitHubReader) PRFiles(ctx context.Context, repo string, number int64) ([]string, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%d/files?per_page=100", g.base(), repo, number)
	var out []string
	for url != "" {
		var page []struct {
			Filename string `json:"filename"`
		}
		status, next, err := g.get(ctx, repo, url, &page)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("GET pr files %s#%d: HTTP %d", repo, number, status)
		}
		for _, f := range page {
			out = append(out, f.Filename)
		}
		url = next
	}
	return out, nil
}

func (g *GitHubReader) base() string {
	if g.BaseURL != "" {
		return g.BaseURL
	}
	return "https://api.github.com"
}

// nextLink extracts the rel="next" URL from a Link header ("" if none).
func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		if strings.Contains(part, `rel="next"`) {
			if i, j := strings.Index(part, "<"), strings.Index(part, ">"); i >= 0 && j > i {
				return part[i+1 : j]
			}
		}
	}
	return ""
}
```

(Add `"strings"` to the imports.)

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/derive/ -run 'TestPRAffects|TestGitHubReader' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/derive
git commit -m "Derive task-affects-component edges from PR files"
```

---

## Acceptance criteria → task map

The full-series map is split across the three parts; this part covers:

| Spec acceptance criterion | Covered by |
|---|---|
| Derivers idempotent + confined (derivers 3–4) | Tasks 7–9 |
| Ordering contract: frontier matches the backbone | Task 7 (`TestFrontierMirrorsClaimNextOrder`); the API-level check lands in part 3, Task 12 |
