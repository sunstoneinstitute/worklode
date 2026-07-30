# Task-declared secrets — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tasks declare org-catalog secret names; `lode next` runs a claim-time
ceremony (consent → one `op run` → OS keystore) so `lode secrets exec` can
inject exactly those values unattended, with names-only audit events and purge
on every release path.

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

**Migration number:** main has `0001`–`0005`; in-flight sibling plans claim
`0006` (task hierarchy), `0007` (skills), `0008` (graph projection,
`docs/plans/2026-07-30-knowledge-graph.md`). This plan takes **`0009_task_secrets`**.
golang-migrate applies versions in order and tolerates gaps, so 0009 lands
cleanly whether or not 0006–0008 have merged.

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
   — this repo has no plugin skill directory. Task 14 carries the full content.

---

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `deploy/base/migrations/0009_task_secrets.up.sql` / `.down.sql` | `tasks.secrets jsonb NOT NULL DEFAULT '[]'` |
| `internal/secrets/names.go` | `ValidName` — the `^[A-Z][A-Z0-9_]*$` gate everything shares |
| `internal/secrets/catalog.go` | `Entry`, `Catalog`, `ParseCatalog` (minimal TOML subset), `Resolve` |
| `internal/secrets/catalog_test.go` | parse the spec's example, comments, errors, resolve/baseline split |
| `internal/secrets/keystore.go` | `Put`/`Fetch`/`Del`/`PurgeTask` on service `worklode:<task-id>` |
| `internal/secrets/manifest.go` | names-only manifest load/save/remove under `~/.cache/worklode/secrets/` |
| `internal/secrets/envfile.go` | `WriteEnvFile` — `NAME=op://…` lines, 0600, refs only |
| `internal/secrets/secrets_test.go` | keystore (MockInit), manifest, envfile, purge |
| `internal/api/secrets.go` | `GET /api/v1/secrets/catalog`, `POST /api/v1/tasks/{id}/secrets-materialized` |
| `internal/api/secrets_test.go` | auth (401), unconfigured (404), names-only event, 422 on bad names |
| `internal/cmd/secrets.go` | `lode secrets catalog\|status\|exec\|purge\|pack` |
| `internal/cmd/secrets_test.go` | pack/exec/purge/status against MockInit + fake worktree |
| `internal/cmd/secretsceremony.go` | the claim-time ceremony; injectable `opRunFunc` |
| `internal/cmd/secretsceremony_test.go` | one op call, decline, missing-name warning, catalog-down, no-op-binary |
| `deploy/base/secrets-catalog.yaml` | `worklode-secrets-catalog` ConfigMap (empty placeholder catalog) |

**Modified files**

| Path | Change |
|---|---|
| `internal/store/tasks.go` | `Task.Secrets`, `TaskInput.Secrets`, `CreateTask`, `UpdateTaskFields`, `scanTask` |
| `internal/store/tasks_test.go` | secrets round-trip, bad-name rejection |
| `internal/api/tasks.go` | wire `secrets` on create/patch/taskJSON; validate names |
| `internal/api/tasks_test.go` | create/patch/brief carry secrets; 422 on bad name |
| `internal/api/server.go:34-73,279-304` | `Config.SecretsCatalogPath`; register the two routes |
| `internal/cmd/serve.go:66-83` | read `LODE_SECRETS_CATALOG_PATH` |
| `internal/cli/client.go` | `Task.Secrets`, input fields, `SecretsCatalog`, `RecordSecretsMaterialized` |
| `internal/cli/client_test.go` | catalog + record round-trips |
| `internal/cmd/task.go` | `--secrets` on `task add`/`task edit`; `printBrief` shows the names |
| `internal/cmd/lifecycle.go` | ceremony in `runNext`/`runResume`; purge in `done`/`block` |
| `internal/hookrun/hookrun.go` | purge in `handleWorktreeRemove`/`handleWorktreeExit`; brief line |
| `internal/hookrun/hookrun_test.go` | purge on remove |
| `deploy/base/kustomization.yaml` | 0009 migration files; `secrets-catalog.yaml` resource |
| `deploy/base/configmap.yaml` | `LODE_SECRETS_CATALOG_PATH` |
| `deploy/base/deployment.yaml` | mount the catalog ConfigMap |
| `README.md` | document declaration, ceremony, `lode secrets`, degradation |

**Out of repo (Task 14):** `sunstoneinstitute/claude-plugins` PR adding
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
- Create: `internal/secrets/names.go`, `deploy/base/migrations/0009_task_secrets.up.sql`, `deploy/base/migrations/0009_task_secrets.down.sql`
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

`deploy/base/migrations/0009_task_secrets.up.sql`:

```sql
-- Spec 017: tasks declare which org-catalog secrets they need, by symbolic
-- name. Names only — values and op:// refs never enter the backbone.
ALTER TABLE tasks ADD COLUMN secrets jsonb NOT NULL DEFAULT '[]'::jsonb;
```

`deploy/base/migrations/0009_task_secrets.down.sql`:

```sql
ALTER TABLE tasks DROP COLUMN secrets;
```

In `deploy/base/kustomization.yaml`, append to the `worklode-migrations`
generator's `files:` list:

```yaml
      - migrations/0009_task_secrets.up.sql
      - migrations/0009_task_secrets.down.sql
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

## Task 6: Client methods

**Files:**
- Modify: `internal/cli/client.go` (`Task` at :367, `CreateTaskInput` at :415, `EditTaskInput` at :703; new methods after `Timeline`)
- Test: `internal/cli/client_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/client_test.go` (package `cli_test`; match the
`httptest.NewServer` + `cli.NewClient(cli.Config{...})` style of
`TestResolveRemoteSendsRawURL` in the same file):

```go
func TestSecretsCatalogClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/secrets/catalog" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"secrets":[{"name":"GITHUB_TOKEN","ref":"op://Employee/GitHub agent token/credential","description":"gh","baseline":true}]}`)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"})
	resp, _, err := c.SecretsCatalog(context.Background())
	if err != nil {
		t.Fatalf("SecretsCatalog: %v", err)
	}
	if len(resp.Secrets) != 1 || resp.Secrets[0].Name != "GITHUB_TOKEN" ||
		!resp.Secrets[0].Baseline || resp.Secrets[0].Ref == "" {
		t.Fatalf("catalog = %+v", resp.Secrets)
	}
}

