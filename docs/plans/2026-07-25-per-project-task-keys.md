---
implements: docs/specs/010-per-project-task-keys.md
---
# Per-project task keys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give each project a required, immutable, unique uppercase key so task IDs become `<KEY>-<n>` counting from 1 per project (e.g. `WL-1`, `SW-1`), replacing the single global `WL-` counter.

**Architecture:** A migration adds `key` and a per-project `next_task_num` counter to `projects`, backfills them from existing task-id prefixes, and drops the global `task_seq`. `CreateTask` allocates IDs from the owning project's counter. `CreateProject` takes a key. The three parsers that hard-code `WL-` are generalised to `[A-Z][A-Z0-9]*-\d+`.

**Tech Stack:** Go 1.26, PostgreSQL (pgx), golang-migrate SQL files, cobra CLI, Kustomize. Store tests run against a real Postgres via `store.OpenTestStore` (skips if unreachable and `CI` unset).

**Spec:** `docs/specs/010-per-project-task-keys.md`

**Test prerequisite:** a local Postgres reachable at `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable` (override with `TEST_POSTGRES_DSN`). Bring one up with `docker compose up -d postgres` if needed. Without it, store tests **skip** (they do not fail), so also run them once with `CI=1` before the final commit to force execution.

---

### Task 1: Generalise the three hard-coded `WL-` ID parsers

