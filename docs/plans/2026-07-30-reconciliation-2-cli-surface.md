---
status: accepted
task: WL-11
implements: docs/specs/013-reconciliation.md
---
# Reconciliation 2/3: endpoints & CLI surface — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 3. Task numbering is global across the series: this plan
holds Tasks 5–9; `2026-07-30-reconciliation-1-replay-engine.md` (Tasks 1–4)
must be merged first; `2026-07-30-reconciliation-3-poll-engine.md` (Tasks
10–13) follows.

**Goal:** Ship the user-facing surface: `GET /api/v1/whoami`,
`GET /api/v1/repos/doctor`, and `POST /api/v1/reconcile` (replay engine only
for now), backing `lode doctor`, `lode project doctor`, and `lode reconcile`.

**Architecture:** Handlers live in `internal/api/reconcile.go`. `lode doctor`
is client-side only and must stay useful with the server unreachable.
`lode project doctor` reads ingestion-health store queries over the
`applied_at`/`mapped_at` columns from part 1. `POST /api/v1/reconcile` runs
engine 1 (`hooks.Replay`, part 1); its response shape is designed up front to
also carry engine 2's result, which part 3 wires into the same handler.

**Tech Stack:** Go 1.x, cobra CLI, `net/http` mux (Go 1.22 routing patterns),
PostgreSQL via `database/sql`, standard-library testing.

**Spec:** `docs/specs/013-reconciliation.md`, read with its amendments from
`docs/specs/014-design-documents-as-graph-objects.md` §6: **engine 3
(spec-doc drift) and the `task_docs` table are superseded and must not be
built.** See part 1's header for the full series scope, prior-art map, and
what is owned elsewhere.

**Prerequisites (landed by part 1):** the `0008` migration
(`events.applied_at`, `project_repos.mapped_at` — existing `project_repos`
rows backfilled to epoch, new rows default `now()`), the transport-independent
`applier` (`internal/hooks/apply.go`), engine 1 (`hooks.Replay`), and
`internal/store/reconcile.go` with `MarkEventApplied` /
`UnappliedGitHubEvents`.

Design calls this plan inherits (recorded in part 1, restated because they
shape Tasks 8–9):

- **`project_repos.mapped_at`** exists so the "last webhook predates its
  mapping" check is decidable; epoch backfill means no current repo
  retroactively alarms.
- **`--task` does not bound engine 1**: an ignored event's task binding is
  unknown before its apply runs. When `task` is set, replay is skipped and
  only engine 2 runs (until part 3 lands, that means a skipped-poll response).

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/api/reconcile.go` | handlers: `whoami`, `reposDoctor`, `reconcile` (+ `parseSince`) |
| `internal/api/reconcile_test.go` | auth/admin gates, since parsing, replay wiring |
| `internal/cmd/doctor.go` | `lode doctor` — client-side checks, each failure names its fix |
| `internal/cmd/doctor_test.go` | table-driven broken-setup fixtures, exit code + fix text |
| `internal/cmd/reconcile.go` | `lode reconcile` cobra glue |
| `internal/cmd/reconcile_test.go` | flag → request body wiring against a fake server |

**Modified files**

| Path | Change |
|---|---|
| `internal/store/reconcile.go` | append `RepoIngestionHealth`, `UnmappedSenders` (Task 7) |
| `internal/store/reconcile_test.go` | append their tests |
| `internal/api/server.go` | register the three routes |
| `internal/cli/client.go` | `ConfigOrigins`, `WhoAmI`, `ReposDoctor`, `Reconcile` |
| `internal/cmd/project.go` | `lode project doctor [repo]` subcommand |

**Test commands**

- Store/API/cmd suites need Postgres (`store.OpenTestStore`):
  `go test ./internal/store/... ./internal/api/... ./internal/cmd/...`
- No Postgres needed: `go test ./internal/cli/...`
- Everything: `go test ./...`

---
## Task 5: GET /api/v1/whoami

**Files:**
- Create: `internal/api/reconcile.go` (whoami handler; grows in Tasks 8/9)
- Modify: `internal/api/server.go` (route)
- Modify: `internal/cli/client.go` (`WhoAmI`)
- Test: `internal/api/reconcile_test.go`, `internal/cli/client_test.go`

- [ ] **Step 1: Write the failing API test**

`internal/api/reconcile_test.go` (`package api_test`, using the existing
`newTestServer`/`doReq` helpers from `internal/api/server_test.go:26,58`):

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWhoami(t *testing.T) {
	_, h, token := newTestServer(t)

	rec := doReq(t, h, http.MethodGet, "/api/v1/whoami", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("whoami: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Admin bool   `json:"admin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" || got.Kind == "" || !got.Admin {
		t.Fatalf("whoami = %+v; want the bootstrap admin actor", got)
	}
}

func TestWhoamiRequiresAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	if rec := doReq(t, h, http.MethodGet, "/api/v1/whoami", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d; want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestWhoami`
Expected: FAIL — 404, route unregistered.

- [ ] **Step 3: Write the handler and route**

`internal/api/reconcile.go`:

```go
// Reconciliation & setup-diagnosis endpoints (spec 013): whoami for the CLI
// doctor, the ingestion-health report, and the reconcile run itself.

package api

import (
	"net/http"
)

// whoamiJSON is the wire form of GET /api/v1/whoami.
type whoamiJSON struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Admin bool   `json:"admin"`
}

// whoami handles GET /api/v1/whoami: the calling actor's identity. Auth
// only, no admin gate — this is how the CLI (and lode doctor) asks whether a
// token is accepted and who it belongs to.
func (s *server) whoami(w http.ResponseWriter, r *http.Request) {
	a := actorFrom(r)
	writeJSON(w, http.StatusOK, whoamiJSON{ID: a.ID, Kind: a.Kind, Admin: a.Admin})
}
```

In `internal/api/server.go`, next to the board route (line 304):

```go
	mux.Handle("GET /api/v1/whoami", s.auth(s.whoami))
```

- [ ] **Step 4: Add the client method**

In `internal/cli/client.go`, after `ServerURL` (line 308):

```go
// WhoAmI is the response of GET /api/v1/whoami.
type WhoAmI struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Admin bool   `json:"admin"`
}