func TestRecordSecretsMaterialized(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"})
	err := c.RecordSecretsMaterialized(context.Background(), "WL-7",
		[]string{"GITHUB_TOKEN", "KUBECONFIG_HZDEV"})
	if err != nil {
		t.Fatalf("RecordSecretsMaterialized: %v", err)
	}
	if gotPath != "/api/v1/tasks/WL-7/secrets-materialized" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"KUBECONFIG_HZDEV"`) || strings.Contains(gotBody, "op://") {
		t.Fatalf("body = %q; want names only", gotBody)
	}
}
```

Ensure `"strings"` is imported in that test file.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/cli/ -run 'TestSecretsCatalogClient|TestRecordSecretsMaterialized'`
Expected: FAIL — `c.SecretsCatalog undefined`.

- [ ] **Step 3: Implement**

In `internal/cli/client.go`:

1. `Task` (line 367): add `Secrets []string \`json:"secrets"\`` after
   `NeedsDecomposition` (the `Brief.Task` and every task listing inherit it).
2. `CreateTaskInput` (line 415): add `Secrets []string \`json:"secrets,omitempty"\``.
3. `EditTaskInput` (line 703): add `Secrets *[]string`; in `EditTask`:

```go
	if in.Secrets != nil {
		body["secrets"] = *in.Secrets
	}
```

4. New methods (place after `Timeline`, end of file):

```go
// SecretsCatalogEntry is one entry of the org secrets catalog: a symbolic
// name, its op:// reference (an address, never a value), and policy.
type SecretsCatalogEntry struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	Description string `json:"description"`
	Baseline    bool   `json:"baseline"`
}

// SecretsCatalogResponse is the response body of SecretsCatalog.
type SecretsCatalogResponse struct {
	Secrets []SecretsCatalogEntry `json:"secrets"`
}

// SecretsCatalog calls GET /api/v1/secrets/catalog (authenticated; 404 when
// the server has no catalog configured).
func (c *Client) SecretsCatalog(ctx context.Context) (SecretsCatalogResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/secrets/catalog", nil)
	if err != nil {
		return SecretsCatalogResponse{}, nil, err
	}
	var resp SecretsCatalogResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return SecretsCatalogResponse{}, nil, fmt.Errorf("decode secrets catalog: %w", err)
	}
	return resp, raw, nil
}

// RecordSecretsMaterialized calls POST /api/v1/tasks/{id}/secrets-materialized
// with the materialized name list — the names-only audit event of spec 017.
func (c *Client) RecordSecretsMaterialized(ctx context.Context, id string, names []string) error {
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/secrets-materialized",
		map[string]any{"names": names})
	return err
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cli/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "Add secrets catalog and materialization-event client calls"
```

---

## Task 7: `--secrets` flags and brief display

**Files:**
- Modify: `internal/cmd/task.go` (`newTaskAddCmd` :47, `newTaskEditCmd` :182, `printBrief` :653), `internal/hookrun/hookrun.go` (`compactBrief` :638)
- Test: `internal/cmd/task_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/task_test.go` (package `cmd`; if existing tests in
that file use a different server-stub helper, reuse it — the point is
asserting the outgoing request body):

```go
func TestTaskAddSendsSecrets(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":"SE-1","project":"secproj","title":"t","priority":"medium","kind":"chore","state":"ready","secrets":["KUBECONFIG_HZDEV","OPENALEX_API_KEY"]}`)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	cmd := newTaskAddCmd()
	cmd.SetArgs([]string{"--project", "secproj", "--title", "t", "--kind", "chore",
		"--secrets", "KUBECONFIG_HZDEV,OPENALEX_API_KEY"})
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("task add: %v", err)
	}
	if !strings.Contains(gotBody, `"secrets":["KUBECONFIG_HZDEV","OPENALEX_API_KEY"]`) {
		t.Fatalf("request body = %q; want the secrets list", gotBody)
	}
}

func TestPrintBriefShowsSecrets(t *testing.T) {
	cmd := newTaskBriefCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	printBrief(cmd, cli.Brief{
		Task:   cli.Task{ID: "SE-1", Title: "t", State: "ready", Priority: "medium", Secrets: []string{"A_TOKEN", "B_KEY"}},
		Branch: "lode/SE-1-t",
	})
	if !strings.Contains(out.String(), "secrets: A_TOKEN, B_KEY") {
		t.Fatalf("brief output missing secrets line:\n%s", out.String())
	}
}
```

Ensure `"bytes"`, `"io"`, `"net/http"`, `"net/http/httptest"`, `"strings"`,
and `"github.com/sunstoneinstitute/worklode/internal/cli"` are imported.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/cmd/ -run 'TestTaskAddSendsSecrets|TestPrintBriefShowsSecrets'`
Expected: FAIL — unknown flag `--secrets`; no secrets line.

- [ ] **Step 3: Implement**

In `internal/cmd/task.go`:

1. `newTaskAddCmd`: add `var secretNames []string`, pass
   `Secrets: secretNames` in `cli.CreateTaskInput`, and register:

```go
	cmd.Flags().StringSliceVar(&secretNames, "secrets", nil,
		"org-catalog secret names this task needs, comma-separated (see `lode secrets catalog`)")
```

2. `newTaskEditCmd`: add `var secretNames []string` and, in the
   `Changed(...)` chain:

```go
	if cmd.Flags().Changed("secrets") {
		names := secretNames
		if len(names) == 1 && names[0] == "none" {
			names = []string{}
		}
		in.Secrets = &names
	}
```

register the flag (help: `"replace the task's declared secret names (comma-separated; 'none' clears)"`),
and include `in.Secrets == nil` in the "nothing to edit" condition and its
message.

3. `printBrief` (after the `branch:` line):

```go
	if len(b.Task.Secrets) > 0 {
		fmt.Fprintf(out, "secrets: %s\n", strings.Join(b.Task.Secrets, ", "))
	}
```

(add `"strings"` to imports).

In `internal/hookrun/hookrun.go`, `compactBrief` (after the branch line):

```go
	if len(b.Task.Secrets) > 0 {
		fmt.Fprintf(&sb, "\nsecrets: %s (use `lode secrets exec`)", strings.Join(b.Task.Secrets, ", "))
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cmd/... ./internal/hookrun/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd internal/hookrun
git commit -m "Declare task secrets from the CLI and show them in briefs"
```

---

## Task 8: Keystore, manifest, and env-file template

**Files:**
- Create: `internal/secrets/keystore.go`, `internal/secrets/manifest.go`, `internal/secrets/envfile.go`
- Test: `internal/secrets/secrets_test.go`

- [ ] **Step 1: Write the failing test**

