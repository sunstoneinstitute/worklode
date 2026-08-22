---
status: draft
covers: docs/specs/052-project-overhead-cost.md
---
# Project overhead cost — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop Worklode's agent-cost tracker from silently dropping every
token billed from a repository's main checkout — the dominant real usage
pattern (a long-running orchestrator session dispatching subagents into task
worktrees) — by reporting that usage against a new project-level overhead
bucket instead of gating it out.

**Architecture:** Every hook handler that already reports usage
(`heartbeat`, `session-end`, `worktree-enter`) keeps doing so exactly as
today for its own task, and additionally classifies every *other* working
directory its transcript touched: a directory that resolves to a task this
actor currently holds the lease on bills there through the existing
per-task path; everything else lands in a new `project_overhead_usage` /
`project_daily_overhead_cost` pair of tables, reported through a new
project-scoped endpoint. `Store.ProjectCost` folds both sources into one
combined report, breaking overhead's own share out separately so it is
visible, not merged away.

**Tech Stack:** Go, PostgreSQL (pgx/`database/sql`), golang-migrate, `templ`
for the cockpit UI.

## Global Constraints

- Never edit a shipped migration; this plan's migration is a new numbered
  pair, `deploy/base/migrations/0046_project_overhead_usage.{up,down}.sql`,
  also listed in `deploy/base/kustomization.yaml`.
- `go build`/`go test` must always run with `-trimpath` (use the `Makefile`
  targets — `make build`, `make test` — or add `-trimpath` by hand for a
  single package/test).
- Store tests need a reachable Postgres with pgvector; they skip silently
  without one unless `CI` is set. Assume one is reachable while executing
  this plan (`TEST_POSTGRES_DSN` or the default
  `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`).
- `internal/model` is a stdlib-only leaf (ADR 036) — no new import there
  beyond what it already has (`time`, `encoding/json` are fine; nothing from
  `internal/store`, `internal/api`, or `internal/cli`).
- `internal/ui` depends on nothing beyond stdlib, `internal/model`, and the
  templ runtime — never `internal/api` or `internal/store`.
- Every existing test in `internal/hookrun`, `internal/store`,
  `internal/transcript`, `internal/api`, `internal/cli`, and `internal/ui`
  must keep passing unmodified except where a task below names a specific
  existing test to change.
- Spec of record for this plan: `docs/specs/052-project-overhead-cost.md`
  (amends `docs/specs/012-agent-sessions.md` §1, §3, §4 — read both before
  starting any task).

---

## Tasks

### Task 1 — Migration: overhead usage and its daily rollup

```yaml
kind: chore
priority: high
skills:
  - worklode-migrations
blockedBy: [ ]
```

**Files:**
- Create: `deploy/base/migrations/0046_project_overhead_usage.up.sql`
- Create: `deploy/base/migrations/0046_project_overhead_usage.down.sql`
- Modify: `deploy/base/kustomization.yaml`

Add the two new tables from spec 052 §1, verbatim:

```sql
-- deploy/base/migrations/0046_project_overhead_usage.up.sql

-- One row per (project, agent, external session id, day, model, speed).
-- Mirrors agent_session_usage's shape, but keyed to a project directly:
-- overhead usage has no lease to hang off (a main-checkout session holds no
-- task's lease at report time), so there is no agent_sessions row for it.
--
-- Replaced wholesale per (project, agent, external session id), never
-- incremented -- same reason as agent_session_usage: the source transcript is
-- cumulative, so a report carries an absolute total that must overwrite a
-- prior one, not add to it.
CREATE TABLE project_overhead_usage (
    project_id            text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent                 text NOT NULL,
    external_session_id   text NOT NULL,
    usage_day             date NOT NULL,
    model                 text NOT NULL,
    speed                 text NOT NULL DEFAULT 'standard',
    input_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_5m_tokens bigint NOT NULL DEFAULT 0,
    cache_write_1h_tokens bigint NOT NULL DEFAULT 0,
    cache_read_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    -- NULL means "no price on file for this model on this day" -- see
    -- agent_session_usage.cost_amount's identical comment.
    cost_amount           numeric(14,6),
    cost_currency         text NOT NULL DEFAULT 'USD',
    PRIMARY KEY (project_id, agent, external_session_id, usage_day, model, speed),
    CONSTRAINT project_overhead_usage_speed_known CHECK (speed IN ('standard', 'fast')),
    CONSTRAINT project_overhead_usage_currency_format CHECK (cost_currency ~ '^[A-Z]{3}$'),
    CONSTRAINT project_overhead_usage_nonnegative CHECK (
        input_tokens >= 0 AND cache_write_5m_tokens >= 0 AND
        cache_write_1h_tokens >= 0 AND cache_read_tokens >= 0 AND
        output_tokens >= 0)
);

CREATE INDEX project_overhead_usage_day ON project_overhead_usage (usage_day);

-- Derived rollup, recomputed from scratch for the affected (project, day)
-- pairs whenever a (project, agent, session) overhead report is replaced.
-- Its own table rather than new columns on project_daily_cost: that table's
-- rows are, by construction, exactly what agent_session_usage sums up
-- through the lease -> task chain, and overhead has no task to join
-- through.
CREATE TABLE project_daily_overhead_cost (
    project_id            text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    usage_day             date NOT NULL,
    cost_currency         text NOT NULL DEFAULT 'USD',
    input_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_5m_tokens bigint NOT NULL DEFAULT 0,
    cache_write_1h_tokens bigint NOT NULL DEFAULT 0,
    cache_read_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    cost_amount           numeric(14,6) NOT NULL DEFAULT 0,
    unpriced_tokens       bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, usage_day, cost_currency),
    CONSTRAINT project_daily_overhead_cost_currency_format CHECK (cost_currency ~ '^[A-Z]{3}$')
);
```

```sql
-- deploy/base/migrations/0046_project_overhead_usage.down.sql
DROP TABLE project_daily_overhead_cost;
DROP TABLE project_overhead_usage;
```

Add both filenames to `deploy/base/kustomization.yaml`'s migration list, in
the same style as the `0045_doc_edges_defers` pair immediately above them
(`.up.sql` then `.down.sql`, each its own list entry).

- [ ] Write the two SQL files and the kustomization entry exactly as above.
- [ ] Run `./scripts/check-migrations.sh --no-fix` — expect no collision
      reported for `0046`.
- [ ] Apply the migration against the test database and confirm both tables
      exist:
      `psql "$TEST_POSTGRES_DSN" -c '\d project_overhead_usage' -c '\d project_daily_overhead_cost'`
      (or run `make test` once — `internal/store` tests apply migrations
      against a fresh database per test and will fail loudly if the SQL is
      invalid).
- [ ] Commit: `git add deploy/base/migrations/0046_project_overhead_usage.up.sql deploy/base/migrations/0046_project_overhead_usage.down.sql deploy/base/kustomization.yaml && git commit -m "Add project_overhead_usage and its daily rollup (spec 052 §1)"`

---

### Task 2 — `transcript.Bucket` carries its own working directory

```yaml
kind: feature
priority: high
skills: [ ]
blockedBy: [ ]
```

**Files:**
- Modify: `internal/transcript/transcript.go`
- Test: `internal/transcript/transcript_test.go`

**Interfaces:**
- Produces: `transcript.Bucket.Cwd string` — populated on every bucket
  `Parse`/`ParseFile` return, whether or not `Options.Root` is set.

Add a `Cwd string` field to `Bucket` (`internal/transcript/transcript.go`,
next to `Day`/`Model`/`Speed`), and add `cwd string` to the internal `key`
struct so bucketing groups by `(day, model, speed, cwd)` instead of
`(day, model, speed)`. The line that builds `k := key{...}` inside `Parse`
gains `cwd: e.Cwd` (raw, not normalized — the entries a caller receives back
already carry whatever `cwd` the transcript recorded, same as before
`Options.Root` filtering was ever added); the line that builds each returned
`Bucket` in the final loop gains `Cwd: k.cwd`. `sortBuckets` gains `Cwd` as a
tie-breaker after `Speed` so output order stays deterministic.