Independent of the DB change; keeps `WL-` working (it's a subset of the generalised pattern) and adds support for other prefixes. Do this first.

**Files:**
- Modify: `internal/worktree/worktree.go:18`
- Modify: `internal/store/changes.go:50-51,66-67`
- Modify: `internal/store/ranking.go:163-171`
- Modify: `internal/store/tasks.go:11` (doc comment only)
- Test: `internal/worktree/worktree_test.go`, `internal/store/changes_test.go`, `internal/store/ranking_test.go`

- [ ] **Step 1: Add failing parser tests for a non-`WL` prefix**

In `internal/worktree/worktree_test.go`, add to the existing `ParseDir` test table (or add a new test) cases proving a `SW-` id parses:

```go
func TestParseDirGeneralPrefix(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/x/wt/SW-3-fix-footer", "SW-3"},
		{"/x/wt/SW-3", "SW-3"},
		{"/x/wt/AB12-7-thing", "AB12-7"},
		{"/x/wt/wl-3-nope", ""}, // lowercase prefix still rejected
	} {
		got, ok := ParseDir(tc.path)
		if tc.want == "" && ok {
			t.Errorf("ParseDir(%q) = (%q, true), want ok=false", tc.path, got)
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("ParseDir(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
```

In `internal/store/changes_test.go`, add cases to the ref/body tests:

```go
func TestTaskIDFromRefGeneralPrefix(t *testing.T) {
	if got := TaskIDFromRef("wl/SW-3-slug"); got != "SW-3" {
		t.Errorf("TaskIDFromRef = %q, want SW-3", got)
	}
	if got := TaskIDFromRef("wl/SW-3"); got != "SW-3" {
		t.Errorf("TaskIDFromRef = %q, want SW-3", got)
	}
}

func TestTaskIDFromBodyGeneralPrefix(t *testing.T) {
	if got := TaskIDFromBody("WL-Task: SW-12\nother"); got != "SW-12" {
		t.Errorf("TaskIDFromBody = %q, want SW-12", got)
	}
}
```

In `internal/store/ranking_test.go`, add:

```go
func TestNumericTaskIDGeneralPrefix(t *testing.T) {
	if numericTaskID("SW-9") != 9 {
		t.Errorf("numericTaskID(SW-9) = %d, want 9", numericTaskID("SW-9"))
	}
	if numericTaskID("AB12-10") != 10 {
		t.Errorf("numericTaskID(AB12-10) = %d, want 10", numericTaskID("AB12-10"))
	}
	if numericTaskID("bad") != math.MaxInt {
		t.Errorf("numericTaskID(bad) should be MaxInt")
	}
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/worktree/ ./internal/store/ -run 'GeneralPrefix' -v`
Expected: FAIL (e.g. `SW-3` not parsed; `numericTaskID("SW-9")` returns MaxInt because `TrimPrefix(id,"WL-")` leaves `SW-9`).

- [ ] **Step 3: Generalise the regexes and `numericTaskID`**

`internal/worktree/worktree.go` line 18 — change the comment's `WL-7` example to `SW-7` and the regex:

```go
// dirRe matches a worktree directory's last segment: a task id, optionally
// followed by a lowercase slug. The bare-id form (SW-7) is intentionally valid.
var dirRe = regexp.MustCompile(`^([A-Z][A-Z0-9]*-\d+)(?:-[a-z0-9-]+)?$`)
```

`internal/store/changes.go` — update both patterns (keep the literal `WL-Task:` marker label; generalise only the captured id) and their comments:

```go
// refTaskIDPattern matches worktree branch names of the form
// "wl/<ID>" or "wl/<ID>-<slug>", capturing the task id (e.g. WL-7, SW-3).
var refTaskIDPattern = regexp.MustCompile(`^wl/([A-Z][A-Z0-9]*-[0-9]+)(?:-.*)?$`)
```

```go
// bodyTaskIDPattern matches a "WL-Task: <ID>" marker line (after trimming
// surrounding whitespace), capturing the task id. "WL-Task" is the fixed
// marker label, not the id prefix.
var bodyTaskIDPattern = regexp.MustCompile(`^WL-Task:\s*([A-Z][A-Z0-9]*-[0-9]+)`)
```

`internal/store/ranking.go` — rewrite `numericTaskID` to parse the digits after the last `-`:

```go
// numericTaskID parses the numeric suffix of a <KEY>-<n> id for tiebreaking.
// SW-9 must sort before SW-10, which a plain string compare gets wrong. A
// malformed id sorts last rather than panicking.
func numericTaskID(id string) int {
	i := strings.LastIndex(id, "-")
	if i < 0 {
		return math.MaxInt
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil {
		return math.MaxInt
	}
	return n
}
```

Also update the `Task` struct doc in `internal/store/tasks.go:11`:

```go
// Task is one unit of work, identified by a per-project <KEY>-<n> id.
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/worktree/ ./internal/store/ -run 'GeneralPrefix' -v`
Expected: PASS. Then `go build ./...` — Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/worktree.go internal/worktree/worktree_test.go \
        internal/store/changes.go internal/store/changes_test.go \
        internal/store/ranking.go internal/store/ranking_test.go \
        internal/store/tasks.go
git commit -m "Generalise task-id parsers from WL- to <KEY>-<n>

Task: WL-12"
```

---

### Task 2: Migration 0003, project key, and per-project task-id counter

The backend of the feature. Threads `key` through the store, API, and CLI so the tree compiles and `lode project add --key` works end to end. Store tests prove per-project numbering and key uniqueness.

**Files:**
- Create: `deploy/base/migrations/0003_project_keys.up.sql`, `deploy/base/migrations/0003_project_keys.down.sql`
- Modify: `deploy/base/kustomization.yaml` (configMapGenerator file list)
- Modify: `internal/store/errors.go` (add `ErrKeyTaken`)
- Modify: `internal/store/projects.go` (`Project.Key`; `CreateProject` takes key; `GetProject`/`ListProjects` select key)
- Modify: `internal/store/tasks.go` (`CreateTask` per-project counter)
- Modify: `internal/api/admin.go` (`projectJSON.Key`, `createProjectRequest.Key`, thread through `createProject`, `toProjectJSON`, `mapStoreErr`)
- Modify: `internal/cli/client.go` (`Project.Key`, `CreateProjectInput.Key`)
- Modify: `internal/cmd/project.go` (required `--key` flag)
- Test: `internal/store/tasks_test.go`, `internal/store/projects_test.go`
- Update callers: `internal/cli/client_test.go` (10 sites), `internal/cmd/lifecycle_test.go:127`, `internal/api/admin_test.go:18`

- [ ] **Step 1: Write the migration files**

`deploy/base/migrations/0003_project_keys.up.sql`:

```sql
ALTER TABLE projects ADD COLUMN key text;
ALTER TABLE projects ADD COLUMN next_task_num bigint NOT NULL DEFAULT 1;

-- Backfill key + counter from existing task-id prefixes (data-driven).
-- worklode's tasks are WL-1..WL-11, so it becomes key 'WL', next_task_num 12.
UPDATE projects p SET key = s.prefix, next_task_num = s.maxnum + 1
FROM (SELECT project_id,
             split_part(id, '-', 1)               AS prefix,
             max(split_part(id, '-', 2)::bigint)   AS maxnum
      FROM tasks GROUP BY project_id, split_part(id, '-', 1)) s
WHERE p.id = s.project_id;

-- Fallback for projects with no tasks yet (none in any environment today):
-- derive a key from the id. Assumes the id yields a format-valid key.
UPDATE projects
SET key = upper(substr(regexp_replace(id, '[^a-zA-Z0-9]', '', 'g'), 1, 4))
WHERE key IS NULL;

ALTER TABLE projects ALTER COLUMN key SET NOT NULL;
ALTER TABLE projects ADD CONSTRAINT projects_key_unique UNIQUE (key);
ALTER TABLE projects ADD CONSTRAINT projects_key_format
    CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$');

DROP TABLE task_seq;
```

`deploy/base/migrations/0003_project_keys.down.sql`:

```sql
CREATE TABLE task_seq (id integer PRIMARY KEY CHECK (id = 1), next bigint NOT NULL);
INSERT INTO task_seq (id, next)
VALUES (1, COALESCE((SELECT max(next_task_num) FROM projects), 1));

ALTER TABLE projects DROP CONSTRAINT projects_key_format;
ALTER TABLE projects DROP CONSTRAINT projects_key_unique;
ALTER TABLE projects DROP COLUMN next_task_num;
ALTER TABLE projects DROP COLUMN key;
```

- [ ] **Step 2: Register the migration in the configMapGenerator**

In `deploy/base/kustomization.yaml`, add the two 0003 files under `configMapGenerator[0].files` (alongside the existing 0001/0002 entries):

```yaml
      - migrations/0003_project_keys.up.sql
      - migrations/0003_project_keys.down.sql
```

Verify: `kubectl kustomize deploy/base | grep 0003_project_keys` — Expected: both files listed as ConfigMap keys.

- [ ] **Step 3: Add `ErrKeyTaken`**

In `internal/store/errors.go`, next to `ErrRepoTaken`:

```go
	// ErrKeyTaken means the project key is already used by another project.
	ErrKeyTaken = errors.New("project key already in use")
```

- [ ] **Step 4: Write the failing store test for per-project numbering + key uniqueness**

Add to `internal/store/projects_test.go` (create the file if absent, `package store`):

```go
func TestPerProjectTaskNumbering(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	if err := s.CreateProject(ctx, "worklode", "Worklode", "WL"); err != nil {
		t.Fatalf("create worklode: %v", err)
	}
	if err := s.CreateProject(ctx, "web", "Web", "SW"); err != nil {
		t.Fatalf("create web: %v", err)
	}

	mk := func(project string) string {
		var task *Task
		_, _, err := s.RecordEvent(ctx, "cli", mustExtID(t), "task.created", []byte(`{}`),
			func(tx *sql.Tx, eventID int64) error {
				var e error
				task, e = CreateTask(tx, s.Now(), TaskInput{
					ProjectID: project, Title: "t", Priority: "medium", Kind: "feature",
				})
				return e
			})
		if err != nil {
			t.Fatalf("create task in %s: %v", project, err)
		}
		return task.ID
	}

	if got := mk("worklode"); got != "WL-1" {
		t.Fatalf("first worklode task = %q, want WL-1", got)
	}
	if got := mk("web"); got != "SW-1" {
		t.Fatalf("first web task = %q, want SW-1", got)
	}
	if got := mk("worklode"); got != "WL-2" {
		t.Fatalf("second worklode task = %q, want WL-2", got)
	}
}

func TestCreateProjectDuplicateKey(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "a", "A", "WL"); err != nil {
		t.Fatalf("create a: %v", err)
	}
	err := s.CreateProject(ctx, "b", "B", "WL")
	if !errors.Is(err, ErrKeyTaken) {
		t.Fatalf("duplicate key err = %v, want ErrKeyTaken", err)
	}
}

