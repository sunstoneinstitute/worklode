# Prometheus Domain Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add domain metrics (leases, sweeper, skill sync, webhooks, embeddings, DB pool) to the worklode server per `docs/specs/022-prometheus-metrics.md`.

**Architecture:** The `prometheus.Registry` moves from inside `api.NewServer` to `internal/cmd/serve.go`, which passes it down: to `api.NewServer` via a new `Config.Metrics` field, to `store.Open` via a new functional option, and to the hooks/embed packages via their constructors. Each package owns a package-private, nil-safe metrics struct next to the code it measures. All new metrics are prefixed `worklode_`; the existing `http_*` pair keeps its names.

**Tech Stack:** Go 1.26, `github.com/prometheus/client_golang` v1.24.1 (already a dependency — `prometheus`, `collectors`, `promhttp`, and `testutil` packages). Postgres-backed tests via `store.OpenTestStore` (needs a reachable pgvector Postgres; `docker compose up -d postgres` locally).

**Verification note:** Store/api/hooks tests skip silently without Postgres unless `CI=1`. Run test commands with a reachable Postgres and confirm tests actually ran (`ok`, not `SKIP` in verbose output).

---

### Task 1: Store metrics scaffolding (options, instruments, lease collector)

**Files:**
- Create: `internal/store/metrics.go`
- Create: `internal/store/metrics_test.go`
- Modify: `internal/store/store.go` (Store struct ~line 22, Open ~line 31)
- Modify: `internal/store/testhelpers.go` (`OpenTestStore` ~line 34, `openTestDB` ~line 107)

- [ ] **Step 1: Write the failing test**

Create `internal/store/metrics_test.go` (package `store` — store tests are in-package):

```go
package store

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestWithMetricsRegisters asserts WithMetrics registers the store's
// instruments: the DB pool collector and the active-lease collector.
func TestWithMetricsRegisters(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := OpenTestStore(t, WithMetrics(reg))
	if s.metrics == nil {
		t.Fatal("WithMetrics did not set s.metrics")
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var names []string
	for _, f := range families {
		names = append(names, f.GetName())
	}
	joined := strings.Join(names, "\n")
	for _, want := range []string{"go_sql_open_connections", "worklode_leases_active"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("registry missing %s; got:\n%s", want, joined)
		}
	}
}

// TestClaimOutcomeMapping asserts the sentinel-error → label mapping.
func TestClaimOutcomeMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "ok"},
		{ErrLeased, "leased"},
		{ErrBlocked, "blocked"},
		{ErrNotFound, "not_found"},
		{ErrBadTransition, "error"},
	} {
		if got := claimOutcome(tc.err); got != tc.want {
			t.Fatalf("claimOutcome(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// TestStoreMetricsNilSafe asserts a store opened without WithMetrics (nil
// storeMetrics) records nothing and does not panic.
func TestStoreMetricsNilSafe(t *testing.T) {
	var m *storeMetrics
	m.claim("claim", "ok")
	m.renew("ok")
	m.release("ok")
	m.expire(3)
}

// keep testutil imported before Task 2 fills in behavioral assertions
var _ = testutil.ToFloat64
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store -run 'TestWithMetricsRegisters|TestClaimOutcomeMapping|TestStoreMetricsNilSafe' -v`
Expected: FAIL to compile — `undefined: WithMetrics`, `undefined: claimOutcome`, `undefined: storeMetrics`.

- [ ] **Step 3: Write the implementation**