`Options.Root`'s filtering (the `inRoot` call) is unchanged — it still drops
an entry whose `cwd` is not under `Root` before the entry ever reaches the
bucketing map. This is intentionally backward compatible: every existing
caller passing `Options{Root: someWorktree}` gets the same total tokens,
just split into one bucket per distinct `cwd` under that root instead of one
merged bucket — harmless, since `internal/store`'s `mergeUsageBuckets`
already re-merges by `(day, model, speed)` server-side regardless of how
many rows the client sends.

- [ ] Write the failing test in `internal/transcript/transcript_test.go`:

```go
func TestParseGroupsByCwd(t *testing.T) {
	lines := []string{
		`{"type":"assistant","cwd":"/repo/.worktrees/WL-1-a","timestamp":"2026-08-01T10:00:00Z","message":{"id":"m1","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":10}}}`,
		`{"type":"assistant","cwd":"/repo","timestamp":"2026-08-01T10:01:00Z","message":{"id":"m2","model":"claude-sonnet-5","usage":{"input_tokens":200,"output_tokens":20}}}`,
	}
	buckets, err := Parse(strings.NewReader(strings.Join(lines, "\n")), Options{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("got %d buckets, want 2 (one per cwd): %+v", len(buckets), buckets)
	}
	byCwd := map[string]Bucket{}
	for _, b := range buckets {
		byCwd[b.Cwd] = b
	}
	if got := byCwd["/repo/.worktrees/WL-1-a"].Usage.Input; got != 100 {
		t.Errorf("worktree bucket input = %d, want 100", got)
	}
	if got := byCwd["/repo"].Usage.Input; got != 200 {
		t.Errorf("main-checkout bucket input = %d, want 200", got)
	}
}
```

- [ ] Run it: `go test -trimpath ./internal/transcript -run TestParseGroupsByCwd -v` — expect FAIL (no `Cwd` field yet, compile error).
- [ ] Add the `Cwd` field and the key/grouping/sort changes described above.
- [ ] Run it again — expect PASS.
- [ ] Run the whole package to confirm no existing test broke:
      `go test -trimpath ./internal/transcript -v`
- [ ] Commit: `git add internal/transcript/transcript.go internal/transcript/transcript_test.go && git commit -m "transcript: carry each bucket's own working directory"`

---

### Task 3 — Store: overhead usage, its rollup, and a combined ProjectCost

```yaml
kind: feature
priority: high
skills: [ ]
blockedBy: [1]
```

**Files:**
- Modify: `internal/store/session_usage.go`
- Test: `internal/store/session_usage_test.go`

**Interfaces:**
- Consumes: `usageColumns` (const, already in this file), `modelPriceFor(ctx,
  q, model, speed, day)`, `microAmount` (`internal/store/pricing.go`),
  `mergeUsageBuckets([]SessionUsageBucket) ([]SessionUsageBucket, error)`
  (already in this file, unchanged), `s.Tx(ctx, func(tx *sql.Tx) error)
  error` (`internal/store/store.go`), `s.GetProject(ctx, id) (*Project,
  error)` (`internal/store/projects.go`), `validAgent(agent string) bool`
  (`internal/store/agent_sessions.go`).
- Produces: `(*Store) ReportProjectOverheadUsage(ctx, projectID, agent,
  externalSessionID string, buckets []SessionUsageBucket) error`; extended
  `CostDay`/`CostTotal` (new `OverheadTokens TokenCounts`, `OverheadCost
  string`, `OverheadUnpricedTokens int64` fields); unchanged `(*Store)
  ProjectCost(ctx, projectID string, from, to time.Time) (*CostReport,
  error)` signature, changed body/SQL.

**Step 1 — extend the report shapes.** In `internal/store/session_usage.go`,
add three fields to both `CostDay` and `CostTotal`:

```go
type CostDay struct {
	Day      time.Time
	Currency string
	Tokens   TokenCounts
	Cost     string
	UnpricedTokens int64
	// OverheadTokens/OverheadCost/OverheadUnpricedTokens are the portion of
	// the totals above with no task to bill to (spec 052): a main-checkout
	// orchestration session, or a worktree this actor no longer held the
	// lease on when it reported. Always zero for a TaskCost report, which
	// has no overhead concept.
	OverheadTokens         TokenCounts
	OverheadCost           string
	OverheadUnpricedTokens int64
}
```

(Same three fields, same doc comment, on `CostTotal`.)

**Step 2 — the overhead write path.** Add, after `mergeUsageBuckets`:

```go
// replaceProjectOverheadUsageTx writes buckets as the complete overhead
// usage of one (project, agent, external session id), pricing each bucket
// against the rate in effect on its own day. Mirrors
// replaceSessionUsageTx's replace-not-accumulate contract and return shape,
// keyed by project instead of by an agent_sessions row -- overhead usage has
// no lease to hang an agent_sessions row off.
func replaceProjectOverheadUsageTx(ctx context.Context, tx *sql.Tx, projectID, agent, externalSessionID string, buckets []SessionUsageBucket) (
	totals TokenCounts, cost string, currency string, days []time.Time, err error) {

	daySet := map[time.Time]bool{}

	rows, err := tx.QueryContext(ctx,
		`DELETE FROM project_overhead_usage
		  WHERE project_id = $1 AND agent = $2 AND external_session_id = $3
		 RETURNING usage_day`, projectID, agent, externalSessionID)
	if err != nil {
		return totals, "", "", nil, fmt.Errorf("clear overhead usage for %s/%s/%s: %w", projectID, agent, externalSessionID, err)
	}
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			rows.Close()
			return totals, "", "", nil, fmt.Errorf("scan cleared overhead usage day: %w", err)
		}
		daySet[day.UTC()] = true
	}
	if err := rows.Err(); err != nil {
		return totals, "", "", nil, fmt.Errorf("clear overhead usage for %s/%s/%s: %w", projectID, agent, externalSessionID, err)
	}
	rows.Close()

	merged, err := mergeUsageBuckets(buckets)
	if err != nil {
		return totals, "", "", nil, err
	}

	byCurrency := map[string]*microAmount{}
	w := usageRowArrays{}
	for _, b := range merged {
		price, err := modelPriceFor(ctx, tx, b.Model, b.Speed, b.Day)
		if err != nil {
			return totals, "", "", nil, err
		}
		var amount *string
		rowCurrency := defaultCurrency
		if price != nil {
			a := price.Cost(b.Tokens)
			amount = &a
			rowCurrency = price.Currency
			acc, ok := byCurrency[rowCurrency]
			if !ok {
				acc = &microAmount{}
				byCurrency[rowCurrency] = acc
			}
			if err := acc.add(a); err != nil {
				return totals, "", "", nil, err
			}
		}
		w.add(b, amount, rowCurrency)
		totals.Add(b.Tokens)
		daySet[b.Day] = true
	}

	if len(w.days) > 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO project_overhead_usage
			   (project_id, agent, external_session_id, usage_day, model, speed, `+usageColumns+`,
			    cost_amount, cost_currency)
			 SELECT $1::text, $2::text, $3::text, u.usage_day, u.model, u.speed,
			        u.input_tokens, u.cache_write_5m_tokens, u.cache_write_1h_tokens,
			        u.cache_read_tokens, u.output_tokens,
			        u.cost_amount::numeric, u.cost_currency
			   FROM unnest($4::date[], $5::text[], $6::text[], $7::bigint[], $8::bigint[],
			               $9::bigint[], $10::bigint[], $11::bigint[], $12::text[], $13::text[])
			        AS u(usage_day, model, speed, input_tokens, cache_write_5m_tokens,
			             cache_write_1h_tokens, cache_read_tokens, output_tokens,
			             cost_amount, cost_currency)`,
			projectID, agent, externalSessionID,
			w.days, w.models, w.speeds, w.input, w.cacheWrite5m, w.cacheWrite1h,
			w.cacheRead, w.output, w.amounts, w.currencies,
		); err != nil {
			return totals, "", "", nil, fmt.Errorf("record overhead usage for %s/%s/%s: %w", projectID, agent, externalSessionID, err)
		}
	}

	if len(byCurrency) == 1 {
		for c, acc := range byCurrency {
			currency, cost = c, acc.String()
		}
	}

	days = make([]time.Time, 0, len(daySet))
	for d := range daySet {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	return totals, cost, currency, days, nil
}