// mustExtID returns a random external id for test events.
func mustExtID(t *testing.T) string {
	t.Helper()
	id, err := randomExternalID()
	if err != nil {
		t.Fatalf("ext id: %v", err)
	}
	return id
}
```

Note: `randomExternalID()` is defined in package `store` (`internal/store/leases.go:35`), so `mustExtID` can call it directly. Add imports `context`, `database/sql`, `errors`, `testing` to the test file.

- [ ] **Step 5: Run the test to verify it fails (compile error)**

Run: `go test ./internal/store/ -run 'PerProjectTaskNumbering|DuplicateKey' -v`
Expected: FAIL — `CreateProject` takes 3 args, not 4; `ErrKeyTaken` may already exist from Step 3 so the failure is the signature mismatch and missing per-project counter.

- [ ] **Step 6: Update `CreateProject` and add `Project.Key`**

In `internal/store/projects.go`:

```go
type Project struct {
	ID          string
	Name        string
	Key         string
	DeployGated bool
	Focus       []string
}
```

```go
// CreateProject registers a new project with the given immutable key.
func (s *Store) CreateProject(ctx context.Context, id, name, key string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, key) VALUES ($1, $2, $3)`, id, name, key)
	if err != nil {
		if isUniqueViolationOn(err, "projects_key_unique") {
			return ErrKeyTaken
		}
		return fmt.Errorf("insert project %s: %w", id, err)
	}
	return nil
}
```