// WhoAmI calls GET /api/v1/whoami: which actor the configured token belongs
// to. A *ClientError with Status 401 means the token is not accepted; a
// transport error means the server is unreachable — lode doctor tells those
// two failures apart.
func (c *Client) WhoAmI(ctx context.Context) (WhoAmI, []byte, error) {
	raw, err := c.do(ctx, http.MethodGet, "/api/v1/whoami", nil)
	if err != nil {
		return WhoAmI{}, nil, err
	}
	var who WhoAmI
	if err := json.Unmarshal(raw, &who); err != nil {
		return WhoAmI{}, nil, fmt.Errorf("decode whoami: %w", err)
	}
	return who, raw, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/api/ -run TestWhoami -v && go test ./internal/cli/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/reconcile.go internal/api/reconcile_test.go internal/api/server.go internal/cli/client.go
git commit -m "Add GET /api/v1/whoami"
```

---

## Task 6: lode doctor

Client-side only; must produce useful output with the server unreachable and
exit non-zero when any check fails, each failure naming its fix (spec 013
§`lode doctor`).

**Files:**
- Modify: `internal/cli/client.go` (`ConfigOrigins` export)
- Create: `internal/cmd/doctor.go`
- Test: `internal/cmd/doctor_test.go`

- [ ] **Step 1: Export the config-origin probe**

In `internal/cli/client.go`, next to `findRepoConfig` (line 71):

```go
// ConfigOrigins reports where config loading would look from startDir: the
// user config path (and whether the file exists) and the repo-local
// .worklode/.lode config the walk-up found, if any. lode doctor reports
// these; LoadConfig remains the authority on what actually loads.
func ConfigOrigins(startDir string) (userPath string, userFound bool, repoPath string, repoFound bool) {
	if p, err := configPath(); err == nil {
		userPath = p
		if _, statErr := os.Stat(p); statErr == nil {
			userFound = true
		}
	}
	repoPath, repoFound = findRepoConfig(startDir)
	return userPath, userFound, repoPath, repoFound
}
```

- [ ] **Step 2: Write the failing test**

`internal/cmd/doctor_test.go` (`package cmd`, using the existing `runLode`
helper from `internal/cmd/lifecycle_test.go:57` and `setupRepoConfig` from
`internal/cmd/currentproject_test.go:17`; these tests fake the server with
`httptest`, so they need no Postgres):

```go
package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLode serves whoami and the project list the way lode doctor consumes
// them, and points LODE_SERVER/LODE_TOKEN at itself.
func fakeLode(t *testing.T, whoamiStatus int, projects string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/whoami":
			w.WriteHeader(whoamiStatus)
			if whoamiStatus == http.StatusOK {
				io.WriteString(w, `{"id":"stig","kind":"human","admin":true}`)
			} else {
				io.WriteString(w, `{"error":"unauthorized"}`)
			}
		case "/api/v1/projects":
			io.WriteString(w, `{"projects":[`+projects+`]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
}

func TestDoctorHealthySetup(t *testing.T) {
	repo := setupRepoConfig(t, "demo")
	gitInit(t, repo)
	fakeLode(t, http.StatusOK, `{"id":"demo","name":"Demo","key":"WL","repos":[],"focus":[]}`)
	if _, _, err := installGitHooks(repo); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	out, err := runLode(t, "doctor")
	if err != nil {
		t.Fatalf("healthy doctor exited non-zero: %v\n%s", err, out)
	}
	for _, want := range []string{"config", "server", "token", "current_project", "hooks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q check:\n%s", want, out)
		}
	}
}

func TestDoctorFailuresNameTheirFix(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T)
		wantFix string
	}{
		{
			name: "missing config",
			setup: func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				t.Chdir(t.TempDir())
				t.Setenv("LODE_SERVER", "")
				t.Setenv("LODE_TOKEN", "")
			},
			wantFix: "config.toml",
		},
		{
			name: "unreachable server",
			setup: func(t *testing.T) {
				setupRepoConfig(t, "demo")
				// A closed port: connection refused, not a slow timeout.
				t.Setenv("LODE_SERVER", "http://127.0.0.1:1")
				t.Setenv("LODE_TOKEN", "wl_test")
			},
			wantFix: "server",
		},
		{
			name: "invalid token",
			setup: func(t *testing.T) {
				setupRepoConfig(t, "demo")
				fakeLode(t, http.StatusUnauthorized, ``)
			},
			wantFix: "lode login",
		},
		{
			name: "unset current_project",
			setup: func(t *testing.T) {
				setupRepoConfig(t, "")
				fakeLode(t, http.StatusOK, ``)
			},
			wantFix: "current_project",
		},
		{
			name: "missing git hooks",
			setup: func(t *testing.T) {
				repo := setupRepoConfig(t, "demo")
				gitInit(t, repo)
				fakeLode(t, http.StatusOK, `{"id":"demo","name":"Demo","key":"WL","repos":[],"focus":[]}`)
			},
			wantFix: "lode install",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			out, err := runLode(t, "doctor")
			if err == nil {
				t.Fatalf("doctor exited zero on a broken setup:\n%s", out)
			}
			if !strings.Contains(out, tc.wantFix) {
				t.Fatalf("doctor output does not name the fix %q:\n%s", tc.wantFix, out)
			}
		})
	}
}

// gitInit makes dir a git repo so the hooks checks have a hooks directory.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if out, err := execGit(dir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// execGit runs one git command in dir.
func execGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}
```

(with `"os/exec"` in the imports). If the cmd test package already has a
git-runner helper, use it instead of adding `execGit`. Note
`setupRepoConfig` chdirs into the repo; the
"missing config" case chdirs to a plain temp dir with an empty `HOME`, and
clears the env overrides so no server is configured at all.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run TestDoctor`
Expected: FAIL — `unknown command "doctor" for "lode"`

- [ ] **Step 4: Write the command**

`internal/cmd/doctor.go`:

```go
// lode doctor: client-side setup diagnosis (spec 013). Runs entirely
// locally, needs no privileges, and stays useful with the server
// unreachable. Each failing check names its fix; any failure exits non-zero
// so hooks and CI can gate on it.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// doctorCheck is one pass/fail line of the report. Fix is set only on
// failure. Skipped checks (e.g. the worktree check outside a worktree, or a
// server-side check with the server unreachable) count as neither.
type doctorCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix,omitempty"`
}

func pass(name, detail string) doctorCheck { return doctorCheck{Name: name, OK: true, Detail: detail} }
func fail(name, detail, fix string) doctorCheck {
	return doctorCheck{Name: name, Detail: detail, Fix: fix}
}
func skip(name, detail string) doctorCheck {
	return doctorCheck{Name: name, OK: true, Skipped: true, Detail: detail}
}

// runDoctorChecks runs the spec's six checks in order from dir. Later checks
// still run when earlier ones fail, degrading to skips where they cannot be
// evaluated, so one run reports everything wrong at once.
func runDoctorChecks(ctx context.Context, dir string) []doctorCheck {
	var checks []doctorCheck

	// 1. Config file found — which one, and where the walk-up located it.
	userPath, userFound, repoPath, repoFound := cli.ConfigOrigins(dir)
	switch {
	case repoFound:
		checks = append(checks, pass("config", "repo config "+repoPath))
	case userFound:
		checks = append(checks, pass("config", "user config "+userPath))
	default:
		checks = append(checks, fail("config",
			"no config file found (looked for a repo-local .worklode/.lode config above "+dir+" and "+userPath+")",
			"run `lode login <server-url>` or create "+userPath+" with server = \"https://...\""))
	}

	cfg, cfgErr := cli.LoadConfig()
	if cfgErr != nil {
		checks = append(checks, fail("config-load", cfgErr.Error(), "fix the config file reported above"))
		return checks
	}

	// 2. server set and reachable / 3. token present and accepted — one
	// whoami round trip answers both: a transport error is "unreachable", a
	// 401 is "token rejected", 200 is both green.
	var c *cli.Client
	serverReachable := false
	switch {
	case cfg.ServerURL == "":
		checks = append(checks, fail("server", "server URL not set",
			"set LODE_SERVER or add server = \"https://...\" to the config file"))
	default:
		c = cli.NewClient(cfg)
		who, _, whoErr := c.WhoAmI(ctx)
		var ce *cli.ClientError
		switch {
		case whoErr == nil:
			serverReachable = true
			checks = append(checks, pass("server", cfg.ServerURL+" reachable"))
			if cfg.Token == "" {
				// 200 with no token cannot happen; guard anyway.
				checks = append(checks, fail("token", "no token configured", "run `lode login`"))
			} else {
				checks = append(checks, pass("token", "accepted; you are "+who.ID+" ("+who.Kind+")"))
			}
		case errors.As(whoErr, &ce):
			serverReachable = true
			checks = append(checks, pass("server", cfg.ServerURL+" reachable"))
			if cfg.Token == "" {
				checks = append(checks, fail("token",
					"no token in the OS keychain or LODE_TOKEN", "run `lode login`"))
			} else {
				checks = append(checks, fail("token",
					fmt.Sprintf("server rejected the token (%d)", ce.Status), "run `lode login` to mint a fresh token"))
			}
		default:
			checks = append(checks, fail("server", cfg.ServerURL+" unreachable: "+whoErr.Error(),
				"check the server URL and your network; set LODE_SERVER to override"))
			checks = append(checks, skip("token", "not checked (server unreachable)"))
		}
	}

	// 4. current_project set, and the project exists.
	switch {
	case cfg.CurrentProject == "":
		checks = append(checks, fail("current_project", "not set",
			"add current_project = \"<project-id>\" to .worklode/config.toml (or the user config)"))
	case !serverReachable:
		checks = append(checks, skip("current_project", cfg.CurrentProject+" (existence not checked: server unreachable)"))
	default:
		if _, err := c.GetProject(ctx, cfg.CurrentProject); err != nil {
			checks = append(checks, fail("current_project",
				"project "+cfg.CurrentProject+" not found on the server",
				"fix current_project in the config, or create the project with `lode project add`"))
		} else {
			checks = append(checks, pass("current_project", cfg.CurrentProject))
		}
	}

	// 5. Git hooks installed in this repo.
	if hooksDir, err := resolveHooksDir(dir); err != nil {
		checks = append(checks, skip("hooks", "not in a git repository"))
	} else {
		content, readErr := os.ReadFile(filepath.Join(hooksDir, "pre-commit"))
		if readErr == nil && strings.Contains(string(content), hookMarker) {
			checks = append(checks, pass("hooks", "pre-commit installed in "+hooksDir))
		} else {
			checks = append(checks, fail("hooks", "worklode pre-commit hook not installed",
				"run `lode install` in this repo"))
		}
	}

	// 6. Inside a task worktree: does it map to a task with a live lease.
	root, inRepo := worktree.Root(dir)
	taskID, isTaskWT := "", false
	if inRepo {
		taskID, isTaskWT = worktree.ParseDir(root)
	}
	switch {
	case !isTaskWT:
		checks = append(checks, skip("worktree", "not inside a task worktree"))
	case !serverReachable:
		checks = append(checks, skip("worktree", taskID+" (lease not checked: server unreachable)"))
	default:
		detail, _, err := c.GetTask(ctx, taskID)
		switch {
		case err != nil:
			checks = append(checks, fail("worktree", "worktree names task "+taskID+", which the server does not know",
				"remove the stale worktree, or create/claim the task"))
		case detail.Lease == nil:
			checks = append(checks, fail("worktree", "task "+taskID+" has no live lease",
				"run `lode claim "+taskID+"` from this worktree"))
		default:
			checks = append(checks, pass("worktree", taskID+" leased until "+detail.Lease.ExpiresAt.Format("2006-01-02 15:04")))
		}
	}

	return checks
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose this machine's lode setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			checks := runDoctorChecks(cmd.Context(), dir)

			failed := 0
			for _, c := range checks {
				if !c.OK {
					failed++
				}
			}
			if jsonOut(cmd) {
				b, err := json.MarshalIndent(struct {
					OK     bool          `json:"ok"`
					Checks []doctorCheck `json:"checks"`
				}{OK: failed == 0, Checks: checks}, "", "  ")
				if err != nil {
					return err
				}
				cmd.Println(string(b))
			} else {
				for _, c := range checks {
					mark := "ok  "
					switch {
					case c.Skipped:
						mark = "skip"
					case !c.OK:
						mark = "FAIL"
					}
					cmd.Printf("%s  %-16s %s\n", mark, c.Name, c.Detail)
					if c.Fix != "" {
						cmd.Printf("      fix: %s\n", c.Fix)
					}
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d check(s) failed", failed)
			}
			return nil
		},
	}
}

func init() {
	rootCmd.AddCommand(newDoctorCmd())
}
```

Adjust the `detail.Lease.ExpiresAt` field name to whatever
`cli.Lease` actually calls it (`internal/cli/client.go:383`) — do not add a
new field.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/cmd/ -run TestDoctor -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cli/client.go internal/cmd/doctor.go internal/cmd/doctor_test.go
git commit -m "Add lode doctor: client-side setup diagnosis"
```

---

## Task 7: Ingestion-health store queries

**Files:**
- Modify: `internal/store/reconcile.go` (append)
- Test: `internal/store/reconcile_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/reconcile_test.go`:

```go
func TestRepoIngestionHealth(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map acme/app: %v", err)
	}
	if err := s.AddRepo(ctx, "demo", "acme/silent"); err != nil {
		t.Fatalf("map acme/silent: %v", err)
	}

	recordGitHubEvent(t, s, "d-1", "issues.opened.ignored", `{"repository":{"full_name":"acme/app"}}`)
	recordGitHubEvent(t, s, "d-2", "push", `{"repository":{"full_name":"acme/app"}}`)
	recordGitHubEvent(t, s, "d-3", "push.ignored", `{"repository":{"full_name":"acme/unmapped"}}`)

	all, err := s.RepoIngestionHealth(ctx, "")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("health rows = %d, want 2 mapped repos", len(all))
	}
	app, silent := all[0], all[1] // ordered by repo
	if app.Repo != "acme/app" || app.LastEventAt == nil || app.Unapplied != 2 {
		t.Fatalf("acme/app = %+v; want a last event and 2 unapplied", app)
	}
	if len(app.EventTypes) != 2 { // issues.opened.ignored, push
		t.Fatalf("acme/app event types = %v; want 2 distinct types", app.EventTypes)
	}
	if silent.Repo != "acme/silent" || silent.LastEventAt != nil || silent.Unapplied != 0 {
		t.Fatalf("acme/silent = %+v; want no events at all", silent)
	}
	if silent.MappedAt.IsZero() {
		t.Fatalf("mapped_at not populated for a fresh mapping")
	}

	one, err := s.RepoIngestionHealth(ctx, "acme/app")
	if err != nil {
		t.Fatalf("filtered health: %v", err)
	}
	if len(one) != 1 || one[0].Repo != "acme/app" {
		t.Fatalf("filtered health = %+v; want only acme/app", one)
	}

	senders, err := s.UnmappedSenders(ctx)
	if err != nil {
		t.Fatalf("unmapped senders: %v", err)
	}
	if len(senders) != 1 || senders[0].Repo != "acme/unmapped" || senders[0].Events != 1 {
		t.Fatalf("unmapped senders = %+v; want acme/unmapped with 1 event", senders)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestRepoIngestionHealth`
Expected: FAIL — `undefined: (*Store).RepoIngestionHealth`

- [ ] **Step 3: Write the implementation**

Append to `internal/store/reconcile.go` (`"encoding/json"` joins the
imports; the `jsonb_agg` scan follows the `scanProjectFocus` pattern in
`internal/store/projects.go:23`):

```go
// RepoIngestion is one mapped repo's ingestion health: what project doctor
// reports (spec 013 §lode project doctor).
type RepoIngestion struct {
	Repo        string
	ProjectID   string
	MappedAt    time.Time
	LastEventAt *time.Time // nil: this repo has never sent a webhook
	EventTypes  []string   // distinct event types seen, sorted
	Unapplied   int        // events still awaiting replay
}

// RepoIngestionHealth returns per-repo ingestion health for every mapped
// repo (or just one, when repo is non-empty), ordered by repo. Events
// correlate to repos by the delivery payload's repository.full_name; this
// scans the events table and is an operator-frequency query, not a hot path.
func (s *Store) RepoIngestionHealth(ctx context.Context, repo string) ([]RepoIngestion, error) {
	q := `SELECT pr.repo, pr.project_id, pr.mapped_at,
	             e.last_event_at, COALESCE(e.event_types, '[]'::jsonb), COALESCE(e.unapplied, 0)
	      FROM project_repos pr
	      LEFT JOIN LATERAL (
	          SELECT max(received_at) AS last_event_at,
	                 jsonb_agg(DISTINCT type) AS event_types,
	                 count(*) FILTER (WHERE applied_at IS NULL) AS unapplied
	          FROM events
	          WHERE source = 'github'
	            AND payload->'repository'->>'full_name' = pr.repo
	      ) e ON true`
	var args []any
	if repo != "" {
		args = append(args, repo)
		q += ` WHERE pr.repo = $1`
	}
	q += ` ORDER BY pr.repo`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("repo ingestion health: %w", err)
	}
	defer rows.Close()

	var out []RepoIngestion
	for rows.Next() {
		var ri RepoIngestion
		var types []byte
		if err := rows.Scan(&ri.Repo, &ri.ProjectID, &ri.MappedAt, &ri.LastEventAt, &types, &ri.Unapplied); err != nil {
			return nil, fmt.Errorf("scan repo ingestion health: %w", err)
		}
		if err := json.Unmarshal(types, &ri.EventTypes); err != nil {
			return nil, fmt.Errorf("decode event types for %s: %w", ri.Repo, err)
		}
		ri.MappedAt = ri.MappedAt.UTC()
		if ri.LastEventAt != nil {
			u := ri.LastEventAt.UTC()
			ri.LastEventAt = &u
		}
		out = append(out, ri)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo ingestion health: %w", err)
	}
	return out, nil
}

// UnmappedSender is a repo that has sent webhooks but maps to no project.
type UnmappedSender struct {
	Repo        string
	Events      int
	LastEventAt time.Time
}

// UnmappedSenders returns repos seen in github deliveries that have no
// project mapping, ordered by repo.
func (s *Store) UnmappedSenders(ctx context.Context) ([]UnmappedSender, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.payload->'repository'->>'full_name', count(*), max(e.received_at)
		 FROM events e
		 WHERE e.source = 'github'
		   AND e.payload->'repository'->>'full_name' IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM project_repos pr
		                   WHERE pr.repo = e.payload->'repository'->>'full_name')
		 GROUP BY 1 ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("unmapped senders: %w", err)
	}
	defer rows.Close()

	var out []UnmappedSender
	for rows.Next() {
		var u UnmappedSender
		if err := rows.Scan(&u.Repo, &u.Events, &u.LastEventAt); err != nil {
			return nil, fmt.Errorf("scan unmapped sender: %w", err)
		}
		u.LastEventAt = u.LastEventAt.UTC()
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unmapped senders: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/ -run TestRepoIngestionHealth -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/reconcile.go internal/store/reconcile_test.go
git commit -m "Add per-repo ingestion-health queries"
```

---

## Task 8: GET /api/v1/repos/doctor and lode project doctor

**Files:**
- Modify: `internal/api/reconcile.go` (handler)
- Modify: `internal/api/server.go` (route)
- Modify: `internal/cli/client.go` (`ReposDoctor`)
- Modify: `internal/cmd/project.go` (`lode project doctor`)
- Test: `internal/api/reconcile_test.go` (append), `internal/cmd/project_test.go` (append)

- [ ] **Step 1: Write the failing API test**

Append to `internal/api/reconcile_test.go` (reuse `mapRepo` from
`internal/api/projects_resolve_test.go:244` and `seedIssue`-style event
seeding from `internal/api/inbox_test.go`):

```go
func TestReposDoctor(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "demo", "WL", "acme/app")

	// One pre-mapping-style unapplied event for the mapped repo, one event
	// from a repo nothing maps.
	seedGitHubEvent(t, st, "d-1", "push.ignored", `{"repository":{"full_name":"acme/app"}}`)
	seedGitHubEvent(t, st, "d-2", "push.ignored", `{"repository":{"full_name":"acme/unmapped"}}`)

	rec := doReq(t, h, http.MethodGet, "/api/v1/repos/doctor", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("repos doctor: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Repos []struct {
			Repo            string  `json:"repo"`
			Project         string  `json:"project"`
			AppInstalled    *bool   `json:"app_installed"`
			LastEventAt     *string `json:"last_event_at"`
			UnappliedEvents int     `json:"unapplied_events"`
			Stale           bool    `json:"stale"`
		} `json:"repos"`
		UnmappedSenders []struct {
			Repo   string `json:"repo"`
			Events int    `json:"events"`
		} `json:"unmapped_senders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Repos) != 1 || resp.Repos[0].Repo != "acme/app" {
		t.Fatalf("repos = %+v; want acme/app", resp.Repos)
	}
	r := resp.Repos[0]
	if r.AppInstalled != nil {
		t.Fatalf("app_installed = %v; want null (app auth unconfigured in tests)", *r.AppInstalled)
	}
	if r.UnappliedEvents != 1 {
		t.Fatalf("unapplied = %d; want 1", r.UnappliedEvents)
	}
	if len(resp.UnmappedSenders) != 1 || resp.UnmappedSenders[0].Repo != "acme/unmapped" {
		t.Fatalf("unmapped senders = %+v; want acme/unmapped", resp.UnmappedSenders)
	}
}