// recomputeProjectOverheadDailyCostTx rebuilds project_daily_overhead_cost
// for projectID on the given days, from scratch -- mirrors
// recomputeProjectDailyCostTx, but its source (project_overhead_usage) is
// already keyed by project, so no lease/task join is needed.
func recomputeProjectOverheadDailyCostTx(ctx context.Context, tx *sql.Tx, projectID string, days []time.Time) error {
	if len(days) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM project_daily_overhead_cost WHERE project_id = $1 AND usage_day = ANY($2::date[])`,
		projectID, days); err != nil {
		return fmt.Errorf("clear overhead rollup for %s on %d day(s): %w", projectID, len(days), err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO project_daily_overhead_cost
		   (project_id, usage_day, cost_currency, `+usageColumns+`,
		    cost_amount, unpriced_tokens)
		 SELECT $1::text, u.usage_day, u.cost_currency,
		        SUM(u.input_tokens), SUM(u.cache_write_5m_tokens),
		        SUM(u.cache_write_1h_tokens), SUM(u.cache_read_tokens),
		        SUM(u.output_tokens),
		        SUM(COALESCE(u.cost_amount, 0)),
		        SUM(CASE WHEN u.cost_amount IS NULL
		                 THEN u.input_tokens + u.cache_write_5m_tokens +
		                      u.cache_write_1h_tokens + u.cache_read_tokens +
		                      u.output_tokens
		                 ELSE 0 END)
		   FROM project_overhead_usage u
		  WHERE u.project_id = $1 AND u.usage_day = ANY($2::date[])
		  GROUP BY u.usage_day, u.cost_currency`,
		projectID, days); err != nil {
		return fmt.Errorf("rebuild overhead rollup for %s on %d day(s): %w", projectID, len(days), err)
	}
	return nil
}

// ReportProjectOverheadUsage records tokens billed by agent/externalSessionID
// with no task to bill to (spec 052): a main-checkout orchestration session,
// or a worktree whose lease this actor no longer held at report time.
// Replace-not-accumulate per (project, agent, external session id) -- the
// client re-reports a running transcript total on every heartbeat.
// ErrInvalidInput for an unknown agent or an empty external session id;
// ErrNotFound for an unknown project.
func (s *Store) ReportProjectOverheadUsage(ctx context.Context, projectID, agent, externalSessionID string, buckets []SessionUsageBucket) error {
	if !validAgent(agent) {
		return fmt.Errorf("unknown agent %q: %w", agent, ErrInvalidInput)
	}
	if externalSessionID == "" {
		return fmt.Errorf("external session id is required: %w", ErrInvalidInput)
	}
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return err
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		_, _, _, days, err := replaceProjectOverheadUsageTx(ctx, tx, projectID, agent, externalSessionID, buckets)
		if err != nil {
			return err
		}
		if len(days) == 0 {
			return nil
		}
		return recomputeProjectOverheadDailyCostTx(ctx, tx, projectID, days)
	})
}
```

**Step 3 — combine `ProjectCost`.** Replace `(*Store) ProjectCost`'s body:
the `from`/`to` filters now apply inside two CTEs (task-attributed and
overhead), full-outer-joined on `(usage_day, cost_currency)` so a day with
only overhead usage still produces a row:

```go
func (s *Store) ProjectCost(ctx context.Context, projectID string, from, to time.Time) (*CostReport, error) {
	where := "project_id = $1"
	args := []any{projectID}
	if !from.IsZero() {
		args = append(args, from.UTC().Truncate(24*time.Hour))
		where += fmt.Sprintf(" AND usage_day >= $%d", len(args))
	}
	if !to.IsZero() {
		args = append(args, to.UTC().Truncate(24*time.Hour))
		where += fmt.Sprintf(" AND usage_day <= $%d", len(args))
	}

	rows, err := s.db.QueryContext(ctx,
		`WITH t AS (
		   SELECT usage_day, cost_currency, `+usageColumns+`, cost_amount, unpriced_tokens
		     FROM project_daily_cost WHERE `+where+`
		 ), o AS (
		   SELECT usage_day, cost_currency, `+usageColumns+`, cost_amount, unpriced_tokens
		     FROM project_daily_overhead_cost WHERE `+where+`
		 )
		 SELECT COALESCE(t.usage_day, o.usage_day),
		        COALESCE(t.cost_currency, o.cost_currency),
		        COALESCE(t.input_tokens,0) + COALESCE(o.input_tokens,0),
		        COALESCE(t.cache_write_5m_tokens,0) + COALESCE(o.cache_write_5m_tokens,0),
		        COALESCE(t.cache_write_1h_tokens,0) + COALESCE(o.cache_write_1h_tokens,0),
		        COALESCE(t.cache_read_tokens,0) + COALESCE(o.cache_read_tokens,0),
		        COALESCE(t.output_tokens,0) + COALESCE(o.output_tokens,0),
		        (COALESCE(t.cost_amount,0) + COALESCE(o.cost_amount,0))::text,
		        COALESCE(t.unpriced_tokens,0) + COALESCE(o.unpriced_tokens,0),
		        COALESCE(o.input_tokens,0), COALESCE(o.cache_write_5m_tokens,0),
		        COALESCE(o.cache_write_1h_tokens,0), COALESCE(o.cache_read_tokens,0),
		        COALESCE(o.output_tokens,0),
		        COALESCE(o.cost_amount,0)::text,
		        COALESCE(o.unpriced_tokens,0)
		   FROM t FULL OUTER JOIN o
		     ON t.usage_day = o.usage_day AND t.cost_currency = o.cost_currency
		  ORDER BY 1, 2`, args...)
	if err != nil {
		return nil, fmt.Errorf("read cost for project %s: %w", projectID, err)
	}
	return scanProjectCostReport(rows, "project "+projectID)
}
```

Note the `WHERE` clause is reused verbatim inside both CTEs (it already
reads `project_id = $1` plus the same positional day filters, so it applies
correctly to each side before the join — do not apply it after the join).

Add `scanProjectCostReport` (parallel to the existing `scanCostReport`,
which stays exactly as-is and keeps serving `TaskCost` — see Step 4):

```go
// scanProjectCostReport scans ProjectCost's combined (task + overhead) query
// into per-day rows plus per-currency totals, each carrying overhead's own
// share alongside the combined figure. Closes rows.
func scanProjectCostReport(rows *sql.Rows, desc string) (*CostReport, error) {
	defer rows.Close()
	report := &CostReport{}
	totals := map[string]*costTotal{}
	var order []string
	for rows.Next() {
		var d CostDay
		if err := rows.Scan(&d.Day, &d.Currency,
			&d.Tokens.Input, &d.Tokens.CacheWrite5m, &d.Tokens.CacheWrite1h,
			&d.Tokens.CacheRead, &d.Tokens.Output, &d.Cost, &d.UnpricedTokens,
			&d.OverheadTokens.Input, &d.OverheadTokens.CacheWrite5m, &d.OverheadTokens.CacheWrite1h,
			&d.OverheadTokens.CacheRead, &d.OverheadTokens.Output,
			&d.OverheadCost, &d.OverheadUnpricedTokens); err != nil {
			return nil, fmt.Errorf("scan cost row for %s: %w", desc, err)
		}
		d.Day = d.Day.UTC()
		report.Days = append(report.Days, d)

		t, ok := totals[d.Currency]
		if !ok {
			t = &costTotal{}
			totals[d.Currency] = t
			order = append(order, d.Currency)
		}
		t.tokens.Add(d.Tokens)
		if err := t.amount.add(d.Cost); err != nil {
			return nil, err
		}
		t.unpriced += d.UnpricedTokens
		t.overheadTokens.Add(d.OverheadTokens)
		if err := t.overheadAmount.add(d.OverheadCost); err != nil {
			return nil, err
		}
		t.overheadUnpriced += d.OverheadUnpricedTokens
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read cost for %s: %w", desc, err)
	}

	for _, c := range order {
		t := totals[c]
		report.Totals = append(report.Totals, CostTotal{
			Currency:               c,
			Tokens:                 t.tokens,
			Cost:                   t.amount.String(),
			UnpricedTokens:         t.unpriced,
			OverheadTokens:         t.overheadTokens,
			OverheadCost:           t.overheadAmount.String(),
			OverheadUnpricedTokens: t.overheadUnpriced,
		})
	}
	return report, nil
}
```

Extend `costTotal` (used by both scan functions) with the three overhead
accumulator fields:

```go
type costTotal struct {
	tokens   TokenCounts
	amount   microAmount
	unpriced int64

	overheadTokens   TokenCounts
	overheadAmount   microAmount
	overheadUnpriced int64
}
```

**Step 4 — `TaskCost`/`scanCostReport` stay task-only.** `scanCostReport`
(used only by `TaskCost` after this change) is otherwise unchanged, but each
`CostDay` it builds must set the three new fields to the literal zero shape
so the wire form is never an empty string:

```go
		if err := rows.Scan(&d.Day, &d.Currency, &d.Tokens.Input, &d.Tokens.CacheWrite5m,
			&d.Tokens.CacheWrite1h, &d.Tokens.CacheRead, &d.Tokens.Output,
			&d.Cost, &d.UnpricedTokens); err != nil {
			return nil, fmt.Errorf("scan cost row for %s: %w", desc, err)
		}
		d.Day = d.Day.UTC()
		d.OverheadCost = "0.000000" // TaskCost has no overhead concept (spec 052 §2)
		report.Days = append(report.Days, d)