```go
package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestKeystoreRoundTrip(t *testing.T) {
	keyring.MockInit() // in-memory backend; no real keychain touched

	if err := Put("WL-7", "GITHUB_TOKEN", "gh_value"); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := Fetch("WL-7", "GITHUB_TOKEN")
	if err != nil || got != "gh_value" {
		t.Fatalf("fetch = %q, %v; want gh_value", got, err)
	}
	// Scoped per task: another task sees nothing (least privilege).
	if _, err := Fetch("WL-8", "GITHUB_TOKEN"); err == nil {
		t.Fatal("cross-task fetch succeeded; want miss")
	}
	if err := Del("WL-7", "GITHUB_TOKEN"); err != nil {
		t.Fatalf("del: %v", err)
	}
	if _, err := Fetch("WL-7", "GITHUB_TOKEN"); err == nil {
		t.Fatal("fetch after delete succeeded")
	}
	// Deleting a missing item is a no-op, so purge is idempotent.
	if err := Del("WL-7", "GITHUB_TOKEN"); err != nil {
		t.Fatalf("second del: %v", err)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, ok := LoadManifest("WL-7"); ok {
		t.Fatal("manifest exists before save")
	}
	m := Manifest{Task: "WL-7", Materialized: []string{"A_TOKEN"}, Declined: []string{"B_KEY"}, At: time.Now().UTC()}
	if err := SaveManifest(m); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := LoadManifest("WL-7")
	if !ok || got.Materialized[0] != "A_TOKEN" || got.Declined[0] != "B_KEY" {
		t.Fatalf("load = %+v, %v", got, ok)
	}
	if err := RemoveManifest("WL-7"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := LoadManifest("WL-7"); ok {
		t.Fatal("manifest survived remove")
	}
}

func TestPurgeTask(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())

	for _, n := range []string{"A_TOKEN", "B_KEY"} {
		if err := Put("WL-7", n, "v-"+n); err != nil {
			t.Fatalf("put %s: %v", n, err)
		}
	}
	if err := SaveManifest(Manifest{Task: "WL-7", Materialized: []string{"A_TOKEN", "B_KEY"}, At: time.Now()}); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	names, err := PurgeTask("WL-7")
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("purged %v; want both names", names)
	}
	for _, n := range []string{"A_TOKEN", "B_KEY"} {
		if _, err := Fetch("WL-7", n); err == nil {
			t.Fatalf("%s survived purge", n)
		}
	}
	if _, ok := LoadManifest("WL-7"); ok {
		t.Fatal("manifest survived purge")
	}
	// No manifest ⇒ nothing to purge, not an error (idempotent release hooks).
	if names, err := PurgeTask("WL-7"); err != nil || len(names) != 0 {
		t.Fatalf("second purge = %v, %v; want empty, nil", names, err)
	}
}

func TestWriteEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".worklode", "secrets.env")
	entries := []Entry{
		{Name: "KUBECONFIG_HZDEV", Ref: "op://Infrastructure/hzdev kubeconfig/kubeconfig"},
		{Name: "GITHUB_TOKEN", Ref: "op://Employee/GitHub agent token/credential"},
	}
	if err := WriteEnvFile(path, entries); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o; want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "GITHUB_TOKEN=op://Employee/GitHub agent token/credential\n" +
		"KUBECONFIG_HZDEV=op://Infrastructure/hzdev kubeconfig/kubeconfig\n"
	if string(data) != want {
		t.Fatalf("env file:\n%s\nwant:\n%s", data, want)
	}
	if strings.Contains(string(data), "gh_value") {
		t.Fatal("env file must hold references only")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/secrets/...`
Expected: FAIL — `undefined: Put` (and the rest).

- [ ] **Step 3: Implement**

`internal/secrets/keystore.go`:

```go
package secrets

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// Service returns the OS-keystore service name for a task. One service per
// task, one item per secret name: reads are scoped to exactly the task's
// materialized set, and purging a task cannot touch another task's items.
func Service(taskID string) string { return "worklode:" + taskID }

// ErrNotStored reports a secret name with no keystore item.
var ErrNotStored = errors.New("secret not in keystore")

// Put stores one secret value for a task. The value comes from the op-run
// child environment (see `lode secrets pack`) and goes nowhere else.
func Put(taskID, name, value string) error {
	if err := keyring.Set(Service(taskID), name, value); err != nil {
		return fmt.Errorf("keystore set %s for %s: %w", name, taskID, err)
	}
	return nil
}

// Fetch reads one secret value for a task. A missing item is ErrNotStored.
func Fetch(taskID, name string) (string, error) {
	v, err := keyring.Get(Service(taskID), name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("%s for %s: %w", name, taskID, ErrNotStored)
	}
	if err != nil {
		return "", fmt.Errorf("keystore get %s for %s: %w", name, taskID, err)
	}
	return v, nil
}

// Del removes one secret item; a missing item is a no-op so purge paths are
// idempotent.
func Del(taskID, name string) error {
	err := keyring.Delete(Service(taskID), name)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("keystore delete %s for %s: %w", name, taskID, err)
	}
	return nil
}

// PurgeTask removes every keystore item recorded in the task's manifest and
// the manifest itself, returning the removed names. keyring has no
// enumeration API, so the manifest is the authority on what to remove; no
// manifest means nothing to purge.
func PurgeTask(taskID string) ([]string, error) {
	m, ok := LoadManifest(taskID)
	if !ok {
		return nil, nil
	}
	for _, n := range m.Materialized {
		if err := Del(taskID, n); err != nil {
			return nil, err
		}
	}
	if err := RemoveManifest(taskID); err != nil {
		return nil, err
	}
	return m.Materialized, nil
}
```

`internal/secrets/manifest.go`:

```go
package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Manifest records WHICH secret names a task has materialized or declined —
// names only, never values or op:// refs. It lives outside the worktree
// (~/.cache/worklode/secrets/<task-id>.json) because purge must still work
// after the worktree is deleted, and keyring cannot enumerate its own items.
type Manifest struct {
	Task         string    `json:"task"`
	Materialized []string  `json:"materialized,omitempty"`
	Declined     []string  `json:"declined,omitempty"`
	At           time.Time `json:"at"`
}

// manifestPath returns ~/.cache/worklode/secrets/<taskID>.json.
func manifestPath(taskID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "worklode", "secrets", taskID+".json"), nil
}

// LoadManifest reads a task's manifest; a missing or unreadable file is
// ok=false, never an error.
func LoadManifest(taskID string) (Manifest, bool) {
	path, err := manifestPath(taskID)
	if err != nil {
		return Manifest{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, false
	}
	var m Manifest
	if json.Unmarshal(data, &m) != nil {
		return Manifest{}, false
	}
	return m, true
}

// SaveManifest writes a task's manifest with 0600 permissions.
func SaveManifest(m Manifest) error {
	path, err := manifestPath(m.Task)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// RemoveManifest deletes a task's manifest; missing is fine.
func RemoveManifest(taskID string) error {
	path, err := manifestPath(taskID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
```

`internal/secrets/envfile.go`:

```go
package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WriteEnvFile renders entries in `op run` env-file format —
// NAME=op://vault/item/field, one per line, sorted by name. References only,
// never values: the file is the portable packing manifest (spec 017 v1.5
// re-uses it verbatim for remote executors). 0600 because vault/item names
// are mildly sensitive.
func WriteEnvFile(path string, entries []Entry) error {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	for _, e := range sorted {
		fmt.Fprintf(&b, "%s=%s\n", e.Name, e.Ref)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/secrets/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets
git commit -m "Add the task-scoped keystore, manifest, and op env-file template"
```

---

## Task 9: `lode secrets` — catalog, pack, purge, status