// TestReposDoctorStale: a mapped repo with no deliveries at all is stale —
// the signal that sends an operator to lode reconcile.
func TestReposDoctorStale(t *testing.T) {
	_, h, token := newTestServer(t)
	mapRepo(t, h, token, "demo", "WL", "acme/silent")

	rec := doReq(t, h, http.MethodGet, "/api/v1/repos/doctor?repo=acme/silent", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("repos doctor: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Repos []struct {
			Stale bool `json:"stale"`
		} `json:"repos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Repos) != 1 || !resp.Repos[0].Stale {
		t.Fatalf("repos = %+v; want one stale repo", resp.Repos)
	}
}

func TestReposDoctorRequiresAdmin(t *testing.T) {
	st, h, token := newTestServer(t)
	nonAdmin := makeNonAdminToken(t, st, h, token)
	if rec := doReq(t, h, http.MethodGet, "/api/v1/repos/doctor", nonAdmin, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d; want 403", rec.Code)
	}
}

// seedGitHubEvent records one github event with a nil apply (applied_at NULL).
func seedGitHubEvent(t *testing.T, st *store.Store, externalID, typ, payload string) {
	t.Helper()
	if _, _, err := st.RecordEvent(context.Background(), "github", externalID, typ,
		[]byte(payload), nil); err != nil {
		t.Fatalf("seed event %s: %v", externalID, err)
	}
}

// makeNonAdminToken creates a non-admin actor and mints a token for it via
// the admin API.
func makeNonAdminToken(t *testing.T, st *store.Store, h http.Handler, adminToken string) string {
	t.Helper()
	rec := doReq(t, h, http.MethodPost, "/api/v1/actors", adminToken,
		map[string]any{"id": "dev", "kind": "human", "display_name": "Dev", "admin": false})
	if rec.Code >= 300 {
		t.Fatalf("create actor: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/actors/dev/tokens", adminToken,
		map[string]any{"description": "test"})
	if rec.Code >= 300 {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body.String())
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil || tok.Token == "" {
		t.Fatalf("decode token: %v (%s)", err, rec.Body.String())
	}
	return tok.Token
}
```

Match `makeNonAdminToken`'s request bodies to the actual `createActor` /
`createToken` handlers in `internal/api/admin.go:288,318` (field names and
response shape); an equivalent helper may already exist in the api test
package — search for one before adding it.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestReposDoctor`
Expected: FAIL — 404, route unregistered.

- [ ] **Step 3: Write the handler and route**

Append to `internal/api/reconcile.go`:

```go
// repoDoctorJSON is one mapped repo's ingestion health on the wire.
// AppInstalled is nil when the server has no GitHub App configured — the
// check cannot run, which is different from "not installed".
type repoDoctorJSON struct {
	Repo            string     `json:"repo"`
	Project         string     `json:"project"`
	AppInstalled    *bool      `json:"app_installed"`
	AppError        string     `json:"app_error,omitempty"`
	MappedAt        time.Time  `json:"mapped_at"`
	LastEventAt     *time.Time `json:"last_event_at"`
	EventTypes      []string   `json:"event_types"`
	UnappliedEvents int        `json:"unapplied_events"`
	// Stale: this repo has never delivered a webhook, or its last delivery
	// predates the mapping — the signal to run lode reconcile.
	Stale bool `json:"stale"`
}

type unmappedSenderJSON struct {
	Repo        string    `json:"repo"`
	Events      int       `json:"events"`
	LastEventAt time.Time `json:"last_event_at"`
}

type reposDoctorResponse struct {
	Repos           []repoDoctorJSON     `json:"repos"`
	UnmappedSenders []unmappedSenderJSON `json:"unmapped_senders"`
}

// reposDoctor handles GET /api/v1/repos/doctor[?repo=owner/name]: per-repo
// ingestion health. Admin-gated — it reads across the whole org.
func (s *server) reposDoctor(w http.ResponseWriter, r *http.Request) {
	health, err := s.st.RepoIngestionHealth(r.Context(), r.URL.Query().Get("repo"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	senders, err := s.st.UnmappedSenders(r.Context())
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	resp := reposDoctorResponse{Repos: []repoDoctorJSON{}, UnmappedSenders: []unmappedSenderJSON{}}
	for _, ri := range health {
		rj := repoDoctorJSON{
			Repo:            ri.Repo,
			Project:         ri.ProjectID,
			MappedAt:        ri.MappedAt,
			LastEventAt:     ri.LastEventAt,
			EventTypes:      ri.EventTypes,
			UnappliedEvents: ri.Unapplied,
			Stale:           ri.LastEventAt == nil || ri.LastEventAt.Before(ri.MappedAt),
		}
		if rj.EventTypes == nil {
			rj.EventTypes = []string{}
		}
		if s.appAuth != nil {
			// Confirmed by minting an installation token (the spec's check);
			// bounded per repo like addRepo's discovery.
			ctx, cancel := context.WithTimeout(r.Context(), discoveryTimeout)
			_, tokErr := s.appAuth.InstallationToken(ctx, ri.Repo)
			cancel()
			installed := tokErr == nil
			rj.AppInstalled = &installed
			if tokErr != nil {
				rj.AppError = tokErr.Error()
			}
		}
		resp.Repos = append(resp.Repos, rj)
	}
	for _, u := range senders {
		resp.UnmappedSenders = append(resp.UnmappedSenders,
			unmappedSenderJSON{Repo: u.Repo, Events: u.Events, LastEventAt: u.LastEventAt})
	}
	writeJSON(w, http.StatusOK, resp)
}
```

(`"context"` and `"time"` join the file's imports; `discoveryTimeout` already
exists at `internal/api/admin.go:223`.)

In `internal/api/server.go`, next to the repos PATCH route (line 292):

```go
	mux.Handle("GET /api/v1/repos/doctor", s.auth(requireAdmin(s.reposDoctor)))
```

- [ ] **Step 4: Run the API tests**

Run: `go test ./internal/api/ -run TestReposDoctor -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Add the client method and CLI command**

In `internal/cli/client.go`, after `AddRepo`:

```go
// RepoDoctor is one repo's ingestion health from GET /api/v1/repos/doctor.
type RepoDoctor struct {
	Repo            string     `json:"repo"`
	Project         string     `json:"project"`
	AppInstalled    *bool      `json:"app_installed"`
	AppError        string     `json:"app_error,omitempty"`
	MappedAt        time.Time  `json:"mapped_at"`
	LastEventAt     *time.Time `json:"last_event_at"`
	EventTypes      []string   `json:"event_types"`
	UnappliedEvents int        `json:"unapplied_events"`
	Stale           bool       `json:"stale"`
}

// UnmappedSender mirrors the server's unmapped_senders entries.
type UnmappedSender struct {
	Repo        string    `json:"repo"`
	Events      int       `json:"events"`
	LastEventAt time.Time `json:"last_event_at"`
}

// ReposDoctorResponse is the response of GET /api/v1/repos/doctor.
type ReposDoctorResponse struct {
	Repos           []RepoDoctor     `json:"repos"`
	UnmappedSenders []UnmappedSender `json:"unmapped_senders"`
}

// ReposDoctor calls GET /api/v1/repos/doctor. An empty repo reports every
// mapped repo. Admin-only on the server.
func (c *Client) ReposDoctor(ctx context.Context, repo string) (ReposDoctorResponse, []byte, error) {
	q := url.Values{}
	if repo != "" {
		q.Set("repo", repo)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/repos/doctor", q), nil)
	if err != nil {
		return ReposDoctorResponse{}, nil, err
	}
	var resp ReposDoctorResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ReposDoctorResponse{}, nil, fmt.Errorf("decode repos doctor: %w", err)
	}
	return resp, raw, nil
}
```

In `internal/cmd/project.go`, add the subcommand to the `AddCommand` list in
`newProjectCmd` (line 19) and:

```go
// newProjectDoctorCmd builds `lode project doctor [repo]`: is ingestion
// working for this repo (operator view, admin token required).
func newProjectDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [repo]",
		Short: "Report webhook-ingestion health per mapped repo",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			repo := ""
			if len(args) == 1 {
				repo = args[0]
			}
			resp, raw, err := c.ReposDoctor(cmd.Context(), repo)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			for _, r := range resp.Repos {
				app := "unchecked (no GitHub App configured)"
				if r.AppInstalled != nil {
					if *r.AppInstalled {
						app = "installed"
					} else {
						app = "NOT INSTALLED (" + r.AppError + ")"
					}
				}
				last := "never"
				if r.LastEventAt != nil {
					last = r.LastEventAt.Format(time.RFC3339)
				}
				cmd.Printf("%s (project %s)\n", r.Repo, r.Project)
				cmd.Printf("  app:        %s\n", app)
				cmd.Printf("  last event: %s (types: %s)\n", last, strings.Join(r.EventTypes, ", "))
				cmd.Printf("  unapplied:  %d\n", r.UnappliedEvents)
				if r.Stale {
					cmd.Printf("  STALE: no delivery since mapping — run `lode reconcile --repo %s`\n", r.Repo)
				}
			}
			for _, u := range resp.UnmappedSenders {
				cmd.Printf("unmapped sender: %s (%d events, last %s)\n",
					u.Repo, u.Events, u.LastEventAt.Format(time.RFC3339))
			}
			return nil
		},
	}
}
```

- [ ] **Step 6: Write a CLI wiring test**

Append to `internal/cmd/project_test.go`:

```go
func TestProjectDoctorRendersReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/doctor" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"repos": [{
				"repo": "acme/app", "project": "demo",
				"app_installed": null,
				"mapped_at": "2026-07-30T00:00:00Z",
				"last_event_at": null, "event_types": [],
				"unapplied_events": 3, "stale": true
			}],
			"unmapped_senders": [{"repo": "acme/unmapped", "events": 2, "last_event_at": "2026-07-29T00:00:00Z"}]
		}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")

	out, err := runLode(t, "project", "doctor")
	if err != nil {
		t.Fatalf("project doctor: %v\n%s", err, out)
	}
	for _, want := range []string{"acme/app", "STALE", "acme/unmapped"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	out, err = runLode(t, "project", "doctor", "--json")
	if err != nil {
		t.Fatalf("project doctor --json: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"stale": true`) && !strings.Contains(out, `"stale":true`) {
		t.Fatalf("--json output does not round-trip stale:\n%s", out)
	}
}
```

(with `"io"`, `"net/http"`, `"net/http/httptest"`, `"strings"` in that test
file's imports as needed).

- [ ] **Step 7: Run everything touched**

Run: `go test ./internal/api/... ./internal/cli/... ./internal/cmd/...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/api internal/cli/client.go internal/cmd/project.go internal/cmd/project_test.go
git commit -m "Add lode project doctor over GET /api/v1/repos/doctor"
```

---

## Task 9: POST /api/v1/reconcile (engine 1) and lode reconcile

The endpoint ships now with the replay engine; Task 13 (part 3) adds polling
to the same handler. The response shape is designed for both from the start.

**Files:**
- Modify: `internal/api/reconcile.go` (handler + `parseSince`)
- Modify: `internal/api/server.go` (route)
- Modify: `internal/cli/client.go` (`Reconcile`)
- Create: `internal/cmd/reconcile.go`
- Test: `internal/api/reconcile_test.go` (append), `internal/cmd/reconcile_test.go`

- [ ] **Step 1: Write the failing API test**

Append to `internal/api/reconcile_test.go`:

```go
func TestReconcileReplaysIgnoredEvents(t *testing.T) {
	st, h, token := newTestServer(t)
	// Delivery recorded before mapping...
	seedGitHubEvent(t, st, "d-1", "issues.opened.ignored", `{
		"action": "opened",
		"repository": {"full_name": "acme/app"},
		"issue": {"number": 7, "title": "late", "state": "open", "html_url": "u"}
	}`)
	// ...then the repo is mapped.
	mapRepo(t, h, token, "demo", "WL", "acme/app")

	rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", token, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		RunID  string `json:"run_id"`
		DryRun bool   `json:"dry_run"`
		Replay struct {
			Candidates int `json:"candidates"`
			Replayed   int `json:"replayed"`
		} `json:"replay"`
		PollSkipped string `json:"poll_skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RunID == "" || resp.Replay.Replayed != 1 {
		t.Fatalf("response = %+v; want a run id and 1 replayed", resp)
	}
	if resp.PollSkipped == "" {
		t.Fatalf("poll_skipped empty; want the no-github-app explanation")
	}
}

func TestReconcileValidation(t *testing.T) {
	_, h, token := newTestServer(t)
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"repo and task together", map[string]any{"repo": "a/b", "task": "WL-1"}, http.StatusUnprocessableEntity},
		{"bad since", map[string]any{"since": "yesterday-ish"}, http.StatusUnprocessableEntity},
		{"duration since", map[string]any{"since": "720h", "dry_run": true}, http.StatusOK},
		{"rfc3339 since", map[string]any{"since": "2026-07-01T00:00:00Z", "dry_run": true}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", token, tc.body); rec.Code != tc.want {
				t.Fatalf("%s: %d %s; want %d", tc.name, rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

func TestReconcileRequiresAdmin(t *testing.T) {
	st, h, token := newTestServer(t)
	nonAdmin := makeNonAdminToken(t, st, h, token)
	if rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", nonAdmin, map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d; want 403", rec.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestReconcile`
Expected: FAIL — 404, route unregistered.

- [ ] **Step 3: Write the handler**

Append to `internal/api/reconcile.go` (imports gain
`"github.com/sunstoneinstitute/worklode/internal/hooks"`):

```go
// reconcileRequest is the body of POST /api/v1/reconcile. Repo and Task are
// mutually exclusive bounds; Since accepts RFC 3339 or a Go duration,
// resolved against the server clock.
type reconcileRequest struct {
	Repo   string `json:"repo,omitempty"`
	Task   string `json:"task,omitempty"`
	Since  string `json:"since,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// reconcileResponse is one run's report, one section per engine. Poll is
// null when polling did not run; PollSkipped says why.
type reconcileResponse struct {
	RunID       string              `json:"run_id"`
	DryRun      bool                `json:"dry_run"`
	Replay      *hooks.ReplayResult `json:"replay"`
	Poll        any                 `json:"poll"` // *reconcile.PollResult once Task 13 (part 3) lands
	PollSkipped string              `json:"poll_skipped,omitempty"`
}

// parseSince resolves a --since value against now: an RFC 3339 timestamp is
// taken as-is; a Go duration ("720h") means now minus that duration.
func parseSince(s string, now time.Time) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		return &u, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, fmt.Errorf("since %q is neither RFC 3339 nor a Go duration", s)
	}
	u := now.Add(-d).UTC()
	return &u, nil
}

// reconcile handles POST /api/v1/reconcile: engine 1 (replay stored events)
// then engine 2 (poll GitHub — part 3 of this series; skipped until the
// App is configured). Synchronous by design: a scoped run is fast and the
// unscoped run is the scheduled case where waiting is acceptable (spec 013
// §API).
func (s *server) reconcile(w http.ResponseWriter, r *http.Request) {
	var req reconcileRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Repo != "" && req.Task != "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo and task are mutually exclusive")
		return
	}
	since, err := parseSince(req.Since, s.st.Now())
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	runID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	resp := reconcileResponse{RunID: runID, DryRun: req.DryRun}

	// Engine 1. --task cannot bound replay (an ignored event's task binding
	// is unknown before its apply runs), so a task-scoped run goes straight
	// to polling.
	if req.Task == "" {
		replay, err := hooks.Replay(r.Context(), s.st,
			hooks.ReplayOptions{Repo: req.Repo, Since: since, DryRun: req.DryRun})
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		resp.Replay = replay
	}

	// Engine 2 lands in part 3 of this series (Task 13 replaces this
	// line with the reconcile.Poll call).
	resp.PollSkipped = "github app auth not configured"

	writeJSON(w, http.StatusOK, resp)
}
```

In `internal/api/server.go`, next to the whoami route:

```go
	mux.Handle("POST /api/v1/reconcile", s.auth(requireAdmin(s.reconcile)))
