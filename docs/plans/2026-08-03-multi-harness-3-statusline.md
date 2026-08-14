---
status: accepted
covers:
  - docs/specs/008-worklode-plugin.md#sec-17.5
requires:
  - 2026-08-03-multi-harness-1-adapter-core.md
---
# Multi-harness 3/3: status line and cost spool — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 3 of 3 — see `2026-08-03-multi-harness-1-adapter-core.md`
for the series map. This part holds 5 tasks (task numbers restart at 1 per
plan file; the cross-part dependency is the `requires:` frontmatter edge on
part 1, which must be merged first — the reshaped `lode install` report and
the `internal/harness` claude-code adapter). It is independent of part 2.

**Goal:** `lode statusline` renders the current task, lease state, heartbeat
freshness and the harness's own cost/context numbers from purely local
reads, and the cost it spools reaches `agent_sessions` on the next heartbeat
flush (spec 024 acceptance 5) — no second network path.

**Architecture:** The hot-path constraint is the whole design (spec §3.5):
the status line re-runs per assistant message and may not call the server.
Two small files in the worktree-private git dir — where the session marker
already lives — carry everything it needs: a **lease marker**
(`worklode-lease.json`: task id, title, lease expiry) written by
`internal/hookrun` whenever it holds fresh lease facts (session-start's
brief, pre-commit's renew), and a **cost spool** (`worklode-cost.json`:
cumulative `total_cost_usd` per session id) appended by `lode statusline`
from the payload. The existing heartbeat path flushes the spool: the touch
call gains an optional harness-reported cost, which the store applies only
to an open session's `cost_amount` — 012's transcript pricing overwrites it
at session end and stays authoritative, with the spool as the live
approximation in between. Token columns are deliberately not approximated:
the payload's context numbers are window occupancy, not billed tokens.
`lode install --statusline` sets `statusLine.command` only when the key is
absent — a status line is a personal choice Worklode must not replace.

**Tech Stack:** Go 1.26, cobra, stdlib `testing`; Postgres (pgvector image)
for the store/API tests; Prometheus client for the one new counter.

**Spec:** `docs/specs/008-worklode-plugin.md` §3.5, §4, Q024.5.

---

## What exists vs. what this builds

- Session-local state: `internal/hookrun/hookrun.go` — the session marker
  (`worklode-session.json`, helpers at `hookrun.go:842-965`) in the
  worktree-private git dir (`worktree.GitDir`). The lease marker and spool
  are siblings using the same read/write shape.
- Heartbeat path: `reportSession` (`hookrun.go:245-262`) →
  `cli.Client.TouchAgentSession` (`internal/cli/client.go:708`) →
  `POST /api/v1/tasks/{id}/agent-session`
  (`internal/api/agentsessions.go:57`, route `server.go:376`) →
  `store.TouchAgentSession` (`internal/store/agent_sessions.go:135`).
- Session-level cost columns: `agent_sessions.input_tokens/output_tokens/
  cost_amount` (`0004_agent_sessions.up.sql`), today written only by
  `EndAgentSession`'s transcript-derived roll-up
  (`agent_sessions.go:326-345`); `costAmountRE` guards the decimal string
  (`agent_sessions.go:47`). **No migration needed** — the flush reuses
  `cost_amount`.
- Store metrics: nil-safe `storeMetrics` in `internal/store/metrics.go`
  (`worklode_claims_total` et al.) — the pattern the new counter follows
  (spec 022; CLAUDE.md's metrics rule applies because Task 3 extends a
  store operation).
- Brief/lease data at session start: `handleSessionStart` already fetches
  the brief (`hookrun.go:353-361`) — title and lease expiry are in hand
  exactly when the marker must be written.
- `docs/follow-ups.md` "Cost is lost when a session never ends cleanly":
  Tasks 3–4 narrow this — harness-reported session-level cost now lands
  continuously — but the per-day `agent_session_usage` buckets still only
  arrive at session end; the follow-up stays open for the bucket half.