Create `internal/store/metrics.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Option configures Open.
type Option func(*Store)

// WithMetrics registers the store's Prometheus instruments on reg: claim and
// lease counters, the active-lease gauge, and database/sql pool stats.
// Without this option the store records nothing.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(s *Store) {
		s.metrics = newStoreMetrics(reg)
		reg.MustRegister(collectors.NewDBStatsCollector(s.db, "worklode"))
		reg.MustRegister(&leaseCollector{db: s.db})
	}
}

// storeMetrics holds the store's domain instruments. All methods are nil-safe
// so call sites need no guards on stores opened without WithMetrics.
type storeMetrics struct {
	claims   *prometheus.CounterVec
	renewals *prometheus.CounterVec
	releases *prometheus.CounterVec
	expiries prometheus.Counter
}

func newStoreMetrics(reg prometheus.Registerer) *storeMetrics {
	m := &storeMetrics{
		claims: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_claims_total",
			Help: "Claim attempts by op and outcome. claim-next's internal claim attempts also count under op=claim.",
		}, []string{"op", "outcome"}),
		renewals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_lease_renewals_total",
			Help: "Lease renewals by outcome.",
		}, []string{"outcome"}),
		releases: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_lease_releases_total",
			Help: "Lease releases by outcome.",
		}, []string{"outcome"}),
		expiries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "worklode_lease_expiries_total",
			Help: "Leases closed by the expiry sweeper.",
		}),
	}
	reg.MustRegister(m.claims, m.renewals, m.releases, m.expiries)
	return m
}

func (m *storeMetrics) claim(op, outcome string) {
	if m == nil {
		return
	}
	m.claims.WithLabelValues(op, outcome).Inc()
}

func (m *storeMetrics) renew(outcome string) {
	if m == nil {
		return
	}
	m.renewals.WithLabelValues(outcome).Inc()
}

func (m *storeMetrics) release(outcome string) {
	if m == nil {
		return
	}
	m.releases.WithLabelValues(outcome).Inc()
}

func (m *storeMetrics) expire(n int) {
	if m == nil || n <= 0 {
		return
	}
	m.expiries.Add(float64(n))
}

// claimOutcome maps a Claim error to its metric label. Everything outside the
// spec'd sentinel set (including ErrBadTransition) is "error".
func claimOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrLeased):
		return "leased"
	case errors.Is(err, ErrBlocked):
		return "blocked"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	default:
		return "error"
	}
}

// outcome maps any error to ok/error, for renewals and releases.
func outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

var leasesActiveDesc = prometheus.NewDesc(
	"worklode_leases_active",
	"Active (unreleased, unexpired) leases, counted at scrape time.",
	nil, nil)

// leaseCollector counts active leases at scrape time. On query failure it
// emits an invalid metric (surfacing a scrape error) rather than a stale zero.
type leaseCollector struct {
	db *sql.DB
}

func (c *leaseCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- leasesActiveDesc
}

func (c *leaseCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n float64
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM leases WHERE released_at IS NULL AND expires_at > now()`,
	).Scan(&n)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(leasesActiveDesc, err)
		return
	}
	ch <- prometheus.MustNewConstMetric(leasesActiveDesc, prometheus.GaugeValue, n)
}
```

In `internal/store/store.go`, add the field to the Store struct:

```go
// Store wraps the Postgres connection pool.
type Store struct {
	db      *sql.DB
	dsn     string
	nowFn   func() time.Time
	metrics *storeMetrics
}
```

and make Open variadic (replace the existing `Open`):

```go
// Open opens a Postgres-backed store for the given postgres:// DSN. Callers
// are responsible for applying migrations (see Migrate) before relying on
// the schema being present — Open does not do this implicitly.
func Open(dsn string, opts ...Option) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	st := &Store{db: db, dsn: dsn, nowFn: func() time.Time { return time.Now().UTC() }}
	for _, o := range opts {
		o(st)
	}
	return st, nil
}
```

In `internal/store/testhelpers.go`, thread options through `OpenTestStore` and `openTestDB` (leave `OpenUnmigratedTestStore` untouched):

```go
func OpenTestStore(t *testing.T, opts ...Option) *Store {
```
…and change its final line to `return openTestDB(t, dbName, opts...)`.

```go
func openTestDB(t *testing.T, dbName string, opts ...Option) *Store {
```
…and change its `Open` call to `s, err := Open(u.String(), opts...)`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store -run 'TestWithMetricsRegisters|TestClaimOutcomeMapping|TestStoreMetricsNilSafe' -v`
Expected: PASS (3 tests). Then `go build ./...` — everything still compiles.

- [ ] **Step 5: Commit**

```bash
git add internal/store/metrics.go internal/store/metrics_test.go internal/store/store.go internal/store/testhelpers.go
git commit -m "store: add metrics option, instruments, and active-lease collector"
```

---

### Task 2: Instrument lease operations

**Files:**
- Modify: `internal/store/leases.go` (`Claim` ~line 130, `Renew` ~line 233, `Release` ~line 276, `ExpireLeases` ~line 411)
- Modify: `internal/store/ranking.go` (`ClaimNext` ~line 242)
- Modify: `internal/store/metrics_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/metrics_test.go` (the file's `var _ = testutil.ToFloat64` line can now be deleted; helpers `openLeaseStore` and `createTask`/`defaultTaskInput` come from `leases_test.go`/the task test fixtures, same package):

```go
// TestLeaseMetricsCounters drives claim/renew/release/expire through a store
// with metrics attached and asserts the counters.
func TestLeaseMetricsCounters(t *testing.T) {
	s, now := openLeaseStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	// Claim ok, then a second claim → leased.
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-a", 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-b", 0); !errors.Is(err, ErrLeased) {
		t.Fatalf("second claim err = %v, want ErrLeased", err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim", "ok")); got != 1 {
		t.Fatalf("claims{claim,ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim", "leased")); got != 1 {
		t.Fatalf("claims{claim,leased} = %v, want 1", got)
	}

	// Active-lease collector sees the one live lease.
	if got := testutil.ToFloat64(&leaseCollector{db: s.db}); got != 1 {
		t.Fatalf("worklode_leases_active = %v, want 1", got)
	}

	// Renew by a non-holder → error; by the holder → ok.
	if _, err := s.Renew(ctx, task.ID, "nobody", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renew by non-holder err = %v, want ErrNotFound", err)
	}
	if _, err := s.Renew(ctx, task.ID, "stig", 0); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.renewals.WithLabelValues("error")); got != 1 {
		t.Fatalf("renewals{error} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.renewals.WithLabelValues("ok")); got != 1 {
		t.Fatalf("renewals{ok} = %v, want 1", got)
	}

	// Release ok.
	if err := s.Release(ctx, task.ID, "stig"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := testutil.ToFloat64(s.metrics.releases.WithLabelValues("ok")); got != 1 {
		t.Fatalf("releases{ok} = %v, want 1", got)
	}

	// Re-claim, then expire it: expiries counts 1.
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-a", time.Second); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	*now = now.Add(time.Hour)
	n, err := s.ExpireLeases(ctx, *now)
	if err != nil || n != 1 {
		t.Fatalf("ExpireLeases = (%d, %v), want (1, nil)", n, err)
	}
	if got := testutil.ToFloat64(s.metrics.expiries); got != 1 {
		t.Fatalf("expiries = %v, want 1", got)
	}
}

// TestClaimNextMetrics: an empty ready set records claim_next/none; a
// successful pickup records claim_next/ok (plus its internal claim/ok).
func TestClaimNextMetrics(t *testing.T) {
	s, _ := openLeaseStore(t)
	reg := prometheus.NewRegistry()
	s.metrics = newStoreMetrics(reg)
	ctx := t.Context()

	res, err := s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "host:/wt-x"})
	if err != nil || res.Claimed {
		t.Fatalf("ClaimNext on empty set = (%+v, %v), want unclaimed, nil", res, err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim_next", "none")); got != 1 {
		t.Fatalf("claims{claim_next,none} = %v, want 1", got)
	}

	createTask(t, s, leaseTestNow, defaultTaskInput())
	res, err = s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "host:/wt-x"})
	if err != nil || !res.Claimed {
		t.Fatalf("ClaimNext = (%+v, %v), want claimed, nil", res, err)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim_next", "ok")); got != 1 {
		t.Fatalf("claims{claim_next,ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(s.metrics.claims.WithLabelValues("claim", "ok")); got != 1 {
		t.Fatalf("claims{claim,ok} = %v, want 1", got)
	}
}
```

Add the missing imports to the test file's import block: `"errors"`, `"time"`.

Note: `openLeaseStore` returns `(*Store, *time.Time)`; moving the clock is `*now = now.Add(...)`. If `defaultTaskInput`/`createTask` have different names in the current fixtures, read `internal/store/leases_test.go` and use the helper that `TestClaimExactlyOnce` (leases_test.go:54) uses to create a ready task — do not write a new seeding helper.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store -run 'TestLeaseMetricsCounters|TestClaimNextMetrics' -v`
Expected: FAIL — counters stay 0 (e.g. `claims{claim,ok} = 0, want 1`). If the failure is a compile error about fixture helper names, fix the test to use the actual helpers per the note above.

- [ ] **Step 3: Add the instrumentation**

`internal/store/leases.go` — `Claim`: replace the final block

```go
	if err != nil {
		return nil, err
	}
	return lease, nil
```

with:

```go
	s.metrics.claim("claim", claimOutcome(err))
	if err != nil {
		return nil, err
	}
	return lease, nil
```

(The `randomExternalID` early-return above it stays uncounted; a crypto/rand failure is not a claim outcome.)

`Renew` — same shape, replace its final block with:

```go
	s.metrics.renew(outcome(err))
	if err != nil {
		return nil, err
	}
	return lease, nil
```

`Release` — its final statement is `return err` after `RecordEvent`; replace with:

```go
	s.metrics.release(outcome(err))
	return err
```

(Also leave `Release`'s own `randomExternalID` early return uncounted, same as Claim.)

`ExpireLeases` — right after the advisory-lock acquisition succeeds (after the `defer conn.ExecContext(...)` unlock statement, ~line 437), add:

```go
	count := 0
	defer func() { s.metrics.expire(count) }()
```

and delete the existing `count := 0` declaration further down (~line 464). The deferred call records whatever was expired even on a mid-loop error return.

`internal/store/ranking.go` — `ClaimNext`:
- The empty-candidates return becomes:

```go
	if len(candidates) == 0 {
		s.metrics.claim("claim_next", "none")
		return &ClaimNextResult{Claimed: false}, nil
	}
```

- The `DryRun` return is left unrecorded (a read, not a claim attempt).
- The successful return inside the loop becomes:

```go
		task := t
		s.metrics.claim("claim_next", "ok")
		return &ClaimNextResult{Claimed: true, Task: &task, FanOut: fanOut[t.ID], Lease: lease}, nil
```

- The loop's hard-error return becomes:

```go
			return nil, err
```
→
```go
			s.metrics.claim("claim_next", "error")
			return nil, err
```

- The final exhausted-loop return becomes:

```go
	s.metrics.claim("claim_next", "none")
	return &ClaimNextResult{Claimed: false}, nil
```

(The three pre-ranking error returns — `readyCandidates`, `BlockingFanOut`, `projectFocusMap` — stay unrecorded: they are read failures before any claim attempt.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store -v`
Expected: PASS, including all pre-existing lease/ranking tests.

- [ ] **Step 5: Commit**

```bash
git add internal/store/leases.go internal/store/ranking.go internal/store/metrics_test.go
git commit -m "store: count claims, renewals, releases, and expiries"
```

---

### Task 3: api.Config.Metrics — registry injection into NewServer

**Files:**
- Modify: `internal/api/server.go` (Config ~line 39, server struct ~line 125, NewServer ~line 198, registry block ~line 305)
- Modify: `internal/api/server_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/server_test.go`:

```go
// TestMetricsEndpointDomainFamilies wires a shared registry through both the
// store and the server, the way serve.go does, and asserts the domain
// families appear on the admin /metrics alongside the HTTP ones.
func TestMetricsEndpointDomainFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	st := store.OpenTestStore(t, store.WithMetrics(reg))
	main, admin, err := api.NewServer(st, api.Config{Metrics: reg})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	doReq(t, main, "GET", "/api/v1/tasks", "", nil)

	rr := doReq(t, admin, "GET", "/metrics", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"http_requests_total",
		"worklode_leases_active",
		"go_sql_open_connections",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %s", want)
		}
	}
}
```

Add `"github.com/prometheus/client_golang/prometheus"` to the file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api -run TestMetricsEndpointDomainFamilies -v`
Expected: FAIL to compile — `unknown field Metrics in struct literal of type api.Config`.

- [ ] **Step 3: Implement Config.Metrics**

In `internal/api/server.go`:

1. Add to `Config` (after `BackgroundCtx`):

```go
	// Metrics is the registry the server registers its instruments on and
	// serves at /metrics on the admin handler. Nil (tests) gets a private
	// empty registry; serve.go passes the process-wide one, which also
	// carries the Go/process collectors and the store's instruments.
	Metrics *prometheus.Registry
```

2. In `NewServer`, right after the `s.bgCtx` defaulting (~line 209), resolve the registry early (later tasks hang embed/hooks instruments off it before line 305):

```go
	reg := cfg.Metrics
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
```

3. Replace the existing registry block (~lines 305–306):

```go
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
```

with nothing (the `s.requests = ...` assignment now follows directly; `reg` already exists). Remove the now-unused `collectors` import.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api -run 'TestMetrics|TestHealthz' -v && go build ./...`
Expected: PASS — the new test, plus `TestMetricsEndpoint` and `TestHealthzAndMetricsNotOnPublicHandler` unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/server_test.go
git commit -m "api: accept an injected metrics registry via Config"
```

---

### Task 4: serve.go composition root + sweeper counter

**Files:**
- Modify: `internal/cmd/serve.go` (RunE ~line 52, sweeper goroutine ~line 100)

No new automated test: the sweeper loop is an inline goroutine with no seam, and `cmd` has no serve tests. Verification is compile + vet + the full suite (the wiring itself is covered by Task 3's test, which mirrors it).

- [ ] **Step 1: Wire the registry**

In `internal/cmd/serve.go` RunE, before `store.Open`:

```go
			reg := prometheus.NewRegistry()
			reg.MustRegister(collectors.NewGoCollector())
			reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
```

Change the open to `st, err := store.Open(dsn, store.WithMetrics(reg))` and add `Metrics: reg,` to the `api.Config` literal.

Add imports:

```go
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
```

- [ ] **Step 2: Count sweeper runs**

Before the sweeper goroutine:

```go
			sweeperRuns := prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "worklode_lease_sweeper_runs_total",
				Help: "Lease sweeper runs by result.",
			}, []string{"result"})
			reg.MustRegister(sweeperRuns)
```

and inside the ticker case, replace:

```go
						if n, err := st.ExpireLeases(ctx, time.Now().UTC()); err != nil {
							slog.Error("expire leases", "err", err)
						} else if n > 0 {
							slog.Info("expired leases", "count", n)
						}
```

with:

```go
						if n, err := st.ExpireLeases(ctx, time.Now().UTC()); err != nil {
							sweeperRuns.WithLabelValues("error").Inc()
							slog.Error("expire leases", "err", err)
						} else {
							sweeperRuns.WithLabelValues("ok").Inc()
							if n > 0 {
								slog.Info("expired leases", "count", n)
							}
						}
```

- [ ] **Step 3: Verify**

Run: `go build ./... && go vet ./internal/cmd/`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/serve.go
git commit -m "serve: own the metrics registry; count sweeper runs"
```

---

### Task 5: Skill sync metrics

**Files:**
- Modify: `internal/api/server.go` (server struct ~line 168, NewServer metric creation, `syncOnce` ~line 457)
- Create: `internal/api/metrics_internal_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/metrics_internal_test.go` — **package `api`** (white-box; the rest of the api tests are external, this one deliberately is not, so it can drive `observeSkillSync` without a full sync harness):

```go
package api

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/skillsync"
)

func TestObserveSkillSync(t *testing.T) {
	s := &server{}
	s.initMetrics(prometheus.NewRegistry())

	s.observeSkillSync(skillsync.Summary{Synced: 3, Changed: 1, Embedded: 1}, nil, 250*time.Millisecond)
	s.observeSkillSync(skillsync.Summary{Synced: 2}, errors.New("boom"), time.Second)

	for _, tc := range []struct {
		result string
		want   float64
	}{{"ok", 1}, {"error", 1}} {
		if got := testutil.ToFloat64(s.syncRuns.WithLabelValues(tc.result)); got != tc.want {
			t.Fatalf("syncRuns{%s} = %v, want %v", tc.result, got, tc.want)
		}
	}
	for _, tc := range []struct {
		action string
		want   float64
	}{{"synced", 5}, {"changed", 1}, {"embedded", 1}} {
		if got := testutil.ToFloat64(s.syncItems.WithLabelValues(tc.action)); got != tc.want {
			t.Fatalf("syncItems{%s} = %v, want %v", tc.action, got, tc.want)
		}
	}
	// The duration series exists (CollectAndCount counts series, not
	// observations; the error pass observing too is covered by observeSkillSync
	// recording duration before branching on err).
	if n := testutil.CollectAndCount(s.syncDuration, "worklode_skill_sync_duration_seconds"); n != 1 {
		t.Fatalf("syncDuration series = %d, want 1", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api -run TestObserveSkillSync -v`
Expected: FAIL to compile — `undefined: initMetrics / observeSkillSync / syncRuns`.

- [ ] **Step 3: Implement**

In `internal/api/server.go`:

1. Add fields to the `server` struct next to `requests`/`durations`:

```go
	syncRuns     *prometheus.CounterVec
	syncDuration prometheus.Histogram
	syncItems    *prometheus.CounterVec
```

2. Factor instrument creation into a method and call it from `NewServer` where `s.requests = ...` currently sits (the http metric literals move in; nothing else changes):

```go
// initMetrics creates and registers the server-owned instruments (HTTP
// middleware and skill sync) on reg.
func (s *server) initMetrics(reg prometheus.Registerer) {
	s.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests served, by method, route pattern, and status code.",
	}, []string{"method", "route", "code"})
	s.durations = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration, by method and route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	s.syncRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_skill_sync_runs_total",
		Help: "Skill sync passes by result.",
	}, []string{"result"})
	s.syncDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "worklode_skill_sync_duration_seconds",
		Help:    "Skill sync pass duration.",
		Buckets: []float64{0.1, 0.5, 1, 5, 15, 60, 300},
	})
	s.syncItems = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_skill_sync_items_total",
		Help: "Skills touched by sync passes, by action.",
	}, []string{"action"})
	reg.MustRegister(s.requests, s.durations, s.syncRuns, s.syncDuration, s.syncItems)
}
```

In `NewServer`, the old `s.requests = ... / s.durations = ... / reg.MustRegister(...)` block becomes `s.initMetrics(reg)`.

3. Instrument `syncOnce` (replace the function):

```go
// syncOnce runs a single bounded sync pass, records its metrics, and logs
// its outcome.
func (s *server) syncOnce(ctx context.Context, reason string) {
	ctx, cancel := context.WithTimeout(ctx, skillSyncTimeout)
	defer cancel()
	start := time.Now()
	sum, err := s.skillSyncer.SyncAll(ctx, s.skillSources)
	s.observeSkillSync(sum, err, time.Since(start))
	if err != nil {
		// Error, matching the HTTP path: a background failure has no caller
		// watching a response, so the log is the only signal there is.
		s.log.Error("skill sync failed", "reason", reason, "err", err)
		return
	}
	s.log.Info("skill sync", "reason", reason, "synced", sum.Synced,
		"changed", sum.Changed, "embedded", sum.Embedded, "deleted", sum.Deleted)
}

// observeSkillSync records one sync pass. A partial failure still carries a
// summary of what landed before the error, so items are recorded on both
// paths (spec 022 §4).
func (s *server) observeSkillSync(sum skillsync.Summary, err error, d time.Duration) {
	s.syncDuration.Observe(d.Seconds())
	result := "ok"
	if err != nil {
		result = "error"
	}
	s.syncRuns.WithLabelValues(result).Inc()
	for action, n := range map[string]int{
		"synced":   sum.Synced,
		"changed":  sum.Changed,
		"embedded": sum.Embedded,
		"deleted":  sum.Deleted,
	} {
		if n > 0 {
			s.syncItems.WithLabelValues(action).Add(float64(n))
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api -run 'TestObserveSkillSync|TestMetrics' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/metrics_internal_test.go
git commit -m "api: record skill sync runs, duration, and item counts"
```

---

### Task 6: Webhook metrics

**Files:**
- Create: `internal/hooks/metrics.go`
- Create: `internal/hooks/metrics_test.go`
- Modify: `internal/hooks/github.go` (struct ~line 29, `NewGitHubHandler` ~line 47, `ServeHTTP` ~line 92)
- Modify: `internal/hooks/flux.go` (struct ~line 39, `NewFluxHandler` ~line 51, `ServeHTTP` ~line 73)
- Modify: `internal/api/server.go` (handler construction ~lines 344–345)
- Modify (call sites, add trailing `nil` arg): `internal/hooks/github_test.go:47,220`, `internal/hooks/skillpush_test.go:27`, `internal/hooks/flux_test.go:43,185,446,474`

- [ ] **Step 1: Write the failing test**

Create `internal/hooks/metrics_test.go` (package `hooks_test`, like the other hooks tests; `newEnv`, `deliverBody`, `sign`, `fixture`, `testSecret` already exist there):

```go
package hooks_test

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestGitHubWebhookMetrics(t *testing.T) {
	st := store.OpenTestStore(t)
	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	h := hooks.NewGitHubHandler(st, testSecret, nil, nil, m)

	// Unmapped repo → ignored (no project mapping exists in this store).
	body := []byte(`{"action":"opened","repository":{"full_name":"acme/unmapped"}}`)
	rr := deliverBody(t, h, "issues", "d-1", body)
	if rr.Code != 200 {
		t.Fatalf("delivery status = %d, want 200", rr.Code)
	}

	// Bad signature → rejected.
	req := httptest.NewRequest("POST", "/hooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "d-2")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("bad-signature status = %d, want 401", rr.Code)
	}

	// Unknown event name lands in the "other" bucket (still recorded ok/ignored).
	rr = deliverBody(t, h, "watch", "d-3", body)
	if rr.Code != 200 {
		t.Fatalf("unknown-event status = %d, want 200", rr.Code)
	}

	for _, tc := range []struct {
		event, result string
		want          float64
	}{
		{"issues", "ignored", 1},
		{"issues", "rejected", 1},
		{"other", "ignored", 1},
	} {
		got := testutil.ToFloat64(m.Events().WithLabelValues("github", tc.event, tc.result))
		if got != tc.want {
			t.Fatalf("events{github,%s,%s} = %v, want %v", tc.event, tc.result, got, tc.want)
		}
	}
}

func TestFluxWebhookMetrics(t *testing.T) {
	st := store.OpenTestStore(t)
	reg := prometheus.NewRegistry()
	m := hooks.NewMetrics(reg)
	h := hooks.NewFluxHandler(st, fluxTestSecret, nil, nil, m)

	// Ignored kind.
	body := []byte(`{"involvedObject":{"kind":"GitRepository","name":"x"},"reason":"Ready"}`)
	req := httptest.NewRequest("POST", "/hooks/flux", bytes.NewReader(body))
	req.Header.Set("X-Signature", signFlux(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("flux status = %d, want 200", rr.Code)
	}
	if got := testutil.ToFloat64(m.Events().WithLabelValues("flux", "flux", "ignored")); got != 1 {
		t.Fatalf("events{flux,flux,ignored} = %v, want 1", got)
	}
}
```

Note: the flux tests sign with the flux secret — find the existing constant and signing helper in `internal/hooks/flux_test.go` (the secret is `fluxTestSecret`; if the signing helper there has a different name than `signFlux`, use that name, or inline the HMAC the way `sign` in github_test.go does but with `fluxTestSecret`).

`m.Events()` is a test-only accessor added in Step 3 — external-package tests can't reach the private field.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hooks -run 'TestGitHubWebhookMetrics|TestFluxWebhookMetrics' -v`
Expected: FAIL to compile — `undefined: hooks.NewMetrics`, wrong arg counts.

- [ ] **Step 3: Implement**

Create `internal/hooks/metrics.go`:

```go
package hooks

import (
	"slices"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the webhook instruments, shared by the GitHub and Flux
// handlers. A nil *Metrics records nothing, so tests can pass nil.
type Metrics struct {
	events *prometheus.CounterVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{events: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_webhook_events_total",
		Help: "Webhook deliveries by source, event type, and result.",
	}, []string{"source", "event", "result"})}
	reg.MustRegister(m.events)
	return m
}

// Events exposes the counter for test assertions.
func (m *Metrics) Events() *prometheus.CounterVec {
	return m.events
}

func (m *Metrics) event(source, event, result string) {
	if m == nil {
		return
	}
	m.events.WithLabelValues(source, event, result).Inc()
}

// eventLabel bounds the metric's event label to the handled GitHub events;
// anything else (including an empty header) is "other".
func eventLabel(event string) string {
	if slices.Contains(handledEvents, event) {
		return event
	}
	return "other"
}
```

`internal/hooks/github.go`:

1. Add `metrics *Metrics` to `githubHandler`; add the parameter `m *Metrics` to `NewGitHubHandler` (after `onSkillPush`) and store it.
2. At the top of `ServeHTTP` (before the secret check), add:

```go
	result := "error"
	defer func() {
		h.metrics.event("github", eventLabel(r.Header.Get("X-GitHub-Event")), result)
	}()
```

3. In the signature-failure branch, set `result = "rejected"` before the `writeErr` line.
4. In the final response switch, set the result before writing: `case !inserted:` and `case skillPush:` and `default:` each get `result = "ok"`; `case ignored:` gets `result = "ignored"`. (Every other early return keeps the `"error"` default, including the empty-secret 503.)

`internal/hooks/flux.go`: same pattern —

1. Add `metrics *Metrics` to `fluxHandler`; add `m *Metrics` parameter to `NewFluxHandler` (after `log`) and store it.
2. Top of `ServeHTTP`:

```go
	result := "error"
	defer func() { h.metrics.event("flux", "flux", result) }()
```

3. Signature failure → `result = "rejected"`; final switch → `"ok"` / `"ignored"` / `"ok"` mirroring github.go. Note flux.go's `ignored` is a local variable in scope at the switch — set `result = "ignored"` in its case.

`internal/api/server.go` (~lines 337–345): create the shared metrics before the handler registrations and pass it:

```go
	hookMetrics := hooks.NewMetrics(reg)
	...
	mux.Handle("POST /hooks/github", hooks.NewGitHubHandler(st, cfg.GitHubWebhookSecret, s.log, onSkillPush, hookMetrics))
	mux.Handle("POST /hooks/flux", hooks.NewFluxHandler(st, cfg.FluxWebhookSecret, cfg.ClusterEnvMap, s.log, hookMetrics))
```

Update the six existing test call sites listed under **Files** by appending `, nil` as the new final argument.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/hooks -v && go build ./...`
Expected: PASS, including all pre-existing webhook tests.

- [ ] **Step 5: Commit**

```bash
git add internal/hooks/ internal/api/server.go
git commit -m "hooks: count webhook deliveries by source, event, and result"
```

---

### Task 7: Embedding call metrics

**Files:**
- Modify: `internal/embed/embed.go` (`OpenAI` struct ~line 73, `Embed` ~line 104)
- Create: `internal/embed/metrics.go`
- Modify: `internal/embed/embed_test.go`
- Modify: `internal/api/server.go` (embedder construction ~line 276)

- [ ] **Step 1: Write the failing test**

Append to `internal/embed/embed_test.go` (check its package clause first and match it; the test below assumes external `embed_test` with the package imported as `embed` — adjust the qualifier if the file is in-package):

```go
func TestEmbedMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2]}]}`))
	}))
	defer srv.Close()

	reg := prometheus.NewRegistry()
	m := embed.NewMetrics(reg)
	p := &embed.OpenAI{URL: srv.URL, Model: "test-model", Metrics: m}

	if _, err := p.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	// Second call against a closed server → error.
	srv.Close()
	if _, err := p.Embed(context.Background(), []string{"hello"}); err == nil {
		t.Fatal("embed against closed server: want error")
	}
	// Empty input makes no HTTP call and records nothing.
	if _, err := p.Embed(context.Background(), nil); err != nil {
		t.Fatalf("embed empty: %v", err)
	}

	if got := testutil.ToFloat64(m.Requests().WithLabelValues("ok")); got != 1 {
		t.Fatalf("requests{ok} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.Requests().WithLabelValues("error")); got != 1 {
		t.Fatalf("requests{error} = %v, want 1", got)
	}
}
```