Update the `SELECT` in `GetProject` and `ListProjects` to `SELECT id, name, key, deploy_gated, focus ...` and add `&p.Key` to each `Scan` in the correct position (after `&p.Name`).

- [ ] **Step 7: Update `CreateTask` to use the per-project counter**

In `internal/store/tasks.go`, replace the `task_seq` block (lines ~88-94) with:

```go
	var n int64
	var key string
	if err := tx.QueryRow(
		`UPDATE projects SET next_task_num = next_task_num + 1
		 WHERE id = $1 RETURNING key, next_task_num - 1`, in.ProjectID,
	).Scan(&key, &n); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %s: %w", in.ProjectID, ErrNotFound)
		}
		return nil, fmt.Errorf("allocate task id: %w", err)
	}
	id := fmt.Sprintf("%s-%d", key, n)
```

Update the `CreateTask` doc comment (line ~79) from "allocates the next WL-<n> id from task_seq" to "allocates the next <KEY>-<n> id from the project's counter".

- [ ] **Step 8: Thread `key` through API and CLI structs so everything compiles**

`internal/api/admin.go`: add `Key string \`json:"key"\`` to both `projectJSON` and `createProjectRequest`; set it in `toProjectJSON` (`Key: p.Key`); pass `req.Key` into `s.st.CreateProject(r.Context(), req.ID, req.Name, req.Key)`; in the `writeJSON(... toProjectJSON(&store.Project{...}))` at the end of `createProject`, add `Key: req.Key`. In `mapStoreErr`, add `errors.Is(err, store.ErrKeyTaken)` to the `StatusConflict` case list.

`internal/cli/client.go`: add `Key string \`json:"key"\`` to `Project` and `CreateProjectInput`.

`internal/cmd/project.go`: in `newProjectAddCmd`, add a `key` var, `cmd.Flags().StringVar(&key, "key", "", "project key: unique uppercase code, immutable (e.g. WL)")`, `cmd.MarkFlagRequired("key")`, and set `Key: key` in the `cli.CreateProjectInput{...}`.

- [ ] **Step 9: Update all existing `CreateProject` call sites to pass a key**

Give each a distinct uppercase key so tests that create two projects don't collide:
- `internal/api/admin_test.go:18` — add `"key": "PROJ"` to the POST body map.
- `internal/cmd/lifecycle_test.go:127` — `cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "PROJ"}`.
- `internal/cli/client_test.go` (lines 70, 134, 263, 304, 334, 372, 425, 485, 513, and any others) — add `Key: "PROJ"` to each `cli.CreateProjectInput{...}`. Grep to be exhaustive: `grep -n 'CreateProjectInput{' internal/cli/client_test.go`.

- [ ] **Step 10: Run the store tests (forcing execution) to verify they pass**

Run: `CI=1 go test ./internal/store/ -run 'PerProjectTaskNumbering|DuplicateKey' -v`
Expected: PASS (`WL-1`, `SW-1`, `WL-2`; duplicate key → `ErrKeyTaken`).
Then the whole tree: `CI=1 go test ./... 2>&1 | tail -20` — Expected: all pass. `go build ./...` — clean.

- [ ] **Step 11: Commit**

```bash
git add deploy/base/migrations/0003_project_keys.up.sql \
        deploy/base/migrations/0003_project_keys.down.sql \
        deploy/base/kustomization.yaml \
        internal/store/errors.go internal/store/projects.go internal/store/tasks.go \
        internal/store/projects_test.go \
        internal/api/admin.go internal/api/admin_test.go \
        internal/cli/client.go internal/cli/client_test.go \
        internal/cmd/project.go internal/cmd/lifecycle_test.go
git commit -m "Add per-project key and per-project task-id counter

Migration 0003 adds projects.key + next_task_num, backfills from existing
task-id prefixes, drops the global task_seq. CreateTask allocates <KEY>-<n>
from the owning project's counter; CreateProject takes an immutable key.

Task: WL-12"
```

---

### Task 3: Key validation and the `KEY` display column

Hardens the user-facing surface: reject a missing/malformed key with a clear 422, and show the key in `project list` / `project add` output.

**Files:**
- Modify: `internal/api/admin.go` (`createProject` format validation)
- Modify: `internal/cli/render.go:75-82` (`ProjectTable` KEY column)
- Test: `internal/api/admin_test.go`, `internal/cli/render_test.go`