```

and, in the totals loop at the end of `scanCostReport`, set
`OverheadCost: "0.000000"` on each appended `CostTotal` (its
`OverheadTokens`/`OverheadUnpricedTokens` are already correctly zero from
Go's zero value). Do not touch `TaskCost`'s SQL, its `WITH scope` CTE, or
its session-count query — only this literal.

- [ ] Write the failing tests, in `internal/store/session_usage_test.go`:

```go
func TestReportProjectOverheadUsageReplacesNotAccumulates(t *testing.T) {
	st := newTestStore(t)
	proj := createTestProject(t, st, "Overhead Proj")

	first := []SessionUsageBucket{{
		Day: mustDay(t, "2026-08-01"), Model: "claude-sonnet-5", Speed: "standard",
		Tokens: TokenCounts{Input: 100, Output: 10},
	}}
	if err := st.ReportProjectOverheadUsage(t.Context(), proj, "claude-code", "sess-1", first); err != nil {
		t.Fatalf("first report: %v", err)
	}
	second := []SessionUsageBucket{{
		Day: mustDay(t, "2026-08-01"), Model: "claude-sonnet-5", Speed: "standard",
		Tokens: TokenCounts{Input: 5, Output: 1},
	}}
	if err := st.ReportProjectOverheadUsage(t.Context(), proj, "claude-code", "sess-1", second); err != nil {
		t.Fatalf("second report: %v", err)
	}

	report, err := st.ProjectCost(t.Context(), proj, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d days, want 1: %+v", len(report.Days), report.Days)
	}
	if got := report.Days[0].OverheadTokens.Input; got != 5 {
		t.Errorf("overhead input tokens = %d, want 5 (replaced, not accumulated)", got)
	}
	if got := report.Days[0].Tokens.Input; got != 5 {
		t.Errorf("combined input tokens = %d, want 5 (task-attributed side is zero)", got)
	}
}

func TestReportProjectOverheadUsageUnknownProject(t *testing.T) {
	st := newTestStore(t)
	err := st.ReportProjectOverheadUsage(t.Context(), "no-such-project", "claude-code", "sess-1",
		[]SessionUsageBucket{{Day: mustDay(t, "2026-08-01"), Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 1}}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestProjectCostCombinesTaskAndOverheadOnSameDay(t *testing.T) {
	st, _, taskID := newTestStoreWithClaimedTask(t) // existing helper: see other tests in this file for its exact name/shape
	proj := projectOfTask(t, st, taskID)            // resolve via st.GetTask or equivalent existing helper

	day := mustDay(t, "2026-08-01")
	// Task-attributed side, through the existing session-usage path.
	sess := touchSessionForTest(t, st, taskID, "claude-code", "sess-task")
	if err := st.replaceAgentSessionUsageForTest(t.Context(), sess, []SessionUsageBucket{
		{Day: day, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}},
	}); err != nil {
		t.Fatalf("task usage: %v", err)
	}
	// Overhead side, same day, same project.
	if err := st.ReportProjectOverheadUsage(t.Context(), proj, "claude-code", "sess-overhead",
		[]SessionUsageBucket{{Day: day, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 50, Output: 5}}}); err != nil {
		t.Fatalf("overhead usage: %v", err)
	}

	report, err := st.ProjectCost(t.Context(), proj, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d days, want 1 (one currency, one day): %+v", len(report.Days), report.Days)
	}
	d := report.Days[0]
	if d.Tokens.Input != 150 {
		t.Errorf("combined input = %d, want 150 (100 task + 50 overhead)", d.Tokens.Input)
	}
	if d.OverheadTokens.Input != 50 {
		t.Errorf("overhead input = %d, want 50", d.OverheadTokens.Input)
	}
}
```

The last test names two helpers (`newTestStoreWithClaimedTask`,
`touchSessionForTest`/`replaceAgentSessionUsageForTest`,
`projectOfTask`) that likely do not exist verbatim yet — before writing it,
read the existing tests in `internal/store/session_usage_test.go` and
`internal/store/agent_sessions_test.go` for whatever helper already claims a
task and reports session usage (e.g. a pattern building on `st.Claim`,
`st.TouchAgentSession`, `st.AddTask`) and call the real ones under their
real names; the sketch above names the *shape* of the test (task-attributed
and overhead usage on the same project and day, read back combined), not a
literal API to invoke unchanged.

- [ ] Run: `go test -trimpath ./internal/store -run 'TestReportProjectOverheadUsage|TestProjectCostCombines' -v` — expect FAIL (functions/fields do not exist yet).
- [ ] Implement Steps 1-4 above.
- [ ] Run again — expect PASS.
- [ ] Run the full package: `go test -trimpath -race ./internal/store/...` — confirm nothing existing broke, in particular any test touching `TaskCost` or `ProjectCost`.
- [ ] Commit: `git add internal/store/session_usage.go internal/store/session_usage_test.go && git commit -m "store: project overhead usage and a combined ProjectCost report (spec 052 §2)"`

---

### Task 4 — Wire model: overhead fields and the report input

```yaml
kind: feature
priority: high
skills: [ ]
blockedBy: [3]
```

**Files:**
- Modify: `internal/model/cost.go`
- Modify: `internal/model/agentsession.go`

**Interfaces:**
- Produces: `model.CostOverhead`, extended `model.CostDay`/`model.CostTotals`,
  `model.ProjectOverheadUsageInput`.

In `internal/model/cost.go`, add:

```go
// CostOverhead is the portion of a CostDay/CostTotals' combined figures that
// came from usage with no task to bill to (spec 052): a main-checkout
// orchestration session, or a worktree whose lease the reporting actor no
// longer held. Nested under its own "overhead" key on the wire rather than
// flattened, so its fields cannot collide with the combined totals'.
type CostOverhead struct {
	TokenCounts
	CostAmount     string `json:"cost_amount"`
	UnpricedTokens int64  `json:"unpriced_tokens"`
}
```

and add one field to each of `CostDay` and `CostTotals` (after the existing
`UnpricedTokens` field in each):

```go
	Overhead CostOverhead `json:"overhead"`
