---
status: accepted
task: WL-16
covers: docs/specs/017-task-secrets.md
---
# Task secrets 3/3: ceremony and rollout — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 3 of 3. Task numbering is global across the series: this plan
holds Tasks 11–14; `2026-07-30-task-secrets-1-server-core.md` (Tasks 1–5) and
`2026-07-30-task-secrets-2-cli-runtime.md` (Tasks 6–10) must both be merged
first.

**Goal:** Turn the parts 1–2 machinery into a live feature: the claim-time
ceremony in `lode next`/`lode resume` (consent → one `op run` → OS keystore),
purge on every release path, the deployment wiring that gives the server a
catalog, and the documentation plus the `lode-secrets` skill agents read.

**Architecture:** `internal/cmd/secretsceremony.go` hosts the ceremony:
resolve the task's declared names ∪ the catalog's baseline set, take one
consent for the non-baseline part, write `.worklode/secrets.env` (references
only), invoke `op run` exactly once with `lode secrets pack` as its child so
values land in the keystore and never in an argument or a log, then record the
names-only `secrets_materialized` event. The `op run` step sits behind an
injectable `opRunFunc` so tests never shell out. Purge is wired into
`lode done`, `lode block`, and the worktree-remove/exit hooks; because the
manifest lives outside the worktree, removal still knows what to purge.

**Tech Stack:** Go 1.26, cobra CLI, `github.com/zalando/go-keyring`
(`keyring.MockInit()` in tests), Kustomize manifests under `deploy/base`,
1Password CLI (`op`) at runtime only — never in tests.

**Spec:** `docs/specs/017-task-secrets.md`. See part 1's header for the full
series scope, the prior-art map, and the migration-number note.

**Prerequisites (landed by parts 1–2):** the `0008` migration and
`tasks.secrets` plumbing through store and API; `internal/secrets` complete
(`ValidName`, `ParseCatalog`/`Resolve`, keystore, manifest, `WriteEnvFile`);
`GET /api/v1/secrets/catalog` and
`POST /api/v1/tasks/{id}/secrets-materialized`; the client methods
(`SecretsCatalog`, `RecordSecretsMaterialized`); `--secrets` on
`task add`/`task edit`; and the `lode secrets catalog|pack|purge|status|exec`
command tree.

Design calls this plan inherits (recorded in part 1, restated because they
shape Tasks 11–14):

1. **Declining consent skips only the non-baseline set**; baseline secrets are
   exempt from consent per the spec's own catalog semantics and still pack.
   (Task 11.)
2. **The materialized-names manifest lives at
   `~/.cache/worklode/secrets/<task-id>.json`**, not in the worktree: keyring
   has no enumeration API, and purge must work after the worktree is deleted —
   which is exactly the `handleWorktreeRemove` path Task 12 wires. Names only —
   never values or refs.
3. **The `lode-secrets` skill ships as a PR to `sunstoneinstitute/claude-plugins`**
   (`plugins/sunstone-dev/skills/lode-secrets/`), next to `worklode-onboarding`
   — this repo has no plugin skill directory. Task 14 carries the full content;
   its step touches no file in this repo.

One more part-1 decision surfaces here as documentation only: the Linux
keystore is Secret Service via go-keyring, so **headless Linux without a
Secret Service degrades to the block-signal path** — Task 14's README section
and skill both have to say so.

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/cmd/secretsceremony.go` | the claim-time ceremony; injectable `opRunFunc` |
| `internal/cmd/secretsceremony_test.go` | one op call, decline, missing-name warning, catalog-down, no-op-binary |
| `deploy/base/secrets-catalog.yaml` | `worklode-secrets-catalog` ConfigMap (empty placeholder catalog) |

**Modified files**

| Path | Change |
|---|---|
| `internal/cmd/lifecycle.go` | ceremony in `runNext`/`runResume`; purge in `done`/`block` |
| `internal/hookrun/hookrun.go` | purge in `handleWorktreeRemove`/`handleWorktreeExit` |
| `internal/hookrun/hookrun_test.go` | purge on remove |
| `deploy/base/kustomization.yaml` | `secrets-catalog.yaml` resource |
| `deploy/base/configmap.yaml` | `LODE_SECRETS_CATALOG_PATH` |
| `deploy/base/deployment.yaml` | mount the catalog ConfigMap |
| `README.md` | document declaration, ceremony, `lode secrets`, degradation |

**Out of repo (Task 14):** `sunstoneinstitute/claude-plugins` PR adding
`plugins/sunstone-dev/skills/lode-secrets/SKILL.md`.

**Test commands**

- CLI/hooks (keyring mocked, no Postgres): `go test ./internal/cmd/... ./internal/hookrun/...`
- Manifests: `kubectl kustomize deploy/base >/dev/null && kubectl kustomize deploy/overlays/hzdev >/dev/null`
- Everything: `go test ./...`

No test may shell out to `op` or touch a real keychain: every keystore test
starts with `keyring.MockInit()`, and the ceremony's `op run` step is behind
the swappable `opRunFunc`.

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
      Tasks 2 and 4 (part 1), 9–10 (part 2), and 11–12).