**Files:**
- Create: `internal/cmd/secrets.go`
- Test: `internal/cmd/secrets_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

// initSecretsWorktree creates <tmp>/wt/<taskID>-fix as a real git repo (its
// root parses as a Worklode worktree) and chdirs into it.
func initSecretsWorktree(t *testing.T, taskID string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wt", taskID+"-fix")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(dir)
	return dir
}

func TestSecretsPackWritesKeystoreNotDisk(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("A_TOKEN", "value-a")
	t.Setenv("B_KEY", "value-b")

	cmd := newSecretsPackCmd()
	cmd.SetArgs([]string{"--task", "WL-7", "--names", "A_TOKEN,B_KEY", "--declined", "C_SECRET"})
	cmd.SetOut(new(bytes.Buffer))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pack: %v", err)
	}

	for name, want := range map[string]string{"A_TOKEN": "value-a", "B_KEY": "value-b"} {
		if got, err := secrets.Fetch("WL-7", name); err != nil || got != want {
			t.Fatalf("keystore %s = %q, %v; want %q", name, got, err, want)
		}
	}
	m, ok := secrets.LoadManifest("WL-7")
	if !ok || len(m.Materialized) != 2 || len(m.Declined) != 1 {
		t.Fatalf("manifest = %+v, %v", m, ok)
	}
	// The redaction check: no file under $HOME contains a value.
	filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "value-a") || strings.Contains(string(data), "value-b") {
			t.Errorf("secret value written to %s", path)
		}
		return nil
	})
}

func TestSecretsPackFailsOnUnresolvedName(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	os.Unsetenv("NOT_RESOLVED")

	cmd := newSecretsPackCmd()
	cmd.SetArgs([]string{"--task", "WL-7", "--names", "NOT_RESOLVED"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "NOT_RESOLVED") {
		t.Fatalf("pack with unresolved name: %v; want error naming it", err)
	}
}

func TestSecretsPurgeCommand(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	initSecretsWorktree(t, "WL-9")

	if err := secrets.Put("WL-9", "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-9", Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	cmd := newSecretsPurgeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := secrets.Fetch("WL-9", "A_TOKEN"); err == nil {
		t.Fatal("secret survived purge")
	}
	if !strings.Contains(out.String(), "A_TOKEN") {
		t.Fatalf("purge output = %q; want the purged name", out.String())
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/cmd/ -run TestSecrets`
Expected: FAIL — `undefined: newSecretsPackCmd`.

- [ ] **Step 3: Implement `internal/cmd/secrets.go`**

```go
package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

// This file implements `lode secrets`: the runtime surface of spec 017.
// Values pass through exactly two places here — the pack command's inherited
// environment (as the child of `op run`) and the exec command's child
// environment. Neither is ever written, logged, or echoed.

func init() {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Task-declared secrets: catalog, status, exec, purge (spec 017)",
	}
	cmd.AddCommand(newSecretsCatalogCmd(), newSecretsStatusCmd(), newSecretsExecCmd(),
		newSecretsPurgeCmd(), newSecretsPackCmd())
	rootCmd.AddCommand(cmd)
}

func newSecretsCatalogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "catalog",
		Short: "List the org secrets catalog: names, baseline flag, descriptions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.SecretsCatalog(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			for _, e := range resp.Secrets {
				marker := " "
				if e.Baseline {
					marker = "*"
				}
				fmt.Fprintf(out, "%s %-28s %s\n", marker, e.Name, e.Description)
			}
			fmt.Fprintln(out, "\n* = baseline: packed for every task, no per-task declaration needed")
			return nil
		},
	}
}

func newSecretsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show declared vs materialized secret names for the bound task (names only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			taskID, _, err := resolveWorktreeTask(".")
			if err != nil {
				return err
			}
			brief, _, err := c.Brief(cmd.Context(), taskID)
			if err != nil {
				return err
			}
			m, _ := secrets.LoadManifest(taskID)

			inKeystore := func(name string) bool {
				_, err := secrets.Fetch(taskID, name)
				return err == nil
			}
			state := func(name string) string {
				switch {
				case contains(m.Declined, name):
					return "declined"
				case contains(m.Materialized, name) && inKeystore(name):
					return "materialized"
				case contains(m.Materialized, name):
					return "missing from keystore"
				default:
					return "unmaterialized"
				}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "task: %s\n", taskID)
			for _, name := range brief.Task.Secrets {
				fmt.Fprintf(out, "  %-28s %s (declared)\n", name, state(name))
			}
			for _, name := range m.Materialized {
				if !contains(brief.Task.Secrets, name) {
					fmt.Fprintf(out, "  %-28s %s (baseline)\n", name, state(name))
				}
			}
			if len(brief.Task.Secrets) == 0 && len(m.Materialized) == 0 && len(m.Declined) == 0 {
				fmt.Fprintln(out, "  no secrets declared or materialized")
			}
			return nil
		},
	}
}

func newSecretsPurgeCmd() *cobra.Command {
	var taskID string
	cmd := &cobra.Command{
		Use:   "purge [--task <id>]",
		Short: "Remove the task's keystore items (invoked by release hooks)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := taskID
			if id == "" {
				var err error
				id, _, err = resolveWorktreeTask(".")
				if err != nil {
					return err
				}
			}
			names, err := secrets.PurgeTask(id)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no secrets stored for %s\n", id)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "purged %s for %s\n", strings.Join(names, ", "), id)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "task id (default: the current worktree's task)")
	return cmd
}

// newSecretsPackCmd is the internal child of the ceremony's single `op run`:
// op resolves every NAME=op://ref line into this process's environment under
// one 1Password authorization; pack moves each value into the OS keystore and
// exits. Values never touch disk or the shell.
func newSecretsPackCmd() *cobra.Command {
	var taskID, namesCSV, declinedCSV string
	cmd := &cobra.Command{
		Use:    "pack",
		Hidden: true,
		Short:  "Internal: write op-run-resolved env values into the OS keystore",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			names := splitNames(namesCSV)
			if taskID == "" || len(names) == 0 {
				return errors.New("--task and --names are required")
			}
			var missing []string
			for _, n := range names {
				if os.Getenv(n) == "" {
					missing = append(missing, n)
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("not resolved in environment (op run did not supply): %s",
					strings.Join(missing, ", "))
			}
			for _, n := range names {
				if err := secrets.Put(taskID, n, os.Getenv(n)); err != nil {
					return err
				}
			}
			if err := secrets.SaveManifest(secrets.Manifest{
				Task: taskID, Materialized: names, Declined: splitNames(declinedCSV),
				At: time.Now().UTC(),
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "packed %d secrets for %s\n", len(names), taskID)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "task id")
	cmd.Flags().StringVar(&namesCSV, "names", "", "comma-separated names to pack")
	cmd.Flags().StringVar(&declinedCSV, "declined", "", "comma-separated names the operator declined")
	return cmd
}

// splitNames splits a comma-separated name list, dropping empties.
func splitNames(csv string) []string {
	var out []string
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
```