```

In `internal/model/agentsession.go`, add:

```go
// ProjectOverheadUsageInput is the request body for
// POST /api/v1/projects/{id}/overhead-usage: report usage with no task to
// bill to (spec 052 §2). All three fields are required.
type ProjectOverheadUsageInput struct {
	Agent              string               `json:"agent"`
	ExternalSessionID  string               `json:"external_session_id"`
	Usage              []SessionUsageBucket `json:"usage"`
}
```

- [ ] Add the two types and two fields exactly as above.
- [ ] Run `go build -trimpath ./internal/model/...` — expect success (this
      package has no tests of its own beyond `rule_test.go`/`deps_test.go`;
      run those too: `go test -trimpath ./internal/model/...` — they check
      the package stays a stdlib-only leaf and that every wire shape follows
      ADR 036; confirm both new types pass without modification).
- [ ] Commit: `git add internal/model/cost.go internal/model/agentsession.go && git commit -m "model: overhead cost fields and the overhead-usage report input (spec 052 §4)"`

---

### Task 5 — API: the overhead-usage endpoint, and mapping overhead onto the wire

```yaml
kind: feature
priority: high
skills: [ ]
blockedBy: [3, 4]
```

**Files:**
- Modify: `internal/api/authz.go`
- Modify: `internal/api/router.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/agentsessions.go`
- Modify: `internal/api/admin.go`
- Test: `internal/api/agentsessions_test.go`

**Interfaces:**
- Consumes: `store.CostDay`/`store.CostTotal`'s new `Overhead*` fields
  (Task 3), `model.CostOverhead`/`ProjectOverheadUsageInput` (Task 4),
  `s.st.ReportProjectOverheadUsage` (Task 3), `readJSON`, `writeBodyErr`,
  `writeErr`, `s.mapStoreErr` (all already in `internal/api/server.go`).
- Produces: `POST /api/v1/projects/{id}/overhead-usage`.

**Step 1 — permission.** In `internal/api/authz.go`, add a new permission
constant next to `permProjectRead`/`permProjectAdmin`:

```go
	permProjectReport Permission = "project.report"
```

and grant it in the `grants` map, next to the `permProjectRead`/`permProjectAdmin` entry:

```go
	permProjectReport: {RoleUser, RoleAdmin},
```

**Step 2 — route guard.** In `internal/api/router.go`, add, in the
`/api/v1/projects` block near the other project routes:

```go
	"POST /api/v1/projects/{id}/overhead-usage": guarded(permProjectReport),
```

**Step 3 — registration.** In `internal/api/server.go`, near the existing
`r.api("POST /api/v1/tasks/{id}/agent-session/end", ...)` line, add:

```go
	r.api("POST /api/v1/projects/{id}/overhead-usage", s.reportProjectOverheadUsage)
```

**Step 4 — handler.** In `internal/api/agentsessions.go`, add:

```go
// reportProjectOverheadUsage handles POST /api/v1/projects/{id}/overhead-usage:
// record usage with no task to bill to (spec 052 §2). All body fields are
// required; toUsageBuckets does the same day/model validation the
// task-scoped endpoints already use.
func (s *server) reportProjectOverheadUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.ProjectOverheadUsageInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	buckets, err := toUsageBuckets(req.Usage)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.ReportProjectOverheadUsage(r.Context(), id, req.Agent, req.ExternalSessionID, buckets); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

**Step 5 — wire mapping.** In `internal/api/admin.go`, update
`toCostReportJSON` to carry the overhead fields through, for both the `Days`
and `Totals` loops:

```go
	for _, d := range pc.Days {
		out.Days = append(out.Days, model.CostDay{
			Day:            d.Day.Format(time.DateOnly),
			Currency:       d.Currency,
			TokenCounts:    toTokenCountsJSON(d.Tokens),
			CostAmount:     d.Cost,
			UnpricedTokens: d.UnpricedTokens,
			Overhead: model.CostOverhead{
				TokenCounts:    toTokenCountsJSON(d.OverheadTokens),
				CostAmount:     d.OverheadCost,
				UnpricedTokens: d.OverheadUnpricedTokens,
			},
		})
	}
	for _, t := range pc.Totals {
		out.Totals = append(out.Totals, model.CostTotals{
			Currency:       t.Currency,
			TokenCounts:    toTokenCountsJSON(t.Tokens),
			CostAmount:     t.Cost,
			UnpricedTokens: t.UnpricedTokens,
			Overhead: model.CostOverhead{
				TokenCounts:    toTokenCountsJSON(t.OverheadTokens),
				CostAmount:     t.OverheadCost,
				UnpricedTokens: t.OverheadUnpricedTokens,
			},
		})
	}
```

- [ ] Write the failing test, in `internal/api/agentsessions_test.go`:

```go
func TestReportProjectOverheadUsage(t *testing.T) {
	srv, st := newTestServer(t) // existing helper in this file — read its
	                            // signature before use; it likely returns
	                            // (http.Handler, *store.Store) or similar.
	proj := createTestProjectForAPI(t, st, "Overhead API Proj") // use whatever
	                            // existing helper this test file already has
	                            // for standing up a project; do not invent a
	                            // new one if one exists.

	body := `{"agent":"claude-code","external_session_id":"sess-1","usage":[
		{"day":"2026-08-01","model":"claude-sonnet-5","input_tokens":100,"output_tokens":10}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+proj+"/overhead-usage", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken) // match this file's existing auth-header convention
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	cost, err := st.ProjectCost(t.Context(), proj, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(cost.Days) != 1 || cost.Days[0].OverheadTokens.Input != 100 {
		t.Fatalf("overhead not recorded: %+v", cost.Days)
	}
}

func TestReportProjectOverheadUsageUnknownProject404(t *testing.T) {
	srv, _ := newTestServer(t)
	body := `{"agent":"claude-code","external_session_id":"sess-1","usage":[
		{"day":"2026-08-01","model":"claude-sonnet-5","input_tokens":1}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/no-such-project/overhead-usage", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
}
```

Before writing these, read three or four existing tests already in
`internal/api/agentsessions_test.go` (e.g. the ones covering
`touchAgentSession`) for this file's real helper names (server
construction, auth header/token, project/task fixtures) and use those exact
names — the sketch above names the request/response shape to assert, not a
literal helper API.

- [ ] Run: `go test -trimpath ./internal/api -run TestReportProjectOverheadUsage -v` — expect FAIL (route not registered / 404 on everything, or a compile error before the handler exists).
- [ ] Implement Steps 1-5.
- [ ] Run again — expect PASS (both new tests).
- [ ] Run the full package: `go test -trimpath -race ./internal/api/...` — the route-guard boot check inside `NewServer`'s own tests will fail loudly if the new route is registered without a `routeGuards` entry, or vice versa; confirm it passes.
- [ ] Commit: `git add internal/api/authz.go internal/api/router.go internal/api/server.go internal/api/agentsessions.go internal/api/admin.go internal/api/agentsessions_test.go && git commit -m "api: POST /api/v1/projects/{id}/overhead-usage, and overhead on the wire (spec 052 §2, §4)"`

---

### Task 6 — CLI: resolving a project without a server round trip, and the client call

```yaml
kind: feature
priority: high
skills: [ ]
blockedBy: [4]
```

**Files:**
- Modify: `internal/cli/client.go`
- Test: `internal/cli/client_test.go`

**Interfaces:**
- Produces: `cli.CurrentProjectFrom(startDir string) string`;
  `(*cli.Client) ReportProjectOverheadUsage(ctx context.Context, projectID
  string, in model.ProjectOverheadUsageInput) error`.

Add, next to `WorktreeDirFrom` in `internal/cli/client.go`:

```go
// CurrentProjectFrom returns the project startDir's repo is scoped to, using
// only local config -- a repo-local config file, then the user config -- with
// no server round trip and no keychain access: the same cheap, dir-scoped
// contract WorktreeDirFrom has, for a caller (internal/hookrun) that must
// resolve a project ahead of a backbone call from a directory that is not
// necessarily the process's own cwd. Returns "" when neither config sets
// one. Does not attempt the git-remote tier of project resolution (spec 019
// §_, ResolveScope) -- a caller that also wants that tier still needs
// ResolveScope with a real client.
func CurrentProjectFrom(startDir string) string {
	if repoPath, ok := findRepoConfig(startDir); ok {
		if data, err := os.ReadFile(repoPath); err == nil {
			if cfg, err := parseConfig(string(data)); err == nil && cfg.CurrentProject != "" {
				return cfg.CurrentProject
			}
		}
	}
	if path, err := configPath(); err == nil {
		if data, err := os.ReadFile(path); err == nil {
			if cfg, err := parseConfig(string(data)); err == nil {
				return cfg.CurrentProject
			}
		}
	}
	return ""
}
```

Add, next to `EndAgentSession`:

```go
// ReportProjectOverheadUsage calls POST /api/v1/projects/{id}/overhead-usage:
// report usage with no task to bill to (spec 052 §2).
func (c *Client) ReportProjectOverheadUsage(ctx context.Context, projectID string, in model.ProjectOverheadUsageInput) error {
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/overhead-usage", in)
	return err
}
```

- [ ] Write the failing test, in `internal/cli/client_test.go`:

```go
func TestCurrentProjectFromRepoConfig(t *testing.T) {
	_, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n") // existing helper in this file
	writeRepoConfig(t, workDir, ".worklode", "current_project = \"repo-proj\"\n") // existing helper in this file
	if got := cli.CurrentProjectFrom(workDir); got != "repo-proj" {
		t.Fatalf("CurrentProjectFrom = %q, want %q", got, "repo-proj")
	}
}

func TestCurrentProjectFromUserConfigFallback(t *testing.T) {
	home, workDir := repoTestHome(t, "current_project = \"user-proj\"\n") // no repo-local config written
	_ = home
	if got := cli.CurrentProjectFrom(workDir); got != "user-proj" {
		t.Fatalf("CurrentProjectFrom = %q, want %q", got, "user-proj")
	}
}
```

Confirm `repoTestHome`/`writeRepoConfig`'s exact signatures against their
use in `TestLoadConfigCurrentProjectFromUserConfig` and
`TestRepoConfigOverridesCurrentProject` (both already in this file) before
writing these — reuse them exactly, do not redefine.

- [ ] Run: `go test -trimpath ./internal/cli -run TestCurrentProjectFrom -v` — expect FAIL (function does not exist).
- [ ] Implement `CurrentProjectFrom` and `ReportProjectOverheadUsage`.
- [ ] Run again — expect PASS.
- [ ] Run the full package: `go test -trimpath -race ./internal/cli/...`
- [ ] Commit: `git add internal/cli/client.go internal/cli/client_test.go && git commit -m "cli: CurrentProjectFrom and the overhead-usage client call (spec 052 §3)"`

---

### Task 7 — hookrun: report overhead instead of dropping it

```yaml
kind: feature
priority: high
skills:
  - superpowers:systematic-debugging
blockedBy: [2, 5, 6]
```

**Files:**
- Modify: `internal/hookrun/hookrun.go`
- Test: `internal/hookrun/hookrun_test.go`

**Interfaces:**
- Consumes: `worktree.Layout.TaskID(dir string) (taskID string, ok bool)`
  (`internal/worktree/worktree.go`, unchanged), `transcript.Bucket.Cwd`
  (Task 2), `cli.CurrentProjectFrom` and `(*cli.Client)
  ReportProjectOverheadUsage` (Task 6), `(*cli.Client) TouchAgentSession`/
  `EndAgentSession` (unchanged).
- Produces: `classifyTranscriptUsage(opts Options, l worktree.Layout,
  transcriptPath string) map[string][]model.SessionUsageBucket`;
  `reportOtherTaskAndOverheadUsage(ctx, opts, c, root, sessionID string,
  byTask map[string][]model.SessionUsageBucket)`; `reportOverhead(ctx, opts,
  c, root, sessionID string, buckets []model.SessionUsageBucket)`; changed
  signatures on `reportSession`, `endSession`, `closeSession` (all gain an
  `l worktree.Layout` parameter — every existing call site already has `l`
  in scope).

**Step 1 — classification.** Add, after `sessionUsage`:

```go
// classifyTranscriptUsage parses transcriptPath's FULL usage -- every cwd the
// session touched, not just one worktree -- and groups it by task id,
// resolved per distinct cwd via l.TaskID (the same worktree-layout
// resolution the hook guard itself uses). A cwd outside the configured
// worktree base, or one whose directory name/stamp carries no task id,
// groups under the empty string key. Same no-failure contract as
// sessionUsage, which this supplements rather than replaces: a missing
// transcript, an unreadable file, or an empty result all yield nil.
func classifyTranscriptUsage(opts Options, l worktree.Layout, transcriptPath string) map[string][]model.SessionUsageBucket {
	if transcriptPath == "" {
		return nil
	}
	buckets, err := transcript.ParseFile(transcriptPath, transcript.Options{})
	if err != nil {
		warn(opts, "parse transcript %s: %v", transcriptPath, err)
		return nil
	}
	out := map[string][]model.SessionUsageBucket{}
	for _, b := range buckets {
		taskID, _ := l.TaskID(b.Cwd) // ok=false ⇒ "" (overhead)
		out[taskID] = append(out[taskID], model.SessionUsageBucket{
			Day:                b.Day.Format(time.DateOnly),
			Model:              b.Model,
			Speed:              b.Speed,
			InputTokens:        b.Usage.Input,
			CacheWrite5mTokens: b.Usage.CacheWrite5m,
			CacheWrite1hTokens: b.Usage.CacheWrite1h,
			CacheReadTokens:    b.Usage.CacheRead,
			OutputTokens:       b.Usage.Output,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
```

**Step 2 — the fan-out helpers.** Add, after `classifyTranscriptUsage`:

```go
// reportOverhead resolves the current repo's project from local config only
// (no server round trip -- see cli.CurrentProjectFrom) and reports buckets
// as that project's overhead usage: tokens with no task to bill to (spec
// 052). root is the directory to resolve the project from -- see
// layoutFor's doc comment on why this must be the hook's own resolved
// worktree root, never os.Getwd().
func reportOverhead(ctx context.Context, opts Options, c *cli.Client, root, sessionID string, buckets []model.SessionUsageBucket) {
	if len(buckets) == 0 {
		return
	}
	project := cli.CurrentProjectFrom(root)
	if project == "" {
		warn(opts, "no project configured for %s; dropping %d overhead usage bucket(s)", root, len(buckets))
		return
	}
	octx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	if err := c.ReportProjectOverheadUsage(octx, project, model.ProjectOverheadUsageInput{
		Agent: opts.agentName(), ExternalSessionID: sessionID, Usage: buckets,
	}); err != nil {
		warn(opts, "report project overhead usage on %s: %v", project, err)
	}
}

// reportOtherTaskAndOverheadUsage reports every classifyTranscriptUsage
// group besides the calling handler's own task (already removed from
// byTask by the caller): a real task id bills through TouchAgentSession --
// never EndAgentSession, since this call has no opinion on whether that
// other task's own session should end, only that these tokens billed to it
// -- and a TouchAgentSession failure (most commonly ErrNotFound: this actor
// no longer holds that task's lease) redirects those buckets to overhead
// instead of dropping them. The "" group (main checkout, or a cwd outside
// the worktree layout) always goes to overhead.
func reportOtherTaskAndOverheadUsage(ctx context.Context, opts Options, c *cli.Client, root, sessionID string, byTask map[string][]model.SessionUsageBucket) {
	if len(byTask) == 0 {
		return
	}
	overhead := byTask[""]
	for taskID, buckets := range byTask {
		if taskID == "" {
			continue
		}
		octx, cancel := context.WithTimeout(ctx, backboneTimeout)
		_, _, err := c.TouchAgentSession(octx, taskID, opts.agentName(), "", sessionID, buckets)
		cancel()
		if err != nil {
			warn(opts, "report agent session on %s: %v (billing to project overhead instead)", taskID, err)
			overhead = append(overhead, buckets...)
		}
	}
	reportOverhead(ctx, opts, c, root, sessionID, overhead)
}
```

**Step 3 — rewrite `reportSession`.** Replace its body:

```go
func reportSession(ctx context.Context, opts Options, c *cli.Client, l worktree.Layout, taskID, root, sessionID, transcriptPath string) {
	if sessionID == "" {
		return
	}
	byTask := classifyTranscriptUsage(opts, l, transcriptPath)

	if taskID != "" {
		ownUsage := byTask[taskID]
		delete(byTask, taskID)

		sctx, cancel := context.WithTimeout(ctx, backboneTimeout)
		sess, _, err := c.TouchAgentSession(sctx, taskID, opts.agentName(), "", sessionID, ownUsage)
		cancel()
		if err != nil {
			warn(opts, "report agent session on %s: %v", taskID, err)
		} else if sess.EndedAt == nil {
			if err := recordHeartbeat(root, opts.now()); err != nil {
				warn(opts, "record heartbeat: %v", err)
			}
		}
	} else {
		// No task of our own (main checkout, or an unleased worktree): there
		// is no agent_sessions row to touch, only usage to classify below.
		// The marker's own debounce still applies -- stamp it directly, since
		// there is no TouchAgentSession response to gate it on.
		if err := recordHeartbeat(root, opts.now()); err != nil {
			warn(opts, "record heartbeat: %v", err)
		}
	}

	reportOtherTaskAndOverheadUsage(ctx, opts, c, root, sessionID, byTask)
}
```

**Step 4 — rewrite `endSession`.** Replace its body (note the added
`l worktree.Layout` parameter and dropped `c` construction stays where it
was, since callers still don't all have a client yet):

```go
func endSession(ctx context.Context, opts Options, l worktree.Layout, taskID, sessionID, transcriptPath, root string) {
	if sessionID == "" {
		return
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}

	byTask := classifyTranscriptUsage(opts, l, transcriptPath)

	if taskID != "" {
		ownUsage := byTask[taskID]
		delete(byTask, taskID)

		ectx, cancel := context.WithTimeout(ctx, backboneTimeout)
		endErr := c.EndAgentSession(ectx, taskID, model.EndAgentSessionInput{
			Agent: opts.agentName(), SessionID: sessionID, Usage: ownUsage,
		})
		cancel()
		if endErr != nil {
			warn(opts, "end agent session on %s: %v", taskID, endErr)
		}
	}
	// taskID == "" (main checkout, or an unleased worktree): no
	// agent_sessions row was ever opened here, so only the classification
	// below applies.

	reportOtherTaskAndOverheadUsage(ctx, opts, c, root, sessionID, byTask)
}
```

**Step 5 — thread `l` through `closeSession` and every caller.** Change
`closeSession`'s signature and body:

```go
func closeSession(ctx context.Context, opts Options, p Payload, l worktree.Layout, taskID, root string) {
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID, _ = markerSessionID(root)
	}
	endSession(ctx, opts, l, taskID, sessionID, p.TranscriptPath, root)
	if err := removeSessionMarker(root); err != nil {
		warn(opts, "remove session marker: %v", err)
	}
}
```

Update every existing call site to pass `l` (each already has it in scope
as a handler parameter) and to match the new argument order
(`taskID, root` instead of whatever order it called with before):

- `reportSession`'s three existing callers — `handleSessionStart` (around
  line 547), `handlePreCommit` (around line 821), and the new
  `handleWorktreeEnter` call in Step 6 below — change to
  `reportSession(ctx, opts, c, l, taskID, root, sessionID, transcriptPath)`.
- `closeSession`'s two existing callers — `handleSessionEnd` (Step 6) and
  `handleWorktreeExit` — change to `closeSession(ctx, opts, p, l, taskID, root)`.

**Step 6 — loosen the guard on `handleHeartbeat`, `handleSessionEnd`, and
`handleWorktreeEnter`.** Each currently opens with
`root, taskID, ok := leasedWorktree(l, dir); if !ok { return }`. Change the
guard to `root != ""` and let `taskID` be `""`:

```go
func handleHeartbeat(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	root, taskID, _ := leasedWorktree(l, dir)
	if root == "" {
		return // not inside a git worktree at all ⇒ nothing to report, nowhere to debounce
	}

	m, hasMarker := readSessionMarker(root)
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = m.SessionID
	}
	if sessionID == "" {
		return
	}

	if !hasMarker || (p.SessionID != "" && p.SessionID != m.SessionID) {
		if err := writeSessionMarker(root, sessionID, opts.now()); err != nil {
			warn(opts, "write session marker: %v", err)
		}
	} else if !heartbeatDue(root, opts.now()) {
		return
	}

	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	reportSession(ctx, opts, c, l, taskID, root, sessionID, p.TranscriptPath)
}

func handleSessionEnd(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	root, taskID, _ := leasedWorktree(l, dir)
	if root == "" {
		return
	}
	closeSession(ctx, opts, p, l, taskID, root)
}

func handleWorktreeEnter(ctx context.Context, opts Options, p Payload, dir string, l worktree.Layout) {
	entered := payloadPath(p, dir)
	root, taskID, _ := leasedWorktree(l, entered)
	if root == "" {
		return
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	if p.SessionID != "" {
		if err := writeSessionMarker(root, p.SessionID, opts.now()); err != nil {
			warn(opts, "write session marker: %v", err)
		}
	}
	reportSession(ctx, opts, c, l, taskID, root, p.SessionID, p.TranscriptPath)
}
```

`handleWorktreeExit`, `handleSessionStart`, and `handlePreCommit` keep their
existing `ok`-gated guards unchanged (only the `closeSession`/`reportSession`
call-site argument order changes, per Step 5) — see spec 052 §3's
"Unchanged, deliberately" list for why.

- [ ] Write the failing tests, in `internal/hookrun/hookrun_test.go`:

```go
// TestHeartbeatFromMainCheckoutSplitsTaskAndOverhead: an orchestrator
// heartbeat fired from the main checkout, whose transcript names one cwd
// under a currently lease-held task worktree and one cwd with no task at
// all, bills the first through /agent-session and the second through
// /overhead-usage.
func TestHeartbeatFromMainCheckoutSplitsTaskAndOverhead(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Main checkout task")

	transcriptPath := writeTestTranscript(t, []testTranscriptLine{ // see below
		{Cwd: wtDir, Model: "claude-sonnet-5", Day: "2026-08-01", Input: 100, Output: 10},
		{Cwd: root, Model: "claude-sonnet-5", Day: "2026-08-01", Input: 50, Output: 5},
	})

	before := rec.count("/agent-session")
	beforeOverhead := rec.count("/overhead-usage")
	runHook(t, "heartbeat", Payload{Cwd: root, SessionID: "sess-orch", TranscriptPath: transcriptPath})

	if rec.count("/agent-session") != before+1 {
		t.Fatal("main-checkout heartbeat did not report the worktree's own task")
	}
	if rec.count("/overhead-usage") != beforeOverhead+1 {
		t.Fatal("main-checkout heartbeat did not report project overhead")
	}

	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	sess, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-orch")
	if err != nil {
		t.Fatalf("agent session for the worktree's task: %v", err)
	}
	if sess.InputTokens == nil || *sess.InputTokens != 100 {
		t.Fatalf("worktree task input tokens = %v, want 100", sess.InputTokens)
	}
}

// TestHeartbeatOutsideWorktreeIsNOP already exists and must keep passing
// unmodified -- Cwd: t.TempDir() has no enclosing git repository at all, so
// root == "" and the handler still returns immediately.
```

`writeTestTranscript`/`testTranscriptLine` likely do not exist yet — check
whether `internal/hookrun/hookrun_test.go` already has a transcript-fixture
helper (search for any existing test that sets `TranscriptPath`); if none
exists, add a small helper that writes one JSONL line per entry in the shape
`internal/transcript`'s `entry` type reads (`type`, `cwd`, `timestamp`,
`message.id`, `message.model`, `message.usage.{input_tokens,output_tokens}`),
to a temp file, and returns its path.