```

- [ ] **Step 4: Run the API tests**

Run: `go test ./internal/api/ -run TestReconcile -v`
Expected: PASS

- [ ] **Step 5: Add the client method and CLI command**

In `internal/cli/client.go`:

```go
// ReconcileInput is the request body of POST /api/v1/reconcile.
type ReconcileInput struct {
	Repo   string `json:"repo,omitempty"`
	Task   string `json:"task,omitempty"`
	Since  string `json:"since,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// Reconcile calls POST /api/v1/reconcile and returns the raw run report;
// the CLI renders it. Admin-only on the server; synchronous.
func (c *Client) Reconcile(ctx context.Context, in ReconcileInput) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/reconcile", in)
}
```

`internal/cmd/reconcile.go`:

```go
// lode reconcile: repair task and spec activity the ingestion path missed
// (spec 013). Operator command; the server does the work.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newReconcileCmd() *cobra.Command {
	var repo, task, since string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Repair what webhook ingestion missed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if repo != "" && task != "" {
				return fmt.Errorf("--repo and --task are mutually exclusive")
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.Reconcile(cmd.Context(), cli.ReconcileInput{
				Repo: repo, Task: task, Since: since, DryRun: dryRun,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			var resp struct {
				RunID  string `json:"run_id"`
				DryRun bool   `json:"dry_run"`
				Replay *struct {
					Candidates    int      `json:"candidates"`
					Replayed      int      `json:"replayed"`
					StillUnmapped int      `json:"still_unmapped"`
					Errors        []string `json:"errors"`
				} `json:"replay"`
				Poll        json.RawMessage `json:"poll"`
				PollSkipped string          `json:"poll_skipped"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("decode reconcile report: %w", err)
			}
			verb := "repaired"
			if resp.DryRun {
				verb = "would repair"
			}
			cmd.Printf("run %s\n", resp.RunID)
			if resp.Replay != nil {
				cmd.Printf("replay: %s %d of %d candidate event(s), %d still unmapped\n",
					verb, resp.Replay.Replayed, resp.Replay.Candidates, resp.Replay.StillUnmapped)
				for _, e := range resp.Replay.Errors {
					cmd.Printf("  error: %s\n", e)
				}
			}
			switch {
			case resp.PollSkipped != "":
				cmd.Printf("poll: skipped (%s)\n", resp.PollSkipped)
			case len(resp.Poll) > 0 && string(resp.Poll) != "null":
				cmd.Printf("poll: %s\n", string(resp.Poll))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "bound the run to one repo (owner/name)")
	cmd.Flags().StringVar(&task, "task", "", "bound the run to one task id")
	cmd.Flags().StringVar(&since, "since", "", "RFC 3339 time or Go duration (e.g. 720h), against the server clock")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report repairs without writing")
	return cmd
}