- [ ] **Step 1: Write failing API validation tests**

Add to `internal/api/admin_test.go`:

```go
func TestCreateProjectKeyValidation(t *testing.T) {
	_, h, token := newTestServer(t)
	// missing key
	rr := doReq(t, h, "POST", "/api/v1/projects", token,
		map[string]any{"id": "p1", "name": "P1"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing key status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	// malformed key (lowercase)
	rr = doReq(t, h, "POST", "/api/v1/projects", token,
		map[string]any{"id": "p2", "name": "P2", "key": "wl"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad key status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	// duplicate key -> 409
	rr = doReq(t, h, "POST", "/api/v1/projects", token,
		map[string]any{"id": "p3", "name": "P3", "key": "WL"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("first WL status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/projects", token,
		map[string]any{"id": "p4", "name": "P4", "key": "WL"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate WL status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `CI=1 go test ./internal/api/ -run TestCreateProjectKeyValidation -v`
Expected: FAIL — missing/lowercase key currently reaches the DB (500 from the CHECK constraint) rather than 422.

- [ ] **Step 3: Add format validation in the handler**

In `internal/api/admin.go`, add a package-level regexp and validate in `createProject` after the `name` check, before calling the store:

```go
var projectKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
```

```go
	if !projectKeyRe.MatchString(req.Key) {
		writeErr(w, http.StatusUnprocessableEntity,
			"key must be an uppercase code matching ^[A-Z][A-Z0-9]{1,9}$")
		return
	}
```

Ensure `regexp` is imported. (`ErrKeyTaken` → 409 mapping was added in Task 2 Step 8.)

- [ ] **Step 4: Run to verify pass**

Run: `CI=1 go test ./internal/api/ -run TestCreateProjectKeyValidation -v`
Expected: PASS.

- [ ] **Step 5: Add the KEY column to `ProjectTable` (failing test first)**

Add to `internal/cli/render_test.go` (create if absent, `package cli`):

```go
func TestProjectTableShowsKey(t *testing.T) {
	var b strings.Builder
	ProjectTable(&b, []Project{{ID: "worklode", Name: "Worklode", Key: "WL", Repos: []string{"a/b"}}})
	out := b.String()
	if !strings.Contains(out, "KEY") || !strings.Contains(out, "WL") {
		t.Fatalf("ProjectTable output missing KEY/WL:\n%s", out)
	}
}
```

Run: `go test ./internal/cli/ -run TestProjectTableShowsKey -v` — Expected: FAIL.

- [ ] **Step 6: Add the column**

In `internal/cli/render.go`, update `ProjectTable`:

```go
func ProjectTable(w io.Writer, projects []Project) {
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tKEY\tNAME\tDEPLOY-GATED\tREPOS")
	for _, p := range projects {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\n", p.ID, p.Key, p.Name, p.DeployGated, strings.Join(p.Repos, ", "))
	}
	tw.Flush()
}
```

Run: `go test ./internal/cli/ -run TestProjectTableShowsKey -v` — Expected: PASS.

- [ ] **Step 7: Full suite + build**

Run: `CI=1 go test ./... 2>&1 | tail -20` — Expected: all pass.
Run: `go build ./...` — Expected: clean.
Run: `go vet ./...` — Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/api/admin.go internal/api/admin_test.go \
        internal/cli/render.go internal/cli/render_test.go
git commit -m "Validate project key and show it in project list

Task: WL-12"
```

---

## Notes for the executor

- **`WL-Task:` marker label is intentionally NOT renamed** — it is a fixed convention in PR bodies; only the captured id after it is generalised.
- **Key immutability** is enforced by omission: no update path sets `key`. Do not add one.
- **The dev server is already at schema v2**; this migration is 0003 and will apply cleanly on top. Do not touch the running cluster — deployment happens through CI/Flux after merge.
- If Postgres is unreachable locally, store/API tests **skip** silently. Always run the final suite with `CI=1` so skips become failures and real execution is confirmed.
- **Backfill is intentionally not covered by an automated test.** Exercising it needs a stepwise migration harness (apply through 0002, seed legacy `WL-` tasks, apply 0003) the test helpers don't expose, and the only real instance is dev's single `worklode` project. The backfill SQL is straightforward and the ongoing counter mechanic is covered by `TestPerProjectTaskNumbering`. Verify the one-time backfill directly after deploy: `psql -c "select id, key, next_task_num from projects;"` should show `worklode | WL | 13` (max existing id is WL-12). If a future project needs backfill coverage, add an `OpenTestStoreAtVersion` helper then.
