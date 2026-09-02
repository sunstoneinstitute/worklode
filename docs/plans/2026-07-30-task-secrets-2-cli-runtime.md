---
status: accepted
covers: docs/specs/017-task-secrets.md
---
# Task secrets 2/3: CLI runtime — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 3. Task numbering is global across the series: this plan
holds Tasks 6–10; `2026-07-30-task-secrets-1-server-core.md` (Tasks 1–5) must
be merged first; `2026-07-30-task-secrets-3-ceremony-and-rollout.md` (Tasks
11–14) follows.

**Goal:** Ship the client-side secrets machinery: client methods for the two
new endpoints, `--secrets` on `task add`/`task edit` plus brief display, the
`internal/secrets` runtime halves (keystore, names-only manifest, env-file),
and the `lode secrets catalog|pack|purge|status|exec` command tree. At the end
of this part the CLI machinery works standalone against a part-1 server; the
claim-time ceremony that drives it automatically lands in part 3.

**Architecture:** `internal/secrets` gains the go-keyring keystore (service
`worklode:<task-id>`), a names-only manifest at
`~/.cache/worklode/secrets/<task-id>.json`, and `WriteEnvFile`, which renders
`NAME=op://…` reference lines into `.worklode/secrets.env` at 0600. `lode
secrets pack` is the sink of the `op run` step — it reads values from its own
environment and writes them to the keystore, so no value is ever an argument.
`lode secrets exec` reads the manifest, fetches those names from the keystore,
and hands them to the child process. Worklode never stores, logs, or
transports a secret value.

**Tech Stack:** Go 1.26, cobra CLI, `github.com/zalando/go-keyring` (already a
dependency; `keyring.MockInit()` in tests), 1Password CLI (`op`) at runtime
only — never in tests.

**Spec:** `docs/specs/017-task-secrets.md`. See part 1's header for the full
series scope, the prior-art map, and the migration-number note.

**Prerequisites (landed by part 1):** the `0008` migration
(`tasks.secrets jsonb`), `Task.Secrets`/`TaskInput.Secrets` in
`internal/store/tasks.go`, `secrets` on the task API (create/patch/`taskJSON`),
`internal/secrets/names.go` (`ValidName`), `internal/secrets/catalog.go`
(`Entry`, `Catalog`, `ParseCatalog`, `Resolve`), and both endpoints —
`GET /api/v1/secrets/catalog` and
`POST /api/v1/tasks/{id}/secrets-materialized`.

Design calls this plan inherits (recorded in part 1, restated because they
shape Tasks 8–10):

1. **Linux keystore = Secret Service via go-keyring**, not the spec's
   ssh-agent-encrypted file. go-keyring is already the module's keystore for
   bearer tokens and covers macOS Keychain + Linux Secret Service with one
   API. Same CLI surface, strictly less new code; headless Linux without a
   Secret Service degrades to the documented block-signal path.
2. **The materialized-names manifest lives at
   `~/.cache/worklode/secrets/<task-id>.json`**, not in the worktree: keyring
   has no enumeration API, and purge must work after the worktree is deleted
   (the `handleWorktreeRemove` path, part 3). Names only — never values or
   refs.

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/secrets/keystore.go` | `Put`/`Fetch`/`Del`/`PurgeTask` on service `worklode:<task-id>` |
| `internal/secrets/manifest.go` | names-only manifest load/save/remove under `~/.cache/worklode/secrets/` |
| `internal/secrets/envfile.go` | `WriteEnvFile` — `NAME=op://…` lines, 0600, refs only |
| `internal/secrets/secrets_test.go` | keystore (MockInit), manifest, envfile, purge |
| `internal/cmd/secrets.go` | `lode secrets catalog\|status\|exec\|purge\|pack` |
| `internal/cmd/secrets_test.go` | pack/exec/purge/status against MockInit + fake worktree |

**Modified files**

| Path | Change |
|---|---|
| `internal/cli/client.go` | `Task.Secrets`, input fields, `SecretsCatalog`, `RecordSecretsMaterialized` |
| `internal/cli/client_test.go` | catalog + record round-trips |
| `internal/cmd/task.go` | `--secrets` on `task add`/`task edit`; `printBrief` shows the names |
| `internal/cmd/task_test.go` | flag parsing, `none` clears, brief rendering |
| `internal/hookrun/hookrun.go` | secrets line in `compactBrief` |

**Test commands**

- Pure packages: `go test ./internal/secrets/...`
- CLI/hooks (keyring mocked, no Postgres): `go test ./internal/cmd/... ./internal/cli/... ./internal/hookrun/...`
- Everything: `go test ./...`

No test may shell out to `op` or touch a real keychain: every keystore test
starts with `keyring.MockInit()`.

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

