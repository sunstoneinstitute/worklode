# Task secrets 1/3: server core — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 1 of 3. Task numbering is global across the series: this plan
holds Tasks 1–5; `2026-07-30-task-secrets-2-cli-runtime.md` holds Tasks 6–10;
`2026-07-30-task-secrets-3-ceremony-and-rollout.md` holds Tasks 11–14. Each
part must be merged before the next starts.

- **Part 1 — server core (Tasks 1–5):** name validation, the `tasks.secrets`
  column, `secrets` through the task API, catalog format and resolution,
  `GET /api/v1/secrets/catalog`, and the `secrets_materialized` event.
  *Checkpoint:* the backbone fully supports secrets; nothing is client-visible
  yet.
- **Part 2 — CLI runtime (Tasks 6–10):** client methods, `--secrets` flags and
  brief display, keystore/manifest/env-file, `lode secrets
  catalog|pack|purge|status`, `lode secrets exec`. *Checkpoint:* the CLI
  secrets machinery works standalone against a part-1 server.
- **Part 3 — ceremony and rollout (Tasks 11–14):** the claim-time ceremony in
  `lode next`/`lode resume`, purge on every release path, deployment wiring for
  the catalog, documentation and the `lode-secrets` skill. *Checkpoint:* the
  full feature is live end to end.

**Goal:** Tasks declare org-catalog secret names; `lode next` runs a claim-time
ceremony (consent → one `op run` → OS keystore) so `lode secrets exec` can
inject exactly those values unattended, with names-only audit events and purge
on every release path. This part builds the server half.

**Architecture:** The backbone gains a `secrets` name list on Task (jsonb
column, wire field, `--secrets` flag) and two endpoints: an authenticated
`GET /api/v1/secrets/catalog` serving a ConfigMap-mounted TOML catalog
(name → `op://` ref + policy), and `POST /api/v1/tasks/{id}/secrets-materialized`
recording a names-only event. A new `internal/secrets` package owns the pure
pieces — name validation, catalog parsing, env-file rendering, the go-keyring
keystore (service `worklode:<task-id>`), and a names-only manifest at
`~/.cache/worklode/secrets/<task-id>.json`. `lode next`/`lode resume` host the
ceremony; `lode secrets exec|status|catalog|purge|pack` are the runtime
surface; `lode done`, `lode block`, and the worktree-remove/exit hooks purge.
Worklode never stores, logs, or transports a secret value — values flow
`op run` → env of `lode secrets pack` → keystore → env of the exec'd child.
This part lands the migration, the store/API wiring, `internal/secrets`'
validation and catalog halves, and both endpoints; parts 2–3 build the CLI.

**Tech Stack:** Go 1.26, cobra CLI, `net/http` mux, PostgreSQL via
`database/sql` (pgx stdlib), `github.com/zalando/go-keyring` (already a
dependency; `keyring.MockInit()` in tests), 1Password CLI (`op`) at runtime
only — never in tests.

**Spec:** `docs/specs/017-task-secrets.md`

---

## What exists vs. what this builds

Nothing of spec 017 is implemented. Grounding points in today's code:

- Claim flow to append the ceremony to: `runNext` at
  `internal/cmd/lifecycle.go:129` (claim → worktree → rebind → brief);
  re-materialization host: `runResume` at `internal/cmd/lifecycle.go:266`.
- Release paths that must purge: `newDoneCmd` (`internal/cmd/lifecycle.go:304`),
  `newBlockCmd` (`internal/cmd/lifecycle.go:336`), `handleWorktreeRemove` and
  `handleWorktreeExit` (`internal/hookrun/hookrun.go:452,561`).
- Worktree guard: `worktree.ParseDir` (`internal/worktree/worktree.go:30`) via
  `resolveWorktreeTask` (`internal/cmd/lifecycle.go:38`).
- Keychain precedent: `internal/cli/tokenstore.go` (go-keyring, mocked with
  `keyring.MockInit()` in `internal/cli/tokenstore_test.go:12`).
- Event log: `store.RecordEvent`/`store.LogChange`
  (`internal/store/events.go:34,122`); timeline surfaces `state_log` rows
  generically (`internal/api/timeline.go:108`), so a names-only `LogChange`
  appears there with no extra rendering work.
- Server env config: `internal/cmd/serve.go:66-83`; deployment manifests:
  `deploy/base/` (migrations ship via the `worklode-migrations`
  configMapGenerator in `deploy/base/kustomization.yaml`).

**Migration number:** provisional. Ids are assigned sequentially at execution
time, in the order plans are actually executed, by the migration-id script on
main. `0008` is the current next-free (`0001`–`0005` on main; `0006` and `0007`
claimed by the in-flight `task-hierarchy` and `skills-task3` worktrees), so the
steps below use it and expect renumbering. golang-migrate applies versions in
order and tolerates gaps, so any assigned id lands cleanly.