func init() {
	rootCmd.AddCommand(newReconcileCmd())
}
```

- [ ] **Step 6: Write the CLI wiring test**

`internal/cmd/reconcile_test.go`:

```go
package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReconcileFlagWiring(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/reconcile" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"run_id":"r1","dry_run":true,
			"replay":{"candidates":2,"replayed":2,"still_unmapped":0},
			"poll":null,"poll_skipped":"github app auth not configured"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")

	out, err := runLode(t, "reconcile", "--repo", "acme/app", "--since", "720h", "--dry-run")
	if err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out)
	}
	if gotBody != `{"repo":"acme/app","since":"720h","dry_run":true}`+"\n" &&
		gotBody != `{"repo":"acme/app","since":"720h","dry_run":true}` {
		t.Fatalf("request body = %s; want the three flags and nothing else", gotBody)
	}
	for _, want := range []string{"would repair 2", "skipped (github app auth not configured)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestReconcileRejectsRepoAndTask(t *testing.T) {
	if out, err := runLode(t, "reconcile", "--repo", "a/b", "--task", "WL-1"); err == nil {
		t.Fatalf("reconcile accepted --repo with --task:\n%s", out)
	}
}
```

- [ ] **Step 7: Run everything touched**

Run: `go test ./internal/api/... ./internal/cli/... ./internal/cmd/...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/api internal/cli/client.go internal/cmd/reconcile.go internal/cmd/reconcile_test.go
git commit -m "Add POST /api/v1/reconcile and lode reconcile (replay engine)"
```

---

## Acceptance criteria → tasks

| Spec acceptance criterion | Covered by |
|---|---|
| `project doctor`: no App installation / last webhook predates mapping / unmapped senders | Task 8 (`app_installed`+`app_error`, `stale`, `unmapped_senders`) |
| `lode doctor` exits non-zero and names the fix for each failure class | Task 6 table test |
| Deterministic `--json` on every command | root `--json` + sorted store queries (`ORDER BY` in every reconcile query) |