Add the needed imports (`prometheus`, `testutil`) to the test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/embed -run TestEmbedMetrics -v`
Expected: FAIL to compile — `undefined: embed.NewMetrics`, `unknown field Metrics`.

- [ ] **Step 3: Implement**

Create `internal/embed/metrics.go`:

```go
package embed

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics instruments outbound embedding calls. A nil *Metrics records
// nothing, so bare OpenAI literals (tests, CLI paths) stay uninstrumented.
type Metrics struct {
	requests *prometheus.CounterVec
	duration prometheus.Histogram
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "worklode_embed_requests_total",
			Help: "Outbound embedding API calls by result. One per Embed call, however many texts it batches.",
		}, []string{"result"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "worklode_embed_request_duration_seconds",
			Help:    "Outbound embedding API call duration.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

// Requests exposes the counter for test assertions.
func (m *Metrics) Requests() *prometheus.CounterVec {
	return m.requests
}

func (m *Metrics) observe(err error, d time.Duration) {
	if m == nil {
		return
	}
	m.duration.Observe(d.Seconds())
	result := "ok"
	if err != nil {
		result = "error"
	}
	m.requests.WithLabelValues(result).Inc()
}
```

In `internal/embed/embed.go`:

1. Add `Metrics *Metrics` to the `OpenAI` struct (below `HTTPClient`).
2. Rename the existing `Embed` body to a private method and wrap it:

```go
func (p *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	start := time.Now()
	vecs, err := p.embed(ctx, texts)
	p.Metrics.observe(err, time.Since(start))
	return vecs, err
}

func (p *OpenAI) embed(ctx context.Context, texts []string) ([][]float32, error) {
	// ...former Embed body from the json.Marshal line down, unchanged,
	// with its own len(texts)==0 early return removed (now in the wrapper).
}
```

In `internal/api/server.go`, the embedder construction (~line 276) becomes:

```go
		s.embedder = &embed.OpenAI{URL: cfg.EmbeddingURL, Model: cfg.EmbeddingModel, Key: cfg.EmbeddingAPIKey, Metrics: embed.NewMetrics(reg)}
```

(`reg` is in scope there since Task 3 moved its resolution to the top of `NewServer`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/embed -v && go build ./...`
Expected: PASS, including the pre-existing `TestOpenAIEmbed*` tests.

- [ ] **Step 5: Commit**

```bash
git add internal/embed/ internal/api/server.go
git commit -m "embed: time and count outbound embedding calls"
```

---

### Task 8: CLAUDE.md maintenance rule + full verification

**Files:**
- Modify: `CLAUDE.md` (add a section after `## Conventions`)
- Modify: `docs/specs/022-prometheus-metrics.md` (status line)

- [ ] **Step 1: Add the Metrics section to CLAUDE.md**

Append after the `## Conventions` section:

```markdown
## Metrics

Server-side changes that add an HTTP endpoint, background loop, outbound
call, or store operation with meaningful outcomes must add or extend
`worklode_*` Prometheus metrics in the owning package, with tests. Follow the
conventions in `docs/specs/022-prometheus-metrics.md`: package-private
nil-safe metrics struct, `prometheus.Registerer` threaded from `serve.go`,
bounded label values, `worklode_` prefix.
```

- [ ] **Step 2: Mark the spec implemented**

In `docs/specs/022-prometheus-metrics.md`, change `**Status:** draft` to `**Status:** implemented`.

- [ ] **Step 3: Full verification**

Run (with Postgres up: `docker compose up -d postgres`):

```bash
gofmt -l . && go vet ./... && go test ./...
```

Expected: no gofmt output, vet clean, all packages `ok` (store/api/hooks tests must not SKIP — check that Postgres was reachable).

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md docs/specs/022-prometheus-metrics.md
git commit -m "Require metrics maintenance for server changes (spec 022)"
```