**Plan-level decisions (deviations from spec prose, all deliberate):**

1. **Linux keystore = Secret Service via go-keyring**, not the spec's
   ssh-agent-encrypted file. go-keyring is already the module's keystore for
   bearer tokens and covers macOS Keychain + Linux Secret Service with one
   API. Same CLI surface, strictly less new code; headless Linux without a
   Secret Service degrades to the documented block-signal path.
2. **Declining consent skips only the non-baseline set**; baseline secrets are
   exempt from consent per the spec's own catalog semantics and still pack.
3. **The materialized-names manifest lives at
   `~/.cache/worklode/secrets/<task-id>.json`**, not in the worktree: keyring
   has no enumeration API, and purge must work after the worktree is deleted
   (the `handleWorktreeRemove` path). Names only — never values or refs.
4. **The `lode-secrets` skill ships as a PR to `sunstoneinstitute/claude-plugins`**
   (`plugins/sunstone-dev/skills/lode-secrets/`), next to `worklode-onboarding`
   — this repo has no plugin skill directory. Task 14 (part 3) carries the full
   content.

---

## File Structure

The tables below cover the whole series; the part that owns each file is named
in the right-hand column where it is not part 1.

**New files**

| Path | Responsibility |
|---|---|
| `deploy/base/migrations/0008_task_secrets.up.sql` / `.down.sql` | `tasks.secrets jsonb NOT NULL DEFAULT '[]'` |
| `internal/secrets/names.go` | `ValidName` — the `^[A-Z][A-Z0-9_]*$` gate everything shares |
| `internal/secrets/catalog.go` | `Entry`, `Catalog`, `ParseCatalog` (minimal TOML subset), `Resolve` |
| `internal/secrets/catalog_test.go` | parse the spec's example, comments, errors, resolve/baseline split |
| `internal/secrets/keystore.go` | (part 2) `Put`/`Fetch`/`Del`/`PurgeTask` on service `worklode:<task-id>` |
| `internal/secrets/manifest.go` | (part 2) names-only manifest load/save/remove under `~/.cache/worklode/secrets/` |
| `internal/secrets/envfile.go` | (part 2) `WriteEnvFile` — `NAME=op://…` lines, 0600, refs only |
| `internal/secrets/secrets_test.go` | (part 2) keystore (MockInit), manifest, envfile, purge |
| `internal/api/secrets.go` | `GET /api/v1/secrets/catalog`, `POST /api/v1/tasks/{id}/secrets-materialized` |
| `internal/api/secrets_test.go` | auth (401), unconfigured (404), names-only event, 422 on bad names |
| `internal/cmd/secrets.go` | (part 2) `lode secrets catalog\|status\|exec\|purge\|pack` |
| `internal/cmd/secrets_test.go` | (part 2) pack/exec/purge/status against MockInit + fake worktree |
| `internal/cmd/secretsceremony.go` | (part 3) the claim-time ceremony; injectable `opRunFunc` |
| `internal/cmd/secretsceremony_test.go` | (part 3) one op call, decline, missing-name warning, catalog-down, no-op-binary |
| `deploy/base/secrets-catalog.yaml` | (part 3) `worklode-secrets-catalog` ConfigMap (empty placeholder catalog) |

**Modified files**

| Path | Change |
|---|---|
| `internal/store/tasks.go` | `Task.Secrets`, `TaskInput.Secrets`, `CreateTask`, `UpdateTaskFields`, `scanTask` |
| `internal/store/tasks_test.go` | secrets round-trip, bad-name rejection |
| `internal/api/tasks.go` | wire `secrets` on create/patch/taskJSON; validate names |
| `internal/api/tasks_test.go` | create/patch/brief carry secrets; 422 on bad name |
| `internal/api/server.go:34-73,279-304` | `Config.SecretsCatalogPath`; register the two routes |
| `internal/cmd/serve.go:66-83` | read `LODE_SECRETS_CATALOG_PATH` |
| `internal/cli/client.go` | (part 2) `Task.Secrets`, input fields, `SecretsCatalog`, `RecordSecretsMaterialized` |
| `internal/cli/client_test.go` | (part 2) catalog + record round-trips |
| `internal/cmd/task.go` | (part 2) `--secrets` on `task add`/`task edit`; `printBrief` shows the names |
| `internal/cmd/lifecycle.go` | (part 3) ceremony in `runNext`/`runResume`; purge in `done`/`block` |
| `internal/hookrun/hookrun.go` | (parts 2–3) brief line; purge in `handleWorktreeRemove`/`handleWorktreeExit` |
| `internal/hookrun/hookrun_test.go` | (part 3) purge on remove |
| `deploy/base/kustomization.yaml` | migration files (part 1); `secrets-catalog.yaml` resource (part 3) |
| `deploy/base/configmap.yaml` | (part 3) `LODE_SECRETS_CATALOG_PATH` |
| `deploy/base/deployment.yaml` | (part 3) mount the catalog ConfigMap |
| `README.md` | (part 3) document declaration, ceremony, `lode secrets`, degradation |