- [ ] Run: `go test -trimpath ./internal/hookrun -run TestHeartbeatFromMainCheckoutSplitsTaskAndOverhead -v` — expect FAIL.
- [ ] Implement Steps 1-6.
- [ ] Run again — expect PASS.
- [ ] Run the full package: `go test -trimpath -race ./internal/hookrun/...` — pay particular attention to `TestHeartbeatOutsideWorktreeIsNOP`, `TestPreCommitWithoutLeaseIsSilent`, `TestWorktreeExitWithoutExplicitPathIsNOP`, and every existing `TestHeartbeat*`/`TestSessionEnd*`/`TestWorktreeEnter*` test — all must still pass with no edits beyond call-site argument order.
- [ ] Commit: `git add internal/hookrun/hookrun.go internal/hookrun/hookrun_test.go && git commit -m "hookrun: report overhead usage instead of dropping it (spec 052 §3)"`

---

### Task 8 — Cockpit: show the combined spend and its overhead share

```yaml
kind: feature
priority: medium
skills:
  - worklode-cockpit-ui
blockedBy: [4, 5]
```

**Files:**
- Modify: `internal/ui/views.go`
- Modify: `internal/ui/cockpit.templ`
- Modify: `internal/api/render.go`
- Test: `internal/api/render_test.go` (or wherever `cockpitCostTotals` is
  already tested — search for its existing test before adding a new file)