**Plan-level decisions (deliberate, under the spec's open questions):**

1. **Q024.5 (worktree binding):** the status line derives the task from
   `cwd` alone — `worktree.Root` + `worktree.ParseDir`, the same guard the
   hooks use — and ignores the payload's `worktree.*`/`workspace.git_worktree`
   block entirely, so the documented absence of `worktree.branch` for
   hook-created worktrees is moot. Two local `git rev-parse` execs per
   render is the cost; the no-network constraint is what §3.5 fixes, and
   `git rev-parse` is single-digit milliseconds.
2. **The payload struct in Task 2 encodes spec §2.4's field names**
   (`session_id`, `cost.total_cost_usd`, `context_window.*`, `model.*`,
   `workspace.*`). Task 2 starts by re-verifying them against the Claude
   Code status-line docs (spec §8); unknown fields decode to zero and only
   ever cost a blank segment — the render never fails the harness
   (spec §4 row 7).
3. **Only cost flows to the backbone, not tokens.** `agent_sessions`'
   token columns mean billed tokens (012); the payload carries window
   occupancy. Approximating one with the other would corrupt the roll-up.
4. **The flush sends the spool's cumulative figure on every debounced
   heartbeat and never deletes the spool**; the spool is removed alongside
   the session marker on session-end/worktree-exit. Re-sending the same
   cumulative number is idempotent by construction (COALESCE-overwrite),
   which kills every append/truncate race for free.

**Out of scope:** OTLP ingest and `--telemetry` (spec §3.6, v2); status
lines for other harnesses (pi's `setStatus` etc. — nothing in v1); any
change to transcript pricing (012 stays authoritative).

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/hookrun/statusline.go` (new) | payload struct, lease marker + cost spool read/write, `Statusline()` renderer |
| `internal/hookrun/statusline_test.go` (new) | render matrix, spool write, no-network guarantee |
| `internal/hookrun/hookrun.go` | write lease marker (session-start, pre-commit renew); flush spool in `reportSession`; remove spool with the session marker |
| `internal/hookrun/hookrun_test.go` | lease-marker + flush assertions |
| `internal/store/agent_sessions.go` | `TouchAgentSession` harness-cost parameter |
| `internal/store/agent_sessions_test.go` | open-session apply, end-overwrite, closed-session no-op |
| `internal/store/metrics.go` | `worklode_harness_cost_reports_total` |
| `internal/store/metrics_test.go` | counter + nil-safety |
| `internal/api/agentsessions.go` | `harness_cost_usd` on the touch request |
| `internal/api/agentsessions_test.go` | wire test |
| `internal/cli/client.go` | `TouchAgentSessionInput` with the optional cost |
| `internal/cli/client_test.go` | round-trip |
| `internal/cmd/statusline.go` (new) | the `lode statusline` cobra command |
| `internal/harness/claudecode.go` | `InstallStatusline`/`UninstallStatusline` |
| `internal/harness/claudecode_test.go` | absent-key-only rule, ours-only removal |
| `internal/cmd/install.go` | `--statusline` flag + report stanza |
| `internal/cmd/install_test.go` | flag test |

**Test commands:** `go test ./internal/hookrun/ ./internal/cmd/
./internal/harness/ ./internal/cli/` (no Postgres);
`go test ./internal/store/ ./internal/api/` (Postgres with pgvector).
Commit after every task, imperative mood, no trailers.

---

## Tasks

### Task 1 — Record the lease marker

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Modify: `internal/hookrun/hookrun.go`, `internal/hookrun/hookrun_test.go`
- Create: `internal/hookrun/statusline.go` (marker half only)

- [ ] **Step 1: Write the failing test**

Append to `internal/hookrun/hookrun_test.go`, inside the existing
session-start fixture (fake client returning a brief with a lease):

```go
	// Session-start leaves a lease marker next to the session marker: the
	// status line's only source of task title and lease expiry, because it
	// may not call the server (spec 008 §17.5).
	lm, ok := readLeaseMarker(root)
	if !ok || lm.TaskID != taskID || lm.Title != "the fixture title" {
		t.Fatalf("lease marker = %+v, %v", lm, ok)
	}
	if lm.ExpiresAt == "" {
		t.Fatal("lease marker missing expiry")
	}
```

And a pre-commit case: after a `handlePreCommit` whose fake `RenewLease`
returns a lease expiring at a known instant, `readLeaseMarker(root).ExpiresAt`
carries that instant (RFC3339). A backbone failure leaves any existing
marker untouched.

Run: `go test ./internal/hookrun/ -run SessionStart` — FAIL.

- [ ] **Step 2: Implement**

In `internal/hookrun/statusline.go`:

```go
// leaseMarkerFile sits beside the session marker in the worktree-private
// git dir: the status line's no-network source of task and lease facts.
const leaseMarkerFile = "worklode-lease.json"

type leaseMarker struct {
	TaskID    string `json:"task_id"`
	Title     string `json:"title"`
	ExpiresAt string `json:"expires_at,omitempty"` // RFC3339
}

func leaseMarkerPath(root string) (string, error) // mirrors markerPath
func readLeaseMarker(root string) (leaseMarker, bool)
func writeLeaseMarker(root string, m leaseMarker) error
```

(read/write bodies mirror `readSessionMarker`/`writeMarker`,
`hookrun.go:866-893`.)

In `hookrun.go`:

- `handleSessionStart`, after the brief fetch succeeds (`hookrun.go:361`):

```go
	lm := leaseMarker{TaskID: taskID, Title: brief.Task.Title}
	if brief.Lease != nil {
		lm.ExpiresAt = brief.Lease.ExpiresAt.Format(time.RFC3339)
	}
	if err := writeLeaseMarker(root, lm); err != nil {
		warn(opts, "write lease marker: %v", err)
	}
```

- `handlePreCommit` (`hookrun.go:540`): capture the renew result —
  `lease, _, err := c.RenewLease(...)` — and on success update just the
  expiry, preserving a title the marker already has:

```go
	if m, ok := readLeaseMarker(root); ok {
		m.ExpiresAt = lease.ExpiresAt.Format(time.RFC3339)
		if werr := writeLeaseMarker(root, m); werr != nil {
			warn(opts, "write lease marker: %v", werr)
		}
	}
```

- `ensureLease`'s renew branch (`hookrun.go:439`) likewise captures the
  returned lease and updates the marker's expiry.

The marker is deliberately **not** removed on session end — the lease
outlives sessions, and a stale expiry renders as `lease expired`, which is
true.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/hookrun/ -count=1
git add internal/hookrun
git commit -m "Record a lease marker for the status line"
```

---

### Task 2 — Render `lode statusline` and spool the cost

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Modify: `internal/hookrun/statusline.go`, `internal/hookrun/hookrun.go` (spool removal on session end)
- Create: `internal/hookrun/statusline_test.go`, `internal/cmd/statusline.go`

- [ ] **Step 1: Re-verify the payload field names**

Check the Claude Code status-line docs (spec 008 §22): the stdin JSON's
`session_id`, `model.id`/`model.display_name`, `workspace.current_dir`,
`cwd`, `cost.total_cost_usd`, and the `context_window` block's field names.
Correct the struct below to the documented names before writing tests.

- [ ] **Step 2: Write the failing tests**

`internal/hookrun/statusline_test.go`:

```go
func TestStatuslineInWorktree(t *testing.T) {
	// Fixture: a git repo with a wt/WL-7-fix worktree, a lease marker
	// {WL-7, "Fix the flaky test", now+40m}, a session marker with
	// LastHeartbeatAt now-2m, payload naming the worktree as cwd.
	payload := `{"session_id":"s1","model":{"display_name":"Opus"},` +
		`"workspace":{"current_dir":"` + wt + `"},` +
		`"cost":{"total_cost_usd":1.2345},` +
		`"context_window":{"used_tokens":76000,"max_tokens":200000}}`
	var out bytes.Buffer
	if err := Statusline(strings.NewReader(payload), &out, fixedNow); err != nil {
		t.Fatalf("statusline: %v", err)
	}
	line := out.String()
	for _, want := range []string{"WL-7", "Fix the flaky test", "lease 40m", "♥ 2m", "$1.23", "38%"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("want exactly one line, got %q", line)
	}
	// The render spooled the cost for the flush:
	sp, ok := readCostSpool(wt)
	if !ok || sp["s1"].CostUSD != 1.2345 {
		t.Fatalf("spool = %+v, %v", sp, ok)
	}
}

func TestStatuslineDegradations(t *testing.T) {
	// - expired lease marker  -> "lease expired", still renders
	// - no lease marker       -> task id from ParseDir only, no title
	// - outside any worktree  -> model/cost/context only, no task segment
	// - unwritable git dir    -> line still prints, spool silently absent
	//   (spec 008 §18 row 8: never fail the harness's render)
	// - empty stdin           -> prints an empty-but-newline-terminated
	//   line, exit nil
}

func TestStatuslineMakesNoNetworkCall(t *testing.T) {
	// Acceptance 5's "no network" is testable: point LODE_SERVER at a
	// httptest.Server whose handler t.Errorf's on any request, run
	// Statusline, assert zero requests.
}
```

Run — FAIL (`Statusline`, `readCostSpool` undefined).

- [ ] **Step 3: Implement**

`internal/hookrun/statusline.go` gains:

```go
// costSpoolFile carries the harness's own cumulative accounting per
// session, appended by `lode statusline` on every render and read by the
// heartbeat flush (spec 008 §17.5). Cumulative, so re-reporting is
// idempotent; removed with the session marker.
const costSpoolFile = "worklode-cost.json"

type costSpoolEntry struct {
	CostUSD   float64 `json:"cost_usd"`
	UpdatedAt string  `json:"updated_at"`
}

func readCostSpool(root string) (map[string]costSpoolEntry, bool)
func writeCostSpool(root string, m map[string]costSpoolEntry) error

// statusPayload is the subset of Claude Code's status-line stdin JSON the
// renderer reads (spec 008 §16.4). Missing fields are blank segments.
type statusPayload struct {
	SessionID string `json:"session_id"`
	Model     struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	Cwd  string `json:"cwd"`
	Cost struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow struct {
		UsedTokens int64 `json:"used_tokens"`
		MaxTokens  int64 `json:"max_tokens"`
	} `json:"context_window"`
}

// Statusline renders one line from the payload on r plus the local
// markers. Pure local reads — the hot-path constraint IS the design
// (spec 008 §17.5): it re-runs per assistant message and must never call
// the server or fail the render.
func Statusline(r io.Reader, w io.Writer, now func() time.Time) error {
	raw, _ := io.ReadAll(r)
	var p statusPayload
	_ = json.Unmarshal(raw, &p)

	dir := p.Workspace.CurrentDir
	if dir == "" {
		dir = p.Cwd
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}

	var segs []string
	if root, ok := worktree.Root(dir); ok {
		if taskID, ok := worktree.ParseDir(root); ok {
			task := taskID
			if lm, ok := readLeaseMarker(root); ok {
				if lm.Title != "" {
					task += " " + lm.Title
				}
				segs = append(segs, task, leaseSegment(lm, now()))
			} else {
				segs = append(segs, task)
			}
			if m, ok := readSessionMarker(root); ok {
				segs = append(segs, heartbeatSegment(m, now()))
			}
			spoolCost(root, p) // best-effort; render continues regardless
		}
	}
	if p.Cost.TotalCostUSD > 0 {
		segs = append(segs, fmt.Sprintf("$%.2f", p.Cost.TotalCostUSD))
	}
	if p.ContextWindow.MaxTokens > 0 {
		segs = append(segs, fmt.Sprintf("ctx %d%%",
			p.ContextWindow.UsedTokens*100/p.ContextWindow.MaxTokens))
	}
	fmt.Fprintln(w, strings.Join(segs, " · "))
	return nil
}
```

with the small helpers: `leaseSegment` (`lease 40m` /
`lease expired` / `lease —` when no expiry), `heartbeatSegment`
(`♥ 2m ago`, `♥ now` under a minute), and `spoolCost` (skip when
`p.SessionID == ""` or cost is zero; read-modify-write the map entry).

`internal/hookrun/hookrun.go`: `handleSessionEnd` (`hookrun.go:519`) and
`handleWorktreeExit` (`hookrun.go:714`) remove the spool where they remove
the session marker — add `removeCostSpool(root)` mirroring
`removeSessionMarker`.

`internal/cmd/statusline.go`:

```go
func newStatuslineCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Render a status line from Claude Code's stdin payload (task, lease, heartbeat, cost)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return hookrun.Statusline(cmd.InOrStdin(), cmd.OutOrStdout(), time.Now)
		},
	}
}

func init() { rootCmd.AddCommand(newStatuslineCmd()) }
```

- [ ] **Step 4: Verify and commit**

```bash
go test ./internal/hookrun/ ./internal/cmd/ -count=1
git add internal/hookrun internal/cmd
git commit -m "Render lode statusline from local markers and spool the cost"
```

---

### Task 3 — Apply harness-reported cost on touch (store and API)

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Modify: `internal/store/agent_sessions.go`, `internal/store/agent_sessions_test.go`, `internal/store/metrics.go`, `internal/store/metrics_test.go`, `internal/api/agentsessions.go`, `internal/api/agentsessions_test.go`

- [ ] **Step 1: Write the failing store test**

Append to `internal/store/agent_sessions_test.go` (reuse the file's
claim-then-touch fixture):

```go
func TestTouchAgentSessionAppliesHarnessCost(t *testing.T) {
	// ... claim fixture ...
	cost := "1.234500"
	sess, err := s.TouchAgentSession(ctx, taskID, actorID, "claude-code", "", "s1", &cost)
	if err != nil || sess.CostAmount == nil || *sess.CostAmount != "1.234500" {
		t.Fatalf("touch with cost: %+v %v", sess, err)
	}
	// nil leaves the stored value alone.
	sess, err = s.TouchAgentSession(ctx, taskID, actorID, "claude-code", "", "s1", nil)
	if err != nil || sess.CostAmount == nil {
		t.Fatalf("nil cost cleared the approximation: %+v %v", sess, err)
	}
	// The transcript-derived end overwrites: 012 stays authoritative.
	// ... EndAgentSession with usage whose rolled-up cost differs, then
	// AgentSession() and assert CostAmount is the end's figure ...
	// A touch after end must not resurrect the approximation:
	bigger := "9.000000"
	if _, err := s.TouchAgentSession(ctx, taskID, actorID, "claude-code", "", "s1", &bigger); err == nil {
		// (or however the closed-session touch surfaces — assert the
		// stored CostAmount is still the end's figure either way)
	}
	// Malformed decimal is ErrInvalidInput, mirroring EndAgentSession.
	bad := "1.2.3"
	if _, err := s.TouchAgentSession(ctx, taskID, actorID, "claude-code", "", "s2", &bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad cost: %v", err)
	}
}
```

Run: `go test ./internal/store/ -run TestTouchAgentSession` — FAIL
(compile: wrong arity).

- [ ] **Step 2: Implement the store change**

`internal/store/agent_sessions.go`:

1. `TouchAgentSession(ctx, taskID, actorID, agent, agentVersion, sessionID
   string, harnessCost *string)` — validate `harnessCost` against
   `costAmountRE` (`agent_sessions.go:47`) up front
   (`fmt.Errorf("harness cost %q: %w", *harnessCost, ErrInvalidInput)`).
2. Thread it into both write paths: the insert sets `cost_amount` when
   non-nil; `bumpAgentSessionLastSeen` (`agent_sessions.go:196`) gains the
   parameter and extends its UPDATE with
   `cost_amount = COALESCE($n, cost_amount)` **guarded by the existing
   `ended_at IS NULL` condition** — a closed session's transcript-priced
   figure is never touched (the doc comment on the touch/closed-session
   interaction at `agent_sessions.go:240-248` gains one sentence: the
   harness cost is a live approximation that end's transcript pricing
   overwrites; spec 008 §17.5).
3. Update the doc comment on the session-level columns' meaning where
   `EndAgentSession` writes them (`agent_sessions.go:326-345`): between
   flush and end, `cost_amount` may hold the harness's client-side figure.

`internal/store/metrics.go` — the metrics rule (CLAUDE.md, spec 022): this
extends a store operation with a meaningful new outcome, so count it:

```go
		harnessCost: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_harness_cost_reports_total",
			Help: "Harness-reported session cost figures applied on touch.",
		}),
```

added to `storeMetrics`, registered in `newStoreMetrics`, incremented in
`TouchAgentSession` when a cost was applied, with the nil-safe method shape
the file already uses. `internal/store/metrics_test.go`: extend the
existing counter test to claim+touch-with-cost and assert the counter
reads 1; assert a nil-metrics store does not panic (the file's existing
nil-safety pattern).

- [ ] **Step 3: Write the failing API test and implement**

Append to `internal/api/agentsessions_test.go` (existing
`newTestServer`/`doReq` fixtures): POST
`/api/v1/tasks/{id}/agent-session` with
`{"agent":"claude-code","session_id":"s1","harness_cost_usd":"1.234500"}` →
200 and the response's `cost_amount` echoing it; `"harness_cost_usd":"x"` →
422.

`internal/api/agentsessions.go`: `agentSessionRequest` gains
`HarnessCostUSD *string \`json:"harness_cost_usd"\`` and the handler passes
it through (`agentsessions.go:66`); `mapStoreErr` already turns
`ErrInvalidInput` into 422. No new endpoint, no new route.

- [ ] **Step 4: Verify and commit**

```bash
go test ./internal/store/ ./internal/api/ -count=1
git add internal/store internal/api
git commit -m "Apply harness-reported cost on agent-session touch"
```

---

### Task 4 — Flush the spool on the heartbeat (client)

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3]
```

**Files:**
- Modify: `internal/cli/client.go`, `internal/cli/client_test.go`, `internal/hookrun/hookrun.go`, `internal/hookrun/hookrun_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/cli/client_test.go` (the file's httptest round-trip pattern):
`TouchAgentSession` sends `harness_cost_usd` when set and omits the key
when nil.

`internal/hookrun/hookrun_test.go`: in the heartbeat fixture (fake server
capturing the touch body), pre-write a spool
`{"s1": {"cost_usd": 1.2345}}` in the worktree git dir and assert the
debounced heartbeat's touch request carries
`"harness_cost_usd":"1.234500"`; with no spool, the key is absent; a spool
entry for a *different* session id is not sent.

Run — FAIL.

- [ ] **Step 2: Implement**

`internal/cli/client.go`: change `TouchAgentSession` to take an input
struct (mirroring `EndAgentSessionInput`):

```go
// TouchAgentSessionInput identifies the session; HarnessCostUSD optionally
// carries the harness's own cumulative cost figure (spec 008 §17.5), a
// decimal string for the same numeric(12,6) round-trip reason as
// EndAgentSessionInput.CostAmount.
type TouchAgentSessionInput struct {
	Agent          string
	AgentVersion   string
	SessionID      string
	HarnessCostUSD *string
}

func (c *Client) TouchAgentSession(ctx context.Context, id string, in TouchAgentSessionInput) (AgentSession, []byte, error)
```

building the body map and setting `harness_cost_usd` only when non-nil.
Update the one production caller (`hookrun.go:251`) and any client tests.

`internal/hookrun/hookrun.go` — `reportSession` (`hookrun.go:245`) reads
the spool before touching:

```go
	in := cli.TouchAgentSessionInput{Agent: opts.agentName(), SessionID: sessionID}
	if sp, ok := readCostSpool(root); ok {
		if e, ok := sp[sessionID]; ok && e.CostUSD > 0 {
			cost := fmt.Sprintf("%.6f", e.CostUSD)
			in.HarnessCostUSD = &cost
		}
	}
	sess, _, err := c.TouchAgentSession(sctx, taskID, in)
```

Nothing else changes: the flush rides the existing debounce
(`heartbeatDebounce`, `hookrun.go:75`), the existing timeout, and the
existing warn-only failure contract — no second network path
(acceptance 5).

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/cli/ ./internal/hookrun/ -count=1
git add internal/cli internal/hookrun
git commit -m "Flush the harness cost spool on the heartbeat"
```

---

### Task 5 — Install the status line on demand

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:**
- Modify: `internal/harness/claudecode.go`, `internal/harness/claudecode_test.go`, `internal/cmd/install.go`, `internal/cmd/install_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/harness/claudecode_test.go`:

```go
func TestInstallStatuslineOnlyWhenAbsent(t *testing.T) {
	// Fresh settings file: InstallStatusline sets
	// settings["statusLine"] = {"type":"command","command":"lode statusline"}
	// and reports "installed". A file whose statusLine is someone else's
	// command is left byte-identical and reports "skipped" — a status line
	// is a personal choice (spec 008 §17.5). Re-run on ours: "unchanged".
}

func TestUninstallStatuslineRemovesOnlyOurs(t *testing.T) {
	// Ours -> key deleted, "removed". Foreign -> untouched, "none".
	// Absent file -> "none", no file created.
}
```

`internal/cmd/install_test.go`: `--statusline` produces a
`statusline` stanza in the JSON report; `--statusline` with an agent list
not containing claude-code reports it skipped (the surface is
Claude-Code-only in v1).

Run — FAIL.

- [ ] **Step 2: Implement**

`internal/harness/claudecode.go`:

```go
// StatuslineInstaller is the optional surface for harnesses with a
// configurable status line. Claude Code is the only v1 implementor; the
// installer type-asserts rather than widening Harness for one member.
type StatuslineInstaller interface {
	InstallStatusline(repoDir, scope string) (StatuslineResult, error)
	UninstallStatusline(repoDir, scope string) (StatuslineResult, error)
}

type StatuslineResult struct {
	Path   string `json:"path"`
	Action string `json:"action"` // installed | unchanged | skipped | removed | none
}
```

`ClaudeCode.InstallStatusline`: resolve the settings path (same scope
logic as `InstallHooks`), `readJSONFile`; if `settings["statusLine"]` is
absent → set

```go
	settings["statusLine"] = map[string]any{"type": "command", "command": "lode statusline"}
```

and write; if present with command `"lode statusline"` → `unchanged`, no
write; anything else → `skipped`, no write. `UninstallStatusline` deletes
the key only when the command is exactly ours.

`internal/cmd/install.go`: `addHookFlags` gains
`cmd.Flags().Bool("statusline", false, "install the Worklode status line
(harnesses that support one; opt-in, never replaces an existing status
line)")`. In `installHooks`/`uninstallHooks`, when set, loop the resolved
agents, type-assert `harness.StatuslineInstaller`, collect
`Statusline []statuslineReport` entries (`{Agent, Path, Action}`) into the
results — agents without the interface are reported
`{Agent, Action: "unsupported"}`. One text line each in
`reportInstall`/`reportUninstall`.

- [ ] **Step 3: Verify**

```bash
go test ./internal/harness/ ./internal/cmd/ -count=1
go test ./... -count=1
```

Manual end-to-end (acceptance 5): in a scratch repo,
`go run . install --statusline`, then
`echo '{"session_id":"s1","cost":{"total_cost_usd":0.5}}' | go run . statusline`
prints a line instantly with the network unplugged.

- [ ] **Step 4: Commit**

```bash
git add internal/harness internal/cmd
git commit -m "Install the Worklode status line on demand"
```

---

## Done when (part 3)

1. `lode statusline` prints task, lease state, heartbeat freshness and the
   payload's cost/context with zero network I/O, and never exits non-zero
   on bad input (acceptance 5, §4 rows 7–8).
2. The cost the harness reported reaches `agent_sessions.cost_amount` on
   the next debounced heartbeat, and the transcript-derived figure
   overwrites it at session end (acceptance 5; 012 authoritative).
3. `worklode_harness_cost_reports_total` counts applied reports and is
   nil-safe without `WithMetrics`.
4. `lode install --statusline` never replaces an existing `statusLine`
   key, and uninstall removes only ours.
5. `go test ./...` green, `./scripts/check-migrations.sh --no-fix` clean
   (no migration in this part).