**Out of repo (Task 14, part 3):** `sunstoneinstitute/claude-plugins` PR adding
`plugins/sunstone-dev/skills/lode-secrets/SKILL.md`.

**Test commands**

- Pure packages: `go test ./internal/secrets/...`
- Postgres-backed (`store.OpenTestStore`): `go test ./internal/store/... ./internal/api/...`
- CLI/hooks (keyring mocked, no Postgres unless noted): `go test ./internal/cmd/... ./internal/cli/... ./internal/hookrun/...`
- Everything: `go test ./...`

No test may shell out to `op` or touch a real keychain: every keystore test
starts with `keyring.MockInit()`, and the ceremony's `op run` step is behind
the swappable `opRunFunc`.

---

## Task 1: Secret-name validation and the `tasks.secrets` column

**Files:**
- Create: `internal/secrets/names.go`, `deploy/base/migrations/0008_task_secrets.up.sql`, `deploy/base/migrations/0008_task_secrets.down.sql`
- Modify: `internal/store/tasks.go`, `deploy/base/kustomization.yaml`
- Test: `internal/secrets/names_test.go`, `internal/store/tasks_test.go` (append)

- [ ] **Step 1: Write the failing name-validation test**

`internal/secrets/names_test.go`:

```go
package secrets

import "testing"

func TestValidName(t *testing.T) {
	valid := []string{"GITHUB_TOKEN", "KUBECONFIG_HZDEV", "A", "X1_Y2"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false; want true", n)
		}
	}
	invalid := []string{"", "github_token", "1TOKEN", "_TOKEN", "GITHUB-TOKEN",
		"GITHUB TOKEN", "op://Employee/x", "A=B"}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true; want false", n)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/secrets/...`
Expected: FAIL — no package `internal/secrets`.

- [ ] **Step 3: Implement `names.go`**

```go
// Package secrets implements the client-side half of spec 017 (task-declared
// secrets): the org catalog format, the OS keystore holding materialized
// values, the names-only manifest, and the op-run env-file template. The
// package never logs, serializes, or persists a secret value — values exist
// only in process environments and the OS keystore.
package secrets

import "regexp"

// nameRE is the spec-017 secret-name grammar: env-var style, org-unique.
var nameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// ValidName reports whether s is a well-formed secret name. Everything that
// stores or transmits secret names (task field, event payload, catalog keys)
// gates on this, which is what keeps values and op:// refs out of those
// channels by construction.
func ValidName(s string) bool { return nameRE.MatchString(s) }
```

Run: `go test ./internal/secrets/...` — PASS.

- [ ] **Step 4: Write the migration**

`deploy/base/migrations/0008_task_secrets.up.sql`:

```sql
-- Spec 017: tasks declare which org-catalog secrets they need, by symbolic
-- name. Names only — values and op:// refs never enter the backbone.
ALTER TABLE tasks ADD COLUMN secrets jsonb NOT NULL DEFAULT '[]'::jsonb;
```

`deploy/base/migrations/0008_task_secrets.down.sql`:

```sql
ALTER TABLE tasks DROP COLUMN secrets;
```

In `deploy/base/kustomization.yaml`, append to the `worklode-migrations`
generator's `files:` list:

```yaml
      - migrations/0008_task_secrets.up.sql
      - migrations/0008_task_secrets.down.sql
```

(0006–0008 are claimed by in-flight plans; golang-migrate tolerates the gap.)

- [ ] **Step 5: Write the failing store test**

Append to `internal/store/tasks_test.go` (package `store`):

```go
func TestTaskSecretsRoundTrip(t *testing.T) {
	s := OpenTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "secproj", "Secrets", "SE"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	var created *Task
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		task, err := CreateTask(tx, s.Now(), TaskInput{
			ProjectID: "secproj", Title: "needs creds", Priority: "medium", Kind: "chore",
			Secrets: []string{"KUBECONFIG_HZDEV", "OPENALEX_API_KEY"},
		})
		created = task
		return err
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := s.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	want := []string{"KUBECONFIG_HZDEV", "OPENALEX_API_KEY"}
	if !reflect.DeepEqual(got.Secrets, want) {
		t.Fatalf("Secrets = %v; want %v", got.Secrets, want)
	}

	// Update replaces the whole list; empty clears.
	next := []string{"GITHUB_TOKEN"}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateTaskFields(tx, s.Now(), created.ID, nil, nil, nil, nil, &next, nil)
	})
	if err != nil {
		t.Fatalf("update secrets: %v", err)
	}
	got, err = s.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !reflect.DeepEqual(got.Secrets, []string{"GITHUB_TOKEN"}) {
		t.Fatalf("Secrets after update = %v; want [GITHUB_TOKEN]", got.Secrets)
	}
}

func TestTaskSecretsRejectsBadName(t *testing.T) {
	s := OpenTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "secproj2", "Secrets2", "SF"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := CreateTask(tx, s.Now(), TaskInput{
			ProjectID: "secproj2", Title: "bad", Priority: "medium", Kind: "chore",
			Secrets: []string{"op://Employee/x"},
		})
		return err
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("create with bad secret name: %v; want ErrInvalidInput", err)
	}
}
```