**Interfaces:**
- Consumes: `model.CostReport`/`model.CostTotals.Overhead` (Task 4/5).
- Produces: extended `ui.CockpitCostTotal`.

In `internal/ui/views.go`, add one field to `CockpitCostTotal`:

```go
type CockpitCostTotal struct {
	Currency            string
	CostAmount          string
	UnpricedTokens      int64
	OverheadCostAmount  string
}
```

In `internal/api/render.go`, update `cockpitCostTotals`:

```go
func cockpitCostTotals(c model.CostReport) []ui.CockpitCostTotal {
	out := make([]ui.CockpitCostTotal, 0, len(c.Totals))
	for _, t := range c.Totals {
		out = append(out, ui.CockpitCostTotal{
			Currency:           t.Currency,
			CostAmount:         t.CostAmount,
			UnpricedTokens:     t.UnpricedTokens,
			OverheadCostAmount: t.Overhead.CostAmount,
		})
	}
	return out
}
```

In `internal/ui/cockpit.templ`, add one line inside the `else` branch of
`automationBoundary`, right after the existing "Agent spend, 30 days" line:

```templ
templ automationBoundary(totals []CockpitCostTotal) {
	<div class="railcard">
		<div class="rt">Automation boundary</div>
		<div class="auto-line"><span class="k">Policy</span><span class="v">not configured</span></div>
		<div class="auto-line"><span class="k">Budget</span><span class="v">not configured</span></div>
		if len(totals) == 0 {
			<div class="auto-line"><span class="k">Agent spend, 30 days</span><span class="v">none recorded</span></div>
		} else {
			for _, t := range totals {
				<div class="auto-line"><span class="k">Agent spend, 30 days</span><span class="v">{ t.Currency } { t.CostAmount }</span></div>
				<div class="auto-line"><span class="k">&mdash; of which overhead</span><span class="v">{ t.Currency } { t.OverheadCostAmount }</span></div>
			}
		}
		<p class="small muted">No automation policy or budget is configured for this project.</p>
	</div>
}
```

Regenerate the templ-generated Go after this edit:
`go generate ./internal/ui/...` (or `templ generate ./internal/ui` per the
`worklode-cockpit-ui` skill — check which command this repo's Makefile/CI
actually runs and use that one; do not hand-edit `cockpit_templ.go`).

- [ ] Search for an existing test of `cockpitCostTotals` (likely in
      `internal/api/render_test.go`) and extend it to assert
      `OverheadCostAmount` round-trips from a `model.CostTotals` with a
      non-zero `Overhead.CostAmount`; if none exists, add one:

```go
func TestCockpitCostTotalsIncludesOverhead(t *testing.T) {
	report := model.CostReport{Totals: []model.CostTotals{{
		Currency:   "USD",
		CostAmount: "1.500000",
		Overhead:   model.CostOverhead{CostAmount: "1.300000"},
	}}}
	got := cockpitCostTotals(report)
	if len(got) != 1 || got[0].OverheadCostAmount != "1.300000" {
		t.Fatalf("cockpitCostTotals = %+v, want OverheadCostAmount 1.300000", got)
	}
}
```

- [ ] Run: `go test -trimpath ./internal/api -run TestCockpitCostTotalsIncludesOverhead -v` — expect FAIL.
- [ ] Implement the four file changes above and regenerate the templ file.
- [ ] Run again — expect PASS.
- [ ] Run `make build` to confirm the whole binary still compiles (the
      `templ generate` step must have produced valid Go).
- [ ] Commit: `git add internal/ui/views.go internal/ui/cockpit.templ internal/ui/cockpit_templ.go internal/api/render.go internal/api/render_test.go && git commit -m "cockpit: show overhead's share of a project's agent spend (spec 052 §4)"`

---

## Self-Review

- **Spec coverage:** 052 §1 (schema) → Task 1. §2 (store/API) → Tasks 3, 5.
  §3 (hook wiring) → Tasks 2, 6, 7. §4 (wire model/cockpit) → Tasks 4, 5
  (mapping), 8 (UI). §5 (testing) → a concrete test named in every task
  above. §6 (out of scope) → no task touches `TaskCost`'s query,
  `handleSessionStart`, or `handlePreCommit`'s guards, confirmed in Task 7's
  final full-package test run.
- **Placeholder scan:** every step shows real code or a real command; the
  two tests in Tasks 3, 5, and 7 that name a plan-time guess at an existing
  helper's identity say so explicitly and instruct reading the real file
  first, rather than inventing a signature that might not compile.
- **Type consistency:** `classifyTranscriptUsage`'s return type
  (`map[string][]model.SessionUsageBucket`) is the same type
  `reportOtherTaskAndOverheadUsage` and `reportSession`/`endSession` consume
  in Task 7; `store.CostDay`/`CostTotal`'s `Overhead*` field names (Task 3)
  match what Task 5's `toCostReportJSON` reads and what `model.CostOverhead`
  (Task 4) exposes on the wire; `cli.CurrentProjectFrom`'s signature (Task 6)
  matches its one call site in Task 7.