(`newSecretsExecCmd` arrives in Task 10; to keep this task compiling, add a
one-line placeholder there now:
`func newSecretsExecCmd() *cobra.Command { return &cobra.Command{Use: "exec", Hidden: true} }`
— Task 10 replaces it, test-first.)

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cmd/ -run TestSecrets -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/secrets.go internal/cmd/secrets_test.go
git commit -m "Add lode secrets catalog, status, pack, and purge"
```

---

## Task 10: `lode secrets exec`

**Files:**
- Modify: `internal/cmd/secrets.go` (replace the placeholder)
- Test: `internal/cmd/secrets_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/secrets_test.go`:

```go
func TestSecretsExecInjectsExactlyMaterializedNames(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	initSecretsWorktree(t, "WL-9")

	for _, n := range []string{"A_TOKEN", "B_KEY"} {
		if err := secrets.Put("WL-9", n, "val-"+n); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-9", Materialized: []string{"A_TOKEN", "B_KEY"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	var gotArgv, gotEnv []string
	restore := execFn
	execFn = func(bin string, argv, env []string) error {
		gotArgv, gotEnv = argv, env
		return nil
	}
	defer func() { execFn = restore }()

	cmd := newSecretsExecCmd()
	cmd.SetArgs([]string{"--", "env"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(gotArgv) == 0 || gotArgv[0] != "env" {
		t.Fatalf("argv = %v", gotArgv)
	}
	injected := map[string]string{}
	for _, kv := range gotEnv {
		k, v, _ := strings.Cut(kv, "=")
		injected[k] = v
	}
	if injected["A_TOKEN"] != "val-A_TOKEN" || injected["B_KEY"] != "val-B_KEY" {
		t.Fatalf("injected env missing secrets: %v", injected)
	}
}

func TestSecretsExecFailsOnMissingKeystoreItem(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	initSecretsWorktree(t, "WL-9")
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-9", Materialized: []string{"GONE_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	cmd := newSecretsExecCmd()
	cmd.SetArgs([]string{"--", "env"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "GONE_TOKEN") {
		t.Fatalf("exec with missing item: %v; want error naming GONE_TOKEN", err)
	}
}

func TestSecretsExecRequiresWorktree(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	// A git repo whose root is NOT wt/<id>-<slug>: the guard must reject it.
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(dir)

	cmd := newSecretsExecCmd()
	cmd.SetArgs([]string{"--", "env"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("exec outside a Worklode worktree succeeded; want the guard to fail")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/cmd/ -run TestSecretsExec`
Expected: FAIL — the placeholder has no behavior (`undefined: execFn`).

- [ ] **Step 3: Implement**

Replace the Task-9 placeholder in `internal/cmd/secrets.go` (add imports
`"os/exec"`, `"syscall"`):

```go
// execFn wraps syscall.Exec so tests can capture the argv/env instead of
// replacing the test process.
var execFn = syscall.Exec

func newSecretsExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec [--] <command> [args...]",
		Short: "Run a command with the bound task's materialized secrets in its environment",
		Long: "Resolves the task from the wt/<id>-<slug> worktree guard, reads that task's " +
			"items from the OS keystore, injects them as environment variables, and execs. " +
			"Values exist only in the child process. The injected set is exactly the task's " +
			"materialized names — not the catalog, not the operator's secrets.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _, err := resolveWorktreeTask(".")
			if err != nil {
				return err
			}
			m, ok := secrets.LoadManifest(taskID)
			if !ok || len(m.Materialized) == 0 {
				return fmt.Errorf("no secrets materialized for %s; `lode resume` runs the ceremony", taskID)
			}
			env := os.Environ()
			for _, name := range m.Materialized {
				v, err := secrets.Fetch(taskID, name)
				if err != nil {
					return fmt.Errorf("secret %s is not in the keystore — do not retry or "+
						"work around; `lode block` with reason missing-secret: %s", name, name)
				}
				env = append(env, name+"="+v)
			}
			bin, err := exec.LookPath(args[0])
			if err != nil {
				return err
			}
			return execFn(bin, args, env)
		},
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/cmd/ -run TestSecrets -v`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/cmd
git commit -m "Add lode secrets exec with the worktree guard"
```

---

## Task 11: The claim-time ceremony in `lode next` and `lode resume`

**Files:**
- Create: `internal/cmd/secretsceremony.go`
- Modify: `internal/cmd/lifecycle.go` (`runNext` :206-210, `runResume` :291-300)
- Test: `internal/cmd/secretsceremony_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

const ceremonyCatalogJSON = `{"secrets":[
  {"name":"GITHUB_TOKEN","ref":"op://Employee/GitHub agent token/credential","description":"gh","baseline":true},
  {"name":"KUBECONFIG_HZDEV","ref":"op://Infrastructure/hzdev kubeconfig/kubeconfig","description":"hzdev",	"baseline":false},
  {"name":"OPENALEX_API_KEY","ref":"op://Infrastructure/openalex/key","description":"openalex","baseline":false}
]}`

// ceremonyFixture returns a client against a stub server (catalog +
// materialized-event recording), a cobra command with buffered IO, and the
// recorded-names channel.
func ceremonyFixture(t *testing.T, catalogStatus int, stdin string) (*cli.Client, *cobra.Command, *bytes.Buffer, *[]string) {
	t.Helper()
	var recorded []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/secrets/catalog":
			w.WriteHeader(catalogStatus)
			if catalogStatus == http.StatusOK {
				io.WriteString(w, ceremonyCatalogJSON)
			} else {
				io.WriteString(w, `{"error":"boom"}`)
			}
		case strings.HasSuffix(r.URL.Path, "/secrets-materialized"):
			var req struct {
				Names []string `json:"names"`
			}
			body, _ := io.ReadAll(r.Body)
			_ = jsonUnmarshal(body, &req)
			recorded = req.Names
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	cmd := &cobra.Command{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(errBuf)
	cmd.SetIn(strings.NewReader(stdin))
	return cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"}), cmd, errBuf, &recorded
}

// fakeOp simulates op run + lode secrets pack: it stores a dummy value per
// name and writes the manifest, exactly as the real pack child would.
func fakeOp(t *testing.T, calls *int, capturedEnvFile *string) func(dir, envFile, taskID string, names, declined []string, stdout, stderr io.Writer) error {
	return func(dir, envFile, taskID string, names, declined []string, stdout, stderr io.Writer) error {
		*calls++
		data, err := os.ReadFile(envFile)
		if err != nil {
			t.Errorf("read env file: %v", err)
		}
		*capturedEnvFile = string(data)
		for _, n := range names {
			if err := secrets.Put(taskID, n, "resolved-"+n); err != nil {
				return err
			}
		}
		return secrets.SaveManifest(secrets.Manifest{Task: taskID, Materialized: names, Declined: declined})
	}
}

func TestCeremonyOneOpRunMaterializesAll(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	c, cmd, errBuf, recorded := ceremonyFixture(t, http.StatusOK, "y\n")

	calls, envFile := 0, ""
	restore := opRunFunc
	opRunFunc = fakeOp(t, &calls, &envFile)
	defer func() { opRunFunc = restore }()

	runSecretsCeremony(context.Background(), cmd, c, "WL-7", dir,
		[]string{"KUBECONFIG_HZDEV", "OPENALEX_API_KEY", "NOT_IN_CATALOG"})

	if calls != 1 {
		t.Fatalf("op run called %d times; want exactly 1 (one authorization)", calls)
	}
	// One baseline + two consented, resolved to refs — references only.
	for _, want := range []string{
		"GITHUB_TOKEN=op://Employee/GitHub agent token/credential",
		"KUBECONFIG_HZDEV=op://Infrastructure/hzdev kubeconfig/kubeconfig",
		"OPENALEX_API_KEY=op://Infrastructure/openalex/key",
	} {
		if !strings.Contains(envFile, want) {
			t.Errorf("env file missing %q:\n%s", want, envFile)
		}
	}
	if strings.Contains(envFile, "resolved-") {
		t.Fatal("env file contains a value")
	}
	if len(*recorded) != 3 {
		t.Fatalf("recorded names = %v; want the 3 materialized names", *recorded)
	}
	if !strings.Contains(errBuf.String(), "NOT_IN_CATALOG") {
		t.Fatalf("no warning for the unknown name:\n%s", errBuf.String())
	}
	if _, err := secrets.Fetch("WL-7", "GITHUB_TOKEN"); err != nil {
		t.Fatalf("baseline secret not in keystore: %v", err)
	}
}

func TestCeremonyDeclineSkipsConsentedSet(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	c, cmd, _, recorded := ceremonyFixture(t, http.StatusOK, "n\n")

	calls, envFile := 0, ""
	restore := opRunFunc
	opRunFunc = fakeOp(t, &calls, &envFile)
	defer func() { opRunFunc = restore }()

	runSecretsCeremony(context.Background(), cmd, c, "WL-7", dir, []string{"KUBECONFIG_HZDEV"})

	// Baseline still packs (exempt from consent); the declined name does not.
	if calls != 1 {
		t.Fatalf("op run called %d times; want 1 (baseline only)", calls)
	}
	if strings.Contains(envFile, "KUBECONFIG_HZDEV") {
		t.Fatal("declined name reached the env file")
	}
	m, ok := secrets.LoadManifest("WL-7")
	if !ok || !contains(m.Declined, "KUBECONFIG_HZDEV") {
		t.Fatalf("manifest = %+v; want KUBECONFIG_HZDEV declined", m)
	}
	if contains(*recorded, "KUBECONFIG_HZDEV") {
		t.Fatal("declined name was recorded as materialized")
	}
}

func TestCeremonyCatalogUnavailableDegrades(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	c, cmd, errBuf, _ := ceremonyFixture(t, http.StatusInternalServerError, "")

	calls, envFile := 0, ""
	restore := opRunFunc
	opRunFunc = fakeOp(t, &calls, &envFile)
	defer func() { opRunFunc = restore }()

	runSecretsCeremony(context.Background(), cmd, c, "WL-7", t.TempDir(), []string{"KUBECONFIG_HZDEV"})
	if calls != 0 {
		t.Fatal("op run called though the catalog was unavailable")
	}
	if !strings.Contains(errBuf.String(), "catalog unavailable") {
		t.Fatalf("no degradation warning:\n%s", errBuf.String())
	}

	// No declared names + no catalog ⇒ silent (no noise on servers without
	// the feature).
	errBuf.Reset()
	runSecretsCeremony(context.Background(), cmd, c, "WL-7", t.TempDir(), nil)
	if errBuf.Len() != 0 {
		t.Fatalf("expected silence, got:\n%s", errBuf.String())
	}
}

// jsonUnmarshal keeps the fixture readable without importing encoding/json at
// every call site.
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
```

Add `"encoding/json"` to the imports.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/cmd/ -run TestCeremony`
Expected: FAIL — `undefined: runSecretsCeremony`.

- [ ] **Step 3: Implement `internal/cmd/secretsceremony.go`**

```go
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

// opRunFunc executes the materialization step: ONE `op run` resolving every
// reference in envFile under a single 1Password authorization, with
// `lode secrets pack` as the child. Swapped in tests.
var opRunFunc = runOpPack

func runOpPack(dir, envFile, taskID string, names, declined []string, stdout, stderr io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate lode binary: %w", err)
	}
	args := []string{"run", "--env-file", envFile, "--",
		self, "secrets", "pack", "--task", taskID, "--names", strings.Join(names, ",")}
	if len(declined) > 0 {
		args = append(args, "--declined", strings.Join(declined, ","))
	}
	cmd := exec.Command("op", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// runSecretsCeremony is the spec-017 claim-time ceremony: fetch the catalog,
// resolve declared ∪ baseline names, take one consent for the non-baseline
// set, materialize under one op authorization, and record a names-only
// event. It NEVER fails the claim — every failure degrades to a stderr
// warning, and a needed-but-unavailable secret later becomes a block signal,
// not a prompt. All output goes to stderr so --json stdout stays clean.
func runSecretsCeremony(ctx context.Context, cmd *cobra.Command, c *cli.Client, taskID, dir string, declared []string) {
	errw := cmd.ErrOrStderr()

	resp, _, err := c.SecretsCatalog(ctx)
	if err != nil {
		// A server without the catalog feature would otherwise warn on every
		// claim; only tasks that declared names need to hear about it.
		if len(declared) > 0 {
			fmt.Fprintf(errw, "secrets: catalog unavailable (%v) — credentialed steps will block\n", err)
		}
		return
	}
	catalog := &secrets.Catalog{}
	for _, e := range resp.Secrets {
		catalog.Entries = append(catalog.Entries, secrets.Entry{
			Name: e.Name, Ref: e.Ref, Description: e.Description, Baseline: e.Baseline,
		})
	}

	baseline, consentSet, missing := catalog.Resolve(declared)
	for _, name := range missing {
		fmt.Fprintf(errw, "secrets: %s is declared but not in the catalog — add it via the deployment repo\n", name)
	}
	if len(baseline)+len(consentSet) == 0 {
		return
	}

	var declined []string
	consented := consentSet
	if len(consentSet) > 0 && !consentToSecrets(cmd, consentSet) {
		declined = entryNames(consentSet)
		consented = nil
	}

	pack := append(append([]secrets.Entry{}, baseline...), consented...)
	if len(pack) == 0 {
		if err := secrets.SaveManifest(secrets.Manifest{Task: taskID, Declined: declined}); err != nil {
			fmt.Fprintf(errw, "secrets: record declined names: %v\n", err)
		}
		fmt.Fprintf(errw, "secrets: declined %s — credentialed steps will block\n", strings.Join(declined, ", "))
		return
	}

	if _, err := exec.LookPath("op"); err != nil {
		fmt.Fprintln(errw, "secrets: 1Password CLI not found — install `op`, sign in, then `lode resume` to materialize")
		return
	}

	envFile := filepath.Join(dir, ".worklode", "secrets.env")
	if err := secrets.WriteEnvFile(envFile, pack); err != nil {
		fmt.Fprintf(errw, "secrets: %v\n", err)
		return
	}
	excludeSecretsEnv(dir)

	names := entryNames(pack)
	if err := opRunFunc(dir, envFile, taskID, names, declined, cmd.OutOrStdout(), errw); err != nil {
		fmt.Fprintf(errw, "secrets: materialization failed: %v — signed in to op? `lode resume` re-runs the ceremony\n", err)
		return
	}
	if err := c.RecordSecretsMaterialized(ctx, taskID, names); err != nil {
		fmt.Fprintf(errw, "secrets: record materialization event: %v\n", err)
	}
	fmt.Fprintf(errw, "secrets: materialized %s\n", strings.Join(names, ", "))
	if len(declined) > 0 {
		fmt.Fprintf(errw, "secrets: declined %s\n", strings.Join(declined, ", "))
	}
}

// consentToSecrets shows the non-baseline set and takes one yes/no for it.
// Without a terminal (agent-run `lode next`, --json pipelines) the answer is
// "no": the claim still succeeds, and `lode resume` in a terminal — where the
// operator is present by definition — re-runs the ceremony.
func consentToSecrets(cmd *cobra.Command, entries []secrets.Entry) bool {
	errw := cmd.ErrOrStderr()
	fmt.Fprintln(errw, "This task declares secrets:")
	for _, e := range entries {
		fmt.Fprintf(errw, "  %-28s %s\n", e.Name, e.Description)
	}
	if f, ok := cmd.InOrStdin().(*os.File); ok && !term.IsTerminal(int(f.Fd())) {
		fmt.Fprintln(errw, "secrets: no terminal for consent — declined; `lode resume` in a terminal to materialize")
		return false
	}
	fmt.Fprint(errw, "Materialize into the OS keystore for unattended use? [y/N] ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// secretsSatisfied reports whether a resume can skip the ceremony: a manifest
// exists, every materialized name is still in the keystore, and every
// declared name was either materialized or explicitly declined.
func secretsSatisfied(taskID string, declared []string) bool {
	m, ok := secrets.LoadManifest(taskID)
	if !ok {
		return len(declared) == 0
	}
	for _, n := range m.Materialized {
		if _, err := secrets.Fetch(taskID, n); err != nil {
			return false
		}
	}
	for _, n := range declared {
		if !contains(m.Materialized, n) && !contains(m.Declined, n) {
			return false
		}
	}
	return true
}

// excludeSecretsEnv adds .worklode/secrets.env to the repo's local ignore
// file (info/exclude in the common git dir) so the refs-only template is
// never committed. Best-effort: any failure is silent.
func excludeSecretsEnv(dir string) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return
	}
	exclude := filepath.Join(strings.TrimSpace(string(out)), "info", "exclude")
	const line = ".worklode/secrets.env"
	if data, err := os.ReadFile(exclude); err == nil && strings.Contains(string(data), line) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// entryNames projects entries to their names.
func entryNames(entries []secrets.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}
```

- [ ] **Step 4: Run the ceremony tests**

Run: `go test ./internal/cmd/ -run TestCeremony -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Wire into `lode next` and `lode resume`**

In `runNext` (`internal/cmd/lifecycle.go`), directly after the successful
brief fetch (after the `rollbackClaim` error return at :206-210):

```go
	// Spec 017: consent + materialization while the operator is present.
	// Never fails the claim; writes to stderr only.
	runSecretsCeremony(ctx, cmd, c, taskID, dir, brief.Task.Secrets)
```

In `runResume`, after the second `c.Brief` fetch (:291-294), before the
output:

```go
	if !secretsSatisfied(taskID, brief.Task.Secrets) {
		runSecretsCeremony(ctx, cmd, c, taskID, root, brief.Task.Secrets)
	}
```

(`ctx` is already in scope in both.)

- [ ] **Step 6: Run the full cmd suite**

Run: `go test ./internal/cmd/...`
Expected: PASS. (`runNext`'s existing tests, if any, hit the ceremony's
catalog fetch; the no-declared-names + error path is silent by design, so
nothing changes for them.)

- [ ] **Step 7: Commit**

```bash
git add internal/cmd
git commit -m "Run the claim-time secrets ceremony in lode next and resume"
```

---

## Task 12: Purge on every release path

**Files:**
- Modify: `internal/cmd/lifecycle.go` (`newDoneCmd` :304, `newBlockCmd` :336), `internal/hookrun/hookrun.go` (`handleWorktreeRemove` :452, `handleWorktreeExit` :561)
- Test: `internal/hookrun/hookrun_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/hookrun/hookrun_test.go` (package `hookrun`; reuse its
existing Options fixtures — hook handlers must never fail the event, so the
purge is assert-by-side-effect):

```go
func TestWorktreeRemovePurgesSecrets(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())

	if err := secrets.Put("WL-3", "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-3", Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	payload := `{"cwd":"/tmp","tool_input":{"path":"/repo/wt/WL-3-fix"}}`
	var stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "worktree-remove",
		Stdin:  strings.NewReader(payload),
		Stdout: io.Discard,
		Stderr: &stderr,
		// No backbone: the release call will warn, but the local purge must
		// have happened regardless.
		NewClient: func() (*cli.Client, error) { return nil, errors.New("no config") },
	})
	if code != 0 {
		t.Fatalf("hook exit = %d; hooks never fail the event", code)
	}
	if _, err := secrets.Fetch("WL-3", "A_TOKEN"); err == nil {
		t.Fatal("secret survived worktree removal")
	}
	if _, ok := secrets.LoadManifest("WL-3"); ok {
		t.Fatal("manifest survived worktree removal")
	}
}
```

Add the needed imports (`"bytes"`, `"context"`, `"errors"`, `"io"`,
`"strings"`, `"github.com/zalando/go-keyring"`,
`"github.com/sunstoneinstitute/worklode/internal/cli"`,
`"github.com/sunstoneinstitute/worklode/internal/secrets"`) if absent.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/hookrun/ -run TestWorktreeRemovePurgesSecrets`
Expected: FAIL — the secret survives.

- [ ] **Step 3: Implement**

In `internal/hookrun/hookrun.go` (import
`"github.com/sunstoneinstitute/worklode/internal/secrets"`), add a helper
next to `endSession`:

```go
// purgeSecrets removes a task's materialized secrets when its worktree goes
// away — materialized lifetime equals worktree lifetime (spec 017). Local
// only, so it runs BEFORE any backbone call and regardless of their outcome.
func purgeSecrets(opts Options, taskID string) {
	names, err := secrets.PurgeTask(taskID)
	if err != nil {
		warn(opts, "purge secrets for %s: %v", taskID, err)
		return
	}
	if len(names) > 0 {
		warn(opts, "purged secrets for %s: %s", taskID, strings.Join(names, ", "))
	}
}
```

Call it first in `handleWorktreeRemove` (immediately after the `ParseDir`
guard, before `opts.client()`) and in `handleWorktreeExit` (immediately after
its `ParseDir` guard).

In `internal/cmd/lifecycle.go` (import
`"github.com/sunstoneinstitute/worklode/internal/secrets"`):

- `newDoneCmd`: after the successful `c.DoneTask` call, before printing:

```go
			if names, err := secrets.PurgeTask(taskID); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "secrets: purge: %v\n", err)
			} else if len(names) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "secrets: purged %s\n", strings.Join(names, ", "))
			}
```

- `newBlockCmd`: the same block after the successful `c.ReleaseLease` call.

(Add `"strings"` to lifecycle.go imports if absent.)

- [ ] **Step 4: Run the suites**

Run: `go test ./internal/hookrun/... ./internal/cmd/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hookrun internal/cmd
git commit -m "Purge task secrets on done, block, and worktree removal"
```

---

## Task 13: Deployment wiring for the catalog

**Files:**
- Create: `deploy/base/secrets-catalog.yaml`
- Modify: `deploy/base/kustomization.yaml`, `deploy/base/configmap.yaml`, `deploy/base/deployment.yaml`

- [ ] **Step 1: Create the catalog ConfigMap**

`deploy/base/secrets-catalog.yaml`:

```yaml
---
# The org secrets catalog (spec 017): symbolic name -> op:// reference +
# policy. Names and refs only — NEVER values. Real entries are added via the
# normal PR flow; vault/item names are mildly sensitive, so the server serves
# this only to authenticated actors.
apiVersion: v1
kind: ConfigMap
metadata:
  name: worklode-secrets-catalog
  namespace: worklode
data:
  catalog.toml: |
    # [GITHUB_TOKEN]
    # ref = "op://Employee/GitHub agent token/credential"
    # description = "GitHub credential the agent operates as"
    # baseline = true
```

- [ ] **Step 2: Wire it up**

`deploy/base/kustomization.yaml` — add to `resources:`:

```yaml
  - secrets-catalog.yaml
```

`deploy/base/configmap.yaml` — add to `data:`:

```yaml
  LODE_SECRETS_CATALOG_PATH: "/etc/worklode/secrets-catalog/catalog.toml"
```

`deploy/base/deployment.yaml` — on the `worklode` container add:

```yaml
          volumeMounts:
            - name: secrets-catalog
              mountPath: /etc/worklode/secrets-catalog
              readOnly: true
```

and under `volumes:`:

```yaml
        - name: secrets-catalog
          configMap:
            name: worklode-secrets-catalog
```

- [ ] **Step 3: Verify the manifests build**

Run: `kubectl kustomize deploy/base >/dev/null && kubectl kustomize deploy/overlays/hzdev >/dev/null`
Expected: no output, exit 0. (If `kubectl` is unavailable, `kustomize build`
is equivalent.)

- [ ] **Step 4: Commit**

```bash
git add deploy/base
git commit -m "Mount the secrets catalog ConfigMap into the server"
```

---

## Task 14: Documentation and the `lode-secrets` skill

**Files:**
- Modify: `README.md`
- Out of repo: PR to `sunstoneinstitute/claude-plugins` adding `plugins/sunstone-dev/skills/lode-secrets/SKILL.md`

- [ ] **Step 1: Document in the README**

Add a `## Task secrets` section to `README.md` (place it near the existing
lifecycle/plugin documentation), covering exactly:

- Declaring: `lode task add --secrets KUBECONFIG_HZDEV,OPENALEX_API_KEY …`,
  `lode task edit <id> --secrets …` (`none` clears); names come from
  `lode secrets catalog`; a name missing from the catalog is a claim-time
  warning, never a failure.
- The ceremony at `lode next`/`lode resume`: one consent for the non-baseline
  set, one `op run` authorization, values go to the OS keystore
  (service `worklode:<task-id>`); `.worklode/secrets.env` holds references
  only. Declining or having no terminal defers to `lode resume`.
- Running: `lode secrets exec -- <command>`; `lode secrets status`;
  purge on `lode done` / `lode block` / worktree removal.
- Server side: `LODE_SECRETS_CATALOG_PATH` and the
  `worklode-secrets-catalog` ConfigMap.
- The guarantees: Worklode stores names only; values never touch disk, logs,
  or the event log; the `secrets_materialized` event lists names.

Keep it to ~30 lines in the README's existing terse style.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "Document task-declared secrets"
```

- [ ] **Step 3: Ship the skill (separate repo — do NOT add to worklode)**

Open a PR to `sunstoneinstitute/claude-plugins` (local clone:
`~/git/sunstone/claude-plugins`) creating
`plugins/sunstone-dev/skills/lode-secrets/SKILL.md`:

```markdown
---
name: lode-secrets
description: Use when writing Worklode plans or executing Worklode tasks that involve credentials, API keys, kubeconfigs, tokens, or other secrets - declaring catalog secret names on tasks at planning time, and running credentialed commands via `lode secrets exec` at execution time. Also use when a command fails for lack of a credential inside a Worklode worktree.
---

# Worklode task secrets

Tasks declare the secrets they need by symbolic name (spec 017). Values are
materialized into the OS keystore at claim time; you never see or handle them.

## Writing plans

- Every plan task lists the catalog secret names its executor will need.
  Browse them with `lode secrets catalog`.
- Put the names on the task: `lode task add --secrets NAME1,NAME2 ...`.
- A needed secret with no catalog entry is a plan-level finding: add the
  entry via a deployment-repo PR before the task is executable. Do not invent
  names — they are org-unique and env-var style (`^[A-Z][A-Z0-9_]*$`).

## Executing tasks

- Run credentialed commands via `lode secrets exec -- <command> [args...]`
  from inside the task worktree. The command's environment gets exactly the
  task's materialized names.
- `lode secrets status` shows declared vs materialized names.
- NEVER probe `op`, ask the operator for a value, or read
  `.worklode/secrets.env` expecting values — it holds `op://` references only.
- A needed-but-unavailable secret is a BLOCK signal, not something to work
  around: run `lode block --on <blocker>` or record
  `missing-secret: NAME` and stop. Do not retry, do not improvise.

This file contains no `op://` references and no values, by design.
```

PR title: `Add the lode-secrets skill (worklode spec 017)`. This step touches
no file in the worklode repo.

---

## Final verification

- [ ] Run: `go test ./...` — everything passes.
- [ ] Manual acceptance pass against `docs/specs/017-task-secrets.md`
      §Acceptance criteria 1–7 (3, 4, and 7 need a real `op` session and are
      manual by design; 1, 2, 5, 6 are covered by the automated tests in
      Tasks 2, 4, 9–12).