Add `"reflect"` and `"errors"` to the file's imports if absent.

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./internal/store/ -run TestTaskSecrets`
Expected: FAIL — compile errors (`TaskInput` has no `Secrets`;
`UpdateTaskFields` takes 8 args). If the test DB predates the migration, the
test store applies `deploy/base/migrations` (see `OpenTestStore`), so 0009
must exist first — it does (Step 4).

- [ ] **Step 7: Implement the store changes**

In `internal/store/tasks.go`:

1. Import `"encoding/json"` and
   `"github.com/sunstoneinstitute/worklode/internal/secrets"`.

2. Add to `Task` (after `NeedsDecomposition`):

```go
	// Secrets is the task's declared org-catalog secret names (spec 017).
	// Names only; nil and empty are equivalent.
	Secrets []string
```

3. Add `Secrets []string` to `TaskInput` (after `Concern`).

4. Add a shared helper (near `ValidConcern`):

```go
// secretsJSON marshals a secret-name list for the tasks.secrets jsonb column,
// validating every name. nil marshals as [].
func secretsJSON(names []string) ([]byte, error) {
	for _, n := range names {
		if !secrets.ValidName(n) {
			return nil, fmt.Errorf("invalid secret name %q: %w", n, ErrInvalidInput)
		}
	}
	if names == nil {
		names = []string{}
	}
	return json.Marshal(names)
}
```

5. In `CreateTask`, before the INSERT:

```go
	secretsVal, err := secretsJSON(in.Secrets)
	if err != nil {
		return nil, err
	}
```

extend the INSERT column list with `secrets` (`$12`), pass `secretsVal`, and
set `Secrets: in.Secrets` on the returned `Task`.

6. Change `UpdateTaskFields` to
   `func UpdateTaskFields(tx *sql.Tx, now time.Time, id string, title, body, priority, concern *string, secretNames *[]string, needsDecomposition *bool) error`
   and, alongside the other `set(...)` calls:

```go
	if secretNames != nil {
		val, err := secretsJSON(*secretNames)
		if err != nil {
			return err
		}
		set("secrets", val)
	}
```

Update the all-nil doc comment and the one caller
(`internal/api/tasks.go:297` — pass `nil` for now; Task 2 wires it).

7. Extend `taskColumns` with `, secrets` and `scanTask` with:

```go
	var secretsRaw []byte
```

scanned in position (after `needs_decomposition`… keep the SELECT order:
`…, concern, needs_decomposition, secrets, created_by, …` — adjust
`taskColumns` and the `Scan` argument order to match), then after a
successful scan:

```go
	if len(secretsRaw) > 0 {
		if err := json.Unmarshal(secretsRaw, &t.Secrets); err != nil {
			return nil, fmt.Errorf("decode task secrets: %w", err)
		}
	}
	if len(t.Secrets) == 0 {
		t.Secrets = nil
	}
```

- [ ] **Step 8: Run the store suite**

Run: `go test ./internal/store/...`
Expected: PASS (a compile failure in `internal/api` is expected until the call
site is patched per Step 7.6 — fix it with a `nil` argument now).

- [ ] **Step 9: Commit**

```bash
git add internal/secrets internal/store internal/api/tasks.go deploy/base
git commit -m "Add the tasks.secrets column and secret-name validation"
```

---

## Task 2: Wire `secrets` through the task API

**Files:**
- Modify: `internal/api/tasks.go`
- Test: `internal/api/tasks_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/api/tasks_test.go` (package `api_test`; `newTestServer`
and `doReq` come from `internal/api/server_test.go`):

```go
func TestTaskSecretsOverAPI(t *testing.T) {
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": "secapi", "name": "Sec", "key": "SA"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "secapi", "title": "creds", "priority": "medium", "kind": "chore",
		"secrets": []string{"KUBECONFIG_HZDEV", "OPENALEX_API_KEY"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID      string   `json:"id"`
		Secrets []string `json:"secrets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(created.Secrets) != 2 || created.Secrets[0] != "KUBECONFIG_HZDEV" {
		t.Fatalf("secrets = %v; want the two declared names", created.Secrets)
	}

	// The brief shows the declaration (acceptance 1).
	rec = doReq(t, h, http.MethodGet, "/api/v1/tasks/"+created.ID+"/brief", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("brief: %d %s", rec.Code, rec.Body.String())
	}
	var brief struct {
		Task struct {
			Secrets []string `json:"secrets"`
		} `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &brief); err != nil {
		t.Fatalf("decode brief: %v", err)
	}
	if len(brief.Task.Secrets) != 2 {
		t.Fatalf("brief secrets = %v; want 2 names", brief.Task.Secrets)
	}

	// PATCH replaces the list.
	rec = doReq(t, h, http.MethodPatch, "/api/v1/tasks/"+created.ID, token,
		map[string]any{"secrets": []string{"GITHUB_TOKEN"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var patched struct {
		Secrets []string `json:"secrets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(patched.Secrets) != 1 || patched.Secrets[0] != "GITHUB_TOKEN" {
		t.Fatalf("patched secrets = %v; want [GITHUB_TOKEN]", patched.Secrets)
	}
}

func TestTaskSecretsRejectsBadNames(t *testing.T) {
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": "secbad", "name": "SecBad", "key": "SB"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "secbad", "title": "bad", "priority": "medium", "kind": "chore",
		"secrets": []string{"op://Employee/GitHub token/credential"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad secret name: %d %s; want 422", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run TestTaskSecrets`
Expected: FAIL — unknown field `secrets` (readJSON disallows unknown fields →
400, not 201).

- [ ] **Step 3: Implement**

In `internal/api/tasks.go`:

1. Import `"github.com/sunstoneinstitute/worklode/internal/secrets"`.

2. Add a validator next to `validPriorities`:

```go
// validSecretNames rejects the request early with a clean message; the store
// re-checks (defense in depth for non-HTTP callers).
func validSecretNames(names []string) bool {
	for _, n := range names {
		if !secrets.ValidName(n) {
			return false
		}
	}
	return true
}
```

3. `taskJSON`: add `Secrets []string \`json:"secrets"\`` after
   `NeedsDecomposition`; in `toTaskJSON` set

```go
		Secrets: append([]string{}, t.Secrets...),
```

   (always an array on the wire, never null).

4. `createTaskRequest`: add `Secrets []string \`json:"secrets"\``. In
   `createTask`, after the concern check:

```go
	if !validSecretNames(req.Secrets) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid secret name: must match ^[A-Z][A-Z0-9_]*$")
		return
	}
```

   and pass `Secrets: req.Secrets` in the `store.TaskInput`.

5. `patchTaskRequest`: add `Secrets *[]string \`json:"secrets"\``. Include it
   in the "no fields to update" check, validate it like create when non-nil,
   pass `req.Secrets` as the new `UpdateTaskFields` argument (replacing the
   Task-1 `nil`), and extend the change log loop with:

```go
	if req.Secrets != nil {
		if err := store.LogChange(tx, "task", id, eventID,
			map[string]any{"field": "secrets", "new": *req.Secrets}); err != nil {
			return err
		}
	}
```

- [ ] **Step 4: Run the API suite**

Run: `go test ./internal/api/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Wire the task secrets declaration through the API"
```

---

## Task 3: Catalog format and resolution

**Files:**
- Create: `internal/secrets/catalog.go`
- Test: `internal/secrets/catalog_test.go`

- [ ] **Step 1: Write the failing test**

```go
package secrets

import (
	"reflect"
	"strings"
	"testing"
)

// specExample is the catalog from docs/specs/017-task-secrets.md, verbatim.
const specExample = `
[GITHUB_TOKEN]
ref = "op://Employee/GitHub agent token/credential"
description = "GitHub credential the agent operates as (operator's own identity)"
baseline = true            # packed for every task; no consent prompt

[KUBECONFIG_HZDEV]
ref = "op://Infrastructure/hzdev kubeconfig/kubeconfig"
description = "Kubernetes access to the hzdev cluster, for troubleshooting tasks"
# baseline defaults to false: must be declared per task, listed at the consent prompt
`

func TestParseCatalog(t *testing.T) {
	c, err := ParseCatalog([]byte(specExample))
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	if len(c.Entries) != 2 {
		t.Fatalf("entries = %d; want 2", len(c.Entries))
	}
	gh, ok := c.Get("GITHUB_TOKEN")
	if !ok || gh.Ref != "op://Employee/GitHub agent token/credential" || !gh.Baseline {
		t.Fatalf("GITHUB_TOKEN = %+v, %v", gh, ok)
	}
	kc, ok := c.Get("KUBECONFIG_HZDEV")
	if !ok || kc.Baseline || !strings.Contains(kc.Description, "hzdev") {
		t.Fatalf("KUBECONFIG_HZDEV = %+v, %v", kc, ok)
	}
}

func TestParseCatalogErrors(t *testing.T) {
	cases := map[string]string{
		"bad name":       "[github_token]\nref = \"op://v/i/f\"\n",
		"missing ref":    "[GITHUB_TOKEN]\ndescription = \"x\"\n",
		"key outside":    "ref = \"op://v/i/f\"\n",
		"unknown key":    "[A]\nref = \"op://v/i/f\"\nvalue = \"nope\"\n",
		"unquoted ref":   "[A]\nref = op://v/i/f\n",
		"duplicate name": "[A]\nref = \"op://v/i/f\"\n[A]\nref = \"op://v/i/g\"\n",
		"bad baseline":   "[A]\nref = \"op://v/i/f\"\nbaseline = yes\n",
	}
	for name, src := range cases {
		if _, err := ParseCatalog([]byte(src)); err == nil {
			t.Errorf("%s: ParseCatalog succeeded; want error", name)
		}
	}
}

func TestResolve(t *testing.T) {
	c, err := ParseCatalog([]byte(specExample))
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	baseline, consented, missing := c.Resolve([]string{"KUBECONFIG_HZDEV", "NOT_IN_CATALOG"})
	if len(baseline) != 1 || baseline[0].Name != "GITHUB_TOKEN" {
		t.Fatalf("baseline = %+v; want [GITHUB_TOKEN]", baseline)
	}
	if len(consented) != 1 || consented[0].Name != "KUBECONFIG_HZDEV" {
		t.Fatalf("consented = %+v; want [KUBECONFIG_HZDEV]", consented)
	}
	if !reflect.DeepEqual(missing, []string{"NOT_IN_CATALOG"}) {
		t.Fatalf("missing = %v; want [NOT_IN_CATALOG]", missing)
	}

	// Declaring a baseline name does not duplicate it into the consent set.
	baseline, consented, missing = c.Resolve([]string{"GITHUB_TOKEN"})
	if len(baseline) != 1 || len(consented) != 0 || len(missing) != 0 {
		t.Fatalf("declared baseline: %v / %v / %v", baseline, consented, missing)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/secrets/ -run 'TestParseCatalog|TestResolve'`
Expected: FAIL — `undefined: ParseCatalog`.

- [ ] **Step 3: Implement `catalog.go`**

```go
package secrets

import (
	"fmt"
	"strings"
)

// Entry is one catalog secret: a symbolic name mapped to a 1Password
// reference plus policy. The ref addresses a value; it never is one.
type Entry struct {
	Name        string
	Ref         string // op://vault/item/field
	Description string
	Baseline    bool // packed for every task; exempt from the consent prompt
}

// Catalog is the parsed org-wide secrets catalog, in file order.
type Catalog struct {
	Entries []Entry
}

// Get returns the entry for name.
func (c *Catalog) Get(name string) (Entry, bool) {
	for _, e := range c.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Resolve maps a task's declared names to catalog entries: the baseline set
// (every baseline entry, declared or not), the consent set (declared,
// non-baseline), and declared names missing from the catalog. A missing name
// is a warning at the ceremony, never a failure (spec 017 degradation).
func (c *Catalog) Resolve(declared []string) (baseline, consented []Entry, missing []string) {
	for _, e := range c.Entries {
		if e.Baseline {
			baseline = append(baseline, e)
		}
	}
	for _, name := range declared {
		e, ok := c.Get(name)
		switch {
		case !ok:
			missing = append(missing, name)
		case !e.Baseline:
			consented = append(consented, e)
		}
	}
	return baseline, consented, missing
}

// ParseCatalog parses the catalog TOML subset: [NAME] tables with
// ref/description string keys and a baseline bool, full-line and trailing
// comments. A hand-rolled parser, matching the module's existing stance of
// carrying no TOML dependency (see internal/cli config parsing).
func ParseCatalog(data []byte) (*Catalog, error) {
	c := &Catalog{}
	cur := -1
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated table header", i+1)
			}
			name := strings.TrimSpace(line[1:end])
			if !ValidName(name) {
				return nil, fmt.Errorf("line %d: invalid secret name %q", i+1, name)
			}
			if _, ok := c.Get(name); ok {
				return nil, fmt.Errorf("line %d: duplicate entry %q", i+1, name)
			}
			c.Entries = append(c.Entries, Entry{Name: name})
			cur = len(c.Entries) - 1
			continue
		}
		if cur < 0 {
			return nil, fmt.Errorf("line %d: key outside a [NAME] table", i+1)
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", i+1)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "ref", "description":
			s, err := unquote(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %v", i+1, key, err)
			}
			if key == "ref" {
				c.Entries[cur].Ref = s
			} else {
				c.Entries[cur].Description = s
			}
		case "baseline":
			if j := strings.Index(val, "#"); j >= 0 {
				val = strings.TrimSpace(val[:j])
			}
			switch val {
			case "true":
				c.Entries[cur].Baseline = true
			case "false":
				c.Entries[cur].Baseline = false
			default:
				return nil, fmt.Errorf("line %d: baseline must be true or false", i+1)
			}
		default:
			return nil, fmt.Errorf("line %d: unknown key %q", i+1, key)
		}
	}
	for _, e := range c.Entries {
		if e.Ref == "" {
			return nil, fmt.Errorf("entry %s: ref is required", e.Name)
		}
	}
	return c, nil
}

// unquote extracts a "quoted" value, ignoring anything after the closing
// quote (trailing comments).
func unquote(val string) (string, error) {
	if len(val) < 2 || val[0] != '"' {
		return "", fmt.Errorf(`expected a "quoted" value`)
	}
	end := strings.Index(val[1:], `"`)
	if end < 0 {
		return "", fmt.Errorf("unterminated string")
	}
	return val[1 : 1+end], nil
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/secrets/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets
git commit -m "Parse the org secrets catalog"
```

---

## Task 4: Serve the catalog

**Files:**
- Create: `internal/api/secrets.go`, `internal/api/secrets_test.go`
- Modify: `internal/api/server.go` (Config + route), `internal/cmd/serve.go:82`

- [ ] **Step 1: Write the failing test**

`internal/api/secrets_test.go` (package `api_test`; mirrors `newTestServer` in
`internal/api/server_test.go:26-42` but injects a Config):

```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
)

// newSecretsTestServer is newTestServer with a secrets catalog on disk.
func newSecretsTestServer(t *testing.T, catalogTOML string) (http.Handler, string) {
	t.Helper()
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	path := filepath.Join(t.TempDir(), "catalog.toml")
	if err := os.WriteFile(path, []byte(catalogTOML), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	h, _, err := api.NewServer(st, api.Config{SecretsCatalogPath: path})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return h, token
}

const testCatalog = `[GITHUB_TOKEN]
ref = "op://Employee/GitHub agent token/credential"
description = "GitHub credential"
baseline = true

[KUBECONFIG_HZDEV]
ref = "op://Infrastructure/hzdev kubeconfig/kubeconfig"
description = "hzdev cluster access"
`

func TestSecretsCatalogRequiresAuth(t *testing.T) {
	h, _ := newSecretsTestServer(t, testCatalog)
	rec := doReq(t, h, http.MethodGet, "/api/v1/secrets/catalog", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d; want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "op://") {
		t.Fatal("unauthenticated response leaked an op:// ref")
	}
}

func TestSecretsCatalogServed(t *testing.T) {
	h, token := newSecretsTestServer(t, testCatalog)
	rec := doReq(t, h, http.MethodGet, "/api/v1/secrets/catalog", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Secrets []struct {
			Name     string `json:"name"`
			Ref      string `json:"ref"`
			Baseline bool   `json:"baseline"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Secrets) != 2 || resp.Secrets[0].Name != "GITHUB_TOKEN" ||
		!resp.Secrets[0].Baseline || resp.Secrets[1].Ref == "" {
		t.Fatalf("catalog = %+v; want both entries with refs", resp.Secrets)
	}
}

func TestSecretsCatalogUnconfigured(t *testing.T) {
	_, h, token := newTestServer(t) // no SecretsCatalogPath
	rec := doReq(t, h, http.MethodGet, "/api/v1/secrets/catalog", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unconfigured catalog: %d; want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run TestSecretsCatalog`
Expected: FAIL — `api.Config` has no `SecretsCatalogPath` (compile error).

- [ ] **Step 3: Implement**

In `internal/api/server.go`, add to `Config` (after `GitHubAppPrivateKey`):

```go
	// SecretsCatalogPath (LODE_SECRETS_CATALOG_PATH) points at the org
	// secrets catalog TOML (a mounted ConfigMap in the deployment). Empty
	// disables the catalog endpoint (404). The file maps names to op:// refs
	// — it holds no values, but vault/item names are mildly sensitive, so it
	// is only ever served authenticated.
	SecretsCatalogPath string
```

Create `internal/api/secrets.go`:

```go
package api

import (
	"net/http"
	"os"

	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

// catalogEntryJSON is the wire form of one catalog entry.
type catalogEntryJSON struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	Description string `json:"description"`
	Baseline    bool   `json:"baseline"`
}

// secretsCatalog handles GET /api/v1/secrets/catalog. Authenticated only —
// the name → op:// map must not leak vault/item structure (spec 017). The
// file is re-read per request so a ConfigMap update propagates without a
// restart; it is small and requests are rare (one per claim).
func (s *server) secretsCatalog(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SecretsCatalogPath == "" {
		writeErr(w, http.StatusNotFound, "secrets catalog not configured")
		return
	}
	data, err := os.ReadFile(s.cfg.SecretsCatalogPath)
	if err != nil {
		s.log.Error("read secrets catalog", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	cat, err := secrets.ParseCatalog(data)
	if err != nil {
		s.log.Error("parse secrets catalog", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := struct {
		Secrets []catalogEntryJSON `json:"secrets"`
	}{Secrets: make([]catalogEntryJSON, 0, len(cat.Entries))}
	for _, e := range cat.Entries {
		out.Secrets = append(out.Secrets, catalogEntryJSON{
			Name: e.Name, Ref: e.Ref, Description: e.Description, Baseline: e.Baseline,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
```

Register the route in `internal/api/server.go`, after the board route
(`server.go:304`):

```go
	mux.Handle("GET /api/v1/secrets/catalog", s.auth(s.secretsCatalog))
```

In `internal/cmd/serve.go`, add to the `api.Config` literal (after
`GitHubAppPrivateKey`, line 82):

```go
				SecretsCatalogPath:  os.Getenv("LODE_SECRETS_CATALOG_PATH"),
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/api/ -run TestSecretsCatalog -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/api internal/cmd/serve.go
git commit -m "Serve the secrets catalog to authenticated actors"
```

---

## Task 5: The `secrets_materialized` event

**Files:**
- Modify: `internal/api/secrets.go`, `internal/api/server.go` (route)
- Test: `internal/api/secrets_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/api/secrets_test.go`:

```go
func TestSecretsMaterializedEvent(t *testing.T) {
	st, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": "secevt", "name": "SecEvt", "key": "SV"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "secevt", "title": "t", "priority": "medium", "kind": "chore",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", rec.Code, rec.Body.String())
	}
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode: %v", err)
	}

	names := []string{"GITHUB_TOKEN", "KUBECONFIG_HZDEV", "OPENALEX_API_KEY"}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks/"+task.ID+"/secrets-materialized",
		token, map[string]any{"names": names})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("record: %d %s; want 204", rec.Code, rec.Body.String())
	}

	// The audit trail carries the names and nothing else (acceptance 3):
	// no op:// refs, no values, in the state log attributed to the event.
	logs, err := st.StateLogForEntity(context.Background(), "task", task.ID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	var found string
	for _, l := range logs {
		if strings.Contains(l.Change, "secrets_materialized") {
			found = l.Change
		}
	}
	if found == "" {
		t.Fatal("no secrets_materialized state-log entry")
	}
	for _, n := range names {
		if !strings.Contains(found, n) {
			t.Fatalf("state log %q missing name %s", found, n)
		}
	}
	if strings.Contains(found, "op://") {
		t.Fatalf("state log leaked an op:// ref: %q", found)
	}
}

func TestSecretsMaterializedRejectsNonNames(t *testing.T) {
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": "secrej", "name": "SecRej", "key": "SR"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "secrej", "title": "t", "priority": "medium", "kind": "chore",
	})
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, bad := range [][]string{
		{},
		{"op://Employee/x/y"},
		{"a-value-not-a-name"},
	} {
		rec = doReq(t, h, http.MethodPost, "/api/v1/tasks/"+task.ID+"/secrets-materialized",
			token, map[string]any{"names": bad})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("names %v: %d; want 422", bad, rec.Code)
		}
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks/NOPE-1/secrets-materialized",
		token, map[string]any{"names": []string{"GITHUB_TOKEN"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task: %d; want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run TestSecretsMaterialized`
Expected: FAIL — route unregistered (404 where 204/422 expected).

- [ ] **Step 3: Implement**

Append to `internal/api/secrets.go` (add imports `"database/sql"`,
`"encoding/json"`, `"github.com/sunstoneinstitute/worklode/internal/store"`):

```go
// secretsMaterialized handles POST /api/v1/tasks/{id}/secrets-materialized:
// the claim-ceremony hook reporting which secret names it put in the local
// keystore. The strict name grammar is the redaction guarantee — an op://
// ref or a raw value cannot pass validation, so neither can ever enter the
// event log.
func (s *server) secretsMaterialized(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Names []string `json:"names"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if len(req.Names) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "names is required")
		return
	}
	if !validSecretNames(req.Names) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid secret name: must match ^[A-Z][A-Z0-9_]*$")
		return
	}
	if _, err := s.st.GetTask(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	actor := actorFrom(r)
	payload, err := json.Marshal(map[string]any{
		"task": id, "actor": actor.ID, "names": req.Names,
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "secrets_materialized", payload,
		func(tx *sql.Tx, eventID int64) error {
			return store.LogChange(tx, "task", id, eventID,
				map[string]any{"field": "secrets_materialized", "names": req.Names})
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Register in `internal/api/server.go` next to the other task routes (after the
timeline route, `server.go:279`):

```go
	mux.Handle("POST /api/v1/tasks/{id}/secrets-materialized", s.auth(s.secretsMaterialized))
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/api/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Record names-only secrets_materialized events"
```

---

