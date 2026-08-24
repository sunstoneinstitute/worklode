package store

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Rates the arithmetic in this file is hand-computed against, from the
// migration's seed (claude-sonnet-5 on its introductory row, which is what
// applies on leaseTestNow's 2026-07-19):
//
//	sonnet-5: input 2.00, 5m write 2.50, 1h write 4.00, read 0.20, output 10.00
//	haiku-4-5: input 1.00, 5m write 1.25, 1h write 2.00, read 0.10, output  5.00
//
// all per million tokens, in USD.
var (
	usageDay1 = time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	usageDay2 = time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
)

// usageSession claims a fresh task on project "horndb" and opens an agent
// session on its lease.
func usageSession(t *testing.T, s *Store, worktree, sessionID string) *Lease {
	t.Helper()
	lease := leaseForTest(t, s, worktree)
	if _, err := s.TouchAgentSession(t.Context(), lease.TaskID, "stig", "claude-code", "", sessionID, nil); err != nil {
		t.Fatalf("touch %s: %v", sessionID, err)
	}
	return lease
}

// reportUsage ends the session with the given buckets.
func reportUsage(t *testing.T, s *Store, lease *Lease, sessionID string, buckets []SessionUsageBucket) {
	t.Helper()
	if err := s.EndAgentSession(t.Context(), lease.TaskID, "stig", "claude-code", sessionID,
		SessionUsage{Buckets: buckets}); err != nil {
		t.Fatalf("end %s: %v", sessionID, err)
	}
}

// reopen heartbeats the session so it can be ended again — EndAgentSession
// only matches rows with ended_at IS NULL.
func reopen(t *testing.T, s *Store, lease *Lease, sessionID string, now *time.Time) {
	t.Helper()
	*now = now.Add(time.Minute)
	if _, err := s.TouchAgentSession(t.Context(), lease.TaskID, "stig", "claude-code", "", sessionID, nil); err != nil {
		t.Fatalf("reopen %s: %v", sessionID, err)
	}
}

type usageRow struct {
	Day      string
	Model    string
	Speed    string
	Tokens   TokenCounts
	Cost     string // "" means SQL NULL
	Currency string
}

// sessionUsageRows reads agent_session_usage directly, so the stored detail is
// asserted rather than inferred from the rollup.
func sessionUsageRows(t *testing.T, s *Store, lease *Lease, sessionID string) []usageRow {
	t.Helper()
	rows, err := s.db.Query(
		`SELECT usage_day, model, speed, `+usageColumns+`, cost_amount::text, cost_currency
		   FROM agent_session_usage WHERE agent_session_id = $1
		  ORDER BY usage_day, model, speed`,
		sessionRowID(t, s, lease.ID, "claude-code", sessionID))
	if err != nil {
		t.Fatalf("read usage rows: %v", err)
	}
	defer rows.Close()

	var out []usageRow
	for rows.Next() {
		var r usageRow
		var day time.Time
		var cost sql.NullString
		if err := rows.Scan(&day, &r.Model, &r.Speed, &r.Tokens.Input, &r.Tokens.CacheWrite5m,
			&r.Tokens.CacheWrite1h, &r.Tokens.CacheRead, &r.Tokens.Output,
			&cost, &r.Currency); err != nil {
			t.Fatalf("scan usage row: %v", err)
		}
		r.Day = day.UTC().Format(time.DateOnly)
		r.Cost = cost.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("usage rows: %v", err)
	}
	return out
}

type rollupRow struct {
	Day      string
	Currency string
	Tokens   TokenCounts
	Cost     string
	Unpriced int64
}

func projectRollupRows(t *testing.T, s *Store, projectID string) []rollupRow {
	t.Helper()
	rows, err := s.db.Query(
		`SELECT usage_day, cost_currency, `+usageColumns+`, cost_amount::text, unpriced_tokens
		   FROM project_daily_cost WHERE project_id = $1
		  ORDER BY usage_day, cost_currency`, projectID)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	defer rows.Close()

	var out []rollupRow
	for rows.Next() {
		var r rollupRow
		var day time.Time
		if err := rows.Scan(&day, &r.Currency, &r.Tokens.Input, &r.Tokens.CacheWrite5m,
			&r.Tokens.CacheWrite1h, &r.Tokens.CacheRead, &r.Tokens.Output,
			&r.Cost, &r.Unpriced); err != nil {
			t.Fatalf("scan rollup row: %v", err)
		}
		r.Day = day.UTC().Format(time.DateOnly)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rollup rows: %v", err)
	}
	return out
}

// projectOverheadRollupRows reads project_daily_overhead_cost directly, so a
// day dropping out of the rollup after a shrinking re-report is asserted
// against the table itself rather than inferred from ProjectCost.
func projectOverheadRollupRows(t *testing.T, s *Store, projectID string) []rollupRow {
	t.Helper()
	rows, err := s.db.Query(
		`SELECT usage_day, cost_currency, `+usageColumns+`, cost_amount::text, unpriced_tokens
		   FROM project_daily_overhead_cost WHERE project_id = $1
		  ORDER BY usage_day, cost_currency`, projectID)
	if err != nil {
		t.Fatalf("read overhead rollup: %v", err)
	}
	defer rows.Close()

	var out []rollupRow
	for rows.Next() {
		var r rollupRow
		var day time.Time
		if err := rows.Scan(&day, &r.Currency, &r.Tokens.Input, &r.Tokens.CacheWrite5m,
			&r.Tokens.CacheWrite1h, &r.Tokens.CacheRead, &r.Tokens.Output,
			&r.Cost, &r.Unpriced); err != nil {
			t.Fatalf("scan overhead rollup row: %v", err)
		}
		r.Day = day.UTC().Format(time.DateOnly)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("overhead rollup rows: %v", err)
	}
	return out
}

// TestEndAgentSessionRecordsPricedUsage walks one bucket all the way through:
// stored detail, session roll-up, project rollup, and ProjectCost.
func TestEndAgentSessionRecordsPricedUsage(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	tokens := TokenCounts{Input: 1000, CacheWrite5m: 2000, CacheWrite1h: 3000, CacheRead: 4000, Output: 5000}
	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: tokens},
	})

	// 1000*2.00 + 2000*2.50 + 3000*4.00 + 4000*0.20 + 5000*10.00, per MTok
	// = (2 + 5 + 12 + 0.8 + 50) e9 micros / 1e6 = 69,800 micro-USD.
	const wantCost = "0.069800"

	got := sessionUsageRows(t, s, lease, "sess-1")
	want := []usageRow{{
		Day: "2026-07-19", Model: "claude-sonnet-5", Speed: "standard",
		Tokens: tokens, Cost: wantCost, Currency: "USD",
	}}
	assertUsageRows(t, got, want)

	// input_tokens on the session is every prompt-side class summed; the class
	// split that determines cost lives in agent_session_usage.
	sess, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if sess.InputTokens == nil || *sess.InputTokens != 10_000 {
		t.Fatalf("session input_tokens: got %s, want 10000 (1000+2000+3000+4000)", nullable(sess.InputTokens))
	}
	if sess.OutputTokens == nil || *sess.OutputTokens != 5000 {
		t.Fatalf("session output_tokens: got %s, want 5000", nullable(sess.OutputTokens))
	}
	if sess.CostAmount == nil || *sess.CostAmount != wantCost {
		t.Fatalf("session cost_amount: got %s, want %s", nullable(sess.CostAmount), wantCost)
	}

	pc, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(pc.Days) != 1 || len(pc.Totals) != 1 {
		t.Fatalf("ProjectCost shape: got %d days and %d totals, want 1 and 1", len(pc.Days), len(pc.Totals))
	}
	if pc.Days[0].Cost != wantCost || pc.Days[0].Tokens != tokens {
		t.Fatalf("ProjectCost day: got cost %s tokens %+v, want %s and %+v",
			pc.Days[0].Cost, pc.Days[0].Tokens, wantCost, tokens)
	}
	if pc.Totals[0].Currency != "USD" || pc.Totals[0].Cost != wantCost {
		t.Fatalf("ProjectCost total: got %s %s, want USD %s",
			pc.Totals[0].Currency, pc.Totals[0].Cost, wantCost)
	}
	if pc.Totals[0].UnpricedTokens != 0 {
		t.Fatalf("unpriced tokens: got %d, want 0", pc.Totals[0].UnpricedTokens)
	}
}

// TestEndAgentSessionReplacesUsage is the load-bearing one: the source
// transcript is cumulative, so a second report carries absolute totals. Adding
// them would inflate every long session by its own history.
func TestEndAgentSessionReplacesUsage(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 1000, Output: 1000}},
	})
	reopen(t, s, lease, "sess-1", now)
	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 3000, Output: 2000}},
	})

	// 3000*2.00 + 2000*10.00 per MTok = 26,000 micro-USD. Accumulating would
	// have given 4000/3000 tokens and 0.038000.
	assertUsageRows(t, sessionUsageRows(t, s, lease, "sess-1"), []usageRow{{
		Day: "2026-07-19", Model: "claude-sonnet-5", Speed: "standard",
		Tokens: TokenCounts{Input: 3000, Output: 2000}, Cost: "0.026000", Currency: "USD",
	}})

	sess, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if sess.InputTokens == nil || *sess.InputTokens != 3000 {
		t.Fatalf("session input_tokens: got %s, want 3000", nullable(sess.InputTokens))
	}

	assertRollupRows(t, projectRollupRows(t, s, "horndb"), []rollupRow{{
		Day: "2026-07-19", Currency: "USD",
		Tokens: TokenCounts{Input: 3000, Output: 2000}, Cost: "0.026000",
	}})
}

// TestTouchAgentSessionRecordsUsage covers the crash path: a session that
// never ends cleanly reports its spend on the heartbeat or nowhere. The
// heartbeat carries a running total, so a later one must replace the earlier
// one exactly as a clean end does — and the project rollup must follow.
func TestTouchAgentSessionRecordsUsage(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	touchUsage := func(buckets []SessionUsageBucket) *model.AgentSession {
		t.Helper()
		*now = now.Add(time.Minute)
		sess, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1", buckets)
		if err != nil {
			t.Fatalf("touch with usage: %v", err)
		}
		return sess
	}

	sess := touchUsage([]SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 1000, Output: 1000}},
	})
	// 1000*2.00 + 1000*10.00 per MTok = 12,000 micro-USD.
	if sess.CostAmount == nil || *sess.CostAmount != "0.012000" {
		t.Fatalf("session cost after first heartbeat: got %s, want 0.012000", nullable(sess.CostAmount))
	}

	sess = touchUsage([]SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 3000, Output: 2000}},
	})
	// 3000*2.00 + 2000*10.00 = 26,000 micro-USD; accumulating would give 38,000.
	if sess.CostAmount == nil || *sess.CostAmount != "0.026000" {
		t.Fatalf("session cost after second heartbeat: got %s, want 0.026000", nullable(sess.CostAmount))
	}

	assertUsageRows(t, sessionUsageRows(t, s, lease, "sess-1"), []usageRow{{
		Day: "2026-07-19", Model: "claude-sonnet-5", Speed: "standard",
		Tokens: TokenCounts{Input: 3000, Output: 2000}, Cost: "0.026000", Currency: "USD",
	}})
	assertRollupRows(t, projectRollupRows(t, s, "horndb"), []rollupRow{{
		Day: "2026-07-19", Currency: "USD",
		Tokens: TokenCounts{Input: 3000, Output: 2000}, Cost: "0.026000",
	}})

	// A heartbeat that reports nothing (no transcript, or none of its turns
	// billed here) leaves the recorded usage alone rather than clearing it.
	sess = touchUsage(nil)
	if sess.CostAmount == nil || *sess.CostAmount != "0.026000" {
		t.Fatalf("session cost after a usage-less heartbeat: got %s, want 0.026000", nullable(sess.CostAmount))
	}
}

// A caller may report the same (day, model, speed) twice. The row must be
// merged before pricing, so its stored amount covers all of its tokens.
func TestSessionUsageMergesDuplicateBuckets(t *testing.T) {
	s, _ := openLeaseStore(t)
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 1000}},
		{Day: usageDay1, Model: "claude-sonnet-5", Speed: "standard", Tokens: TokenCounts{Input: 500, Output: 100}},
	})

	// 1500*2.00 + 100*10.00 per MTok = 4,000 micro-USD.
	assertUsageRows(t, sessionUsageRows(t, s, lease, "sess-1"), []usageRow{{
		Day: "2026-07-19", Model: "claude-sonnet-5", Speed: "standard",
		Tokens: TokenCounts{Input: 1500, Output: 100}, Cost: "0.004000", Currency: "USD",
	}})
}

// One session routinely mixes models at several-fold different rates.
func TestSessionUsagePricesEachModelSeparately(t *testing.T) {
	s, _ := openLeaseStore(t)
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 1000, Output: 1000}},
		{Day: usageDay1, Model: "claude-haiku-4-5", Tokens: TokenCounts{Input: 1000, Output: 1000}},
	})

	// sonnet: 1000*2.00 + 1000*10.00 = 12,000 micro-USD.
	// haiku:  1000*1.00 + 1000* 5.00 =  6,000 micro-USD.
	assertUsageRows(t, sessionUsageRows(t, s, lease, "sess-1"), []usageRow{
		{
			Day: "2026-07-19", Model: "claude-haiku-4-5", Speed: "standard",
			Tokens: TokenCounts{Input: 1000, Output: 1000}, Cost: "0.006000", Currency: "USD",
		},
		{
			Day: "2026-07-19", Model: "claude-sonnet-5", Speed: "standard",
			Tokens: TokenCounts{Input: 1000, Output: 1000}, Cost: "0.012000", Currency: "USD",
		},
	})
	assertRollupRows(t, projectRollupRows(t, s, "horndb"), []rollupRow{{
		Day: "2026-07-19", Currency: "USD",
		Tokens: TokenCounts{Input: 2000, Output: 2000}, Cost: "0.018000",
	}})
}

// A session that runs past midnight must split its cost across both days.
func TestSessionUsageSplitsAcrossDays(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
		// A day given with a time-of-day is truncated to its UTC date.
		{Day: usageDay2.Add(13 * time.Hour), Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 2000}},
	})

	assertRollupRows(t, projectRollupRows(t, s, "horndb"), []rollupRow{
		{Day: "2026-07-19", Currency: "USD", Tokens: TokenCounts{Output: 1000}, Cost: "0.010000"},
		{Day: "2026-07-20", Currency: "USD", Tokens: TokenCounts{Output: 2000}, Cost: "0.020000"},
	})

	windows := []struct {
		name     string
		from, to time.Time
		wantDays []string
		wantCost string
	}{
		{"unbounded", time.Time{}, time.Time{}, []string{"2026-07-19", "2026-07-20"}, "0.030000"},
		{"first day only", usageDay1, usageDay1, []string{"2026-07-19"}, "0.010000"},
		{"second day only", usageDay2, usageDay2, []string{"2026-07-20"}, "0.020000"},
		{"open-ended from the second day", usageDay2, time.Time{}, []string{"2026-07-20"}, "0.020000"},
		{"after the window", usageDay2.AddDate(0, 0, 1), time.Time{}, nil, ""},
	}
	for _, w := range windows {
		t.Run(w.name, func(t *testing.T) {
			pc, err := s.ProjectCost(ctx, "horndb", w.from, w.to)
			if err != nil {
				t.Fatalf("ProjectCost: %v", err)
			}
			if len(pc.Days) != len(w.wantDays) {
				t.Fatalf("days: got %d, want %d", len(pc.Days), len(w.wantDays))
			}
			for i, want := range w.wantDays {
				if got := pc.Days[i].Day.Format(time.DateOnly); got != want {
					t.Fatalf("day %d: got %s, want %s", i, got, want)
				}
			}
			if w.wantCost == "" {
				if len(pc.Totals) != 0 {
					t.Fatalf("totals outside the window: got %+v, want none", pc.Totals)
				}
				return
			}
			if pc.Totals[0].Cost != w.wantCost {
				t.Fatalf("window cost: got %s, want %s", pc.Totals[0].Cost, w.wantCost)
			}
		})
	}
}

// A model with no rate on file is unpriced, which is deliberately distinct
// from free: its amount stays NULL and its tokens surface as unpriced.
func TestSessionUsageRecordsUnpricedModel(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
		{Day: usageDay1, Model: "vendor-y-nightly", Tokens: TokenCounts{Input: 1000, Output: 500}},
	})

	rows := sessionUsageRows(t, s, lease, "sess-1")
	assertUsageRows(t, rows, []usageRow{
		{
			Day: "2026-07-19", Model: "claude-sonnet-5", Speed: "standard",
			Tokens: TokenCounts{Output: 1000}, Cost: "0.010000", Currency: "USD",
		},
		{
			Day: "2026-07-19", Model: "vendor-y-nightly", Speed: "standard",
			Tokens: TokenCounts{Input: 1000, Output: 500}, Cost: "", Currency: "USD",
		},
	})

	var isNull bool
	if err := s.db.QueryRow(
		`SELECT cost_amount IS NULL FROM agent_session_usage WHERE model = 'vendor-y-nightly'`,
	).Scan(&isNull); err != nil {
		t.Fatalf("read cost_amount: %v", err)
	}
	if !isNull {
		t.Fatal("unpriced model: cost_amount is not NULL")
	}

	// The rollup carries only the priced amount, plus a visible count of what
	// it understates by.
	assertRollupRows(t, projectRollupRows(t, s, "horndb"), []rollupRow{{
		Day: "2026-07-19", Currency: "USD",
		Tokens: TokenCounts{Input: 1000, Output: 1500}, Cost: "0.010000", Unpriced: 1500,
	}})

	pc, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if pc.Totals[0].UnpricedTokens != 1500 {
		t.Fatalf("unpriced tokens: got %d, want 1500", pc.Totals[0].UnpricedTokens)
	}
	if pc.Totals[0].Cost != "0.010000" {
		t.Fatalf("cost: got %s, want 0.010000 (unpriced tokens must not be billed at zero)",
			pc.Totals[0].Cost)
	}
}

// Two sessions on different tasks of the same project land in one rollup row.
func TestProjectDailyCostAggregatesSessions(t *testing.T) {
	s, _ := openLeaseStore(t)
	first := usageSession(t, s, "host:/.worktrees/one", "sess-1")
	second := usageSession(t, s, "host:/.worktrees/two", "sess-2")

	reportUsage(t, s, first, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
	})
	reportUsage(t, s, second, "sess-2", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 3000}},
	})

	assertRollupRows(t, projectRollupRows(t, s, "horndb"), []rollupRow{{
		Day: "2026-07-19", Currency: "USD",
		Tokens: TokenCounts{Output: 4000}, Cost: "0.040000",
	}})
}

// A nil Buckets means "no breakdown reported"; an empty non-nil one means
// "this session billed nothing", and must clear what was recorded.
func TestEndAgentSessionNilBucketsKeepsUsageEmptyClearsIt(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
	})

	reopen(t, s, lease, "sess-1", now)
	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
		SessionUsage{}); err != nil {
		t.Fatalf("end with nil buckets: %v", err)
	}
	assertUsageRows(t, sessionUsageRows(t, s, lease, "sess-1"), []usageRow{{
		Day: "2026-07-19", Model: "claude-sonnet-5", Speed: "standard",
		Tokens: TokenCounts{Output: 1000}, Cost: "0.010000", Currency: "USD",
	}})
	assertRollupRows(t, projectRollupRows(t, s, "horndb"), []rollupRow{{
		Day: "2026-07-19", Currency: "USD", Tokens: TokenCounts{Output: 1000}, Cost: "0.010000",
	}})

	reopen(t, s, lease, "sess-1", now)
	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{})

	if rows := sessionUsageRows(t, s, lease, "sess-1"); len(rows) != 0 {
		t.Fatalf("usage rows after clearing: got %+v, want none", rows)
	}
	// The rollup is recomputed from the (now absent) usage rows, so the day
	// drops out entirely rather than lingering at its old amount.
	if rows := projectRollupRows(t, s, "horndb"); len(rows) != 0 {
		t.Fatalf("rollup rows after clearing: got %+v, want none", rows)
	}
	sess, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if sess.InputTokens == nil || *sess.InputTokens != 0 ||
		sess.OutputTokens == nil || *sess.OutputTokens != 0 {
		t.Fatalf("session tokens after clearing: got in=%s out=%s, want 0 and 0",
			nullable(sess.InputTokens), nullable(sess.OutputTokens))
	}
	if sess.CostAmount != nil {
		t.Fatalf("session cost_amount after clearing: got %v, want nil", *sess.CostAmount)
	}
}

func TestSessionUsageRejectsInvalidBuckets(t *testing.T) {
	tests := []struct {
		name   string
		bucket SessionUsageBucket
	}{
		{"no model", SessionUsageBucket{Day: usageDay1, Tokens: TokenCounts{Output: 1}}},
		{"no day", SessionUsageBucket{Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1}}},
		{"negative output", SessionUsageBucket{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: -1}}},
		{"negative cache read", SessionUsageBucket{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{CacheRead: -5, Output: 10}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openLeaseStore(t)
			ctx := t.Context()
			lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

			err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
				SessionUsage{Buckets: []SessionUsageBucket{tt.bucket}})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("got %v, want ErrInvalidInput", err)
			}

			// The whole transaction rolls back, so the session is still open
			// and nothing was recorded.
			sess, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
			if err != nil {
				t.Fatalf("read session: %v", err)
			}
			if sess.EndedAt != nil {
				t.Fatalf("session ended despite a rejected bucket: %v", *sess.EndedAt)
			}
			if rows := sessionUsageRows(t, s, lease, "sess-1"); len(rows) != 0 {
				t.Fatalf("usage rows after a rejected bucket: got %+v, want none", rows)
			}
		})
	}
}

// Reporting usage is lease-holder-only, the same probe-resistant policy as the
// rest of the agent-session API.
func TestEndAgentSessionUsageRejectsNonHolder(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")
	if err := s.CreateActor(ctx, "mallory", "agent", "Mallory", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}

	err := s.EndAgentSession(ctx, lease.TaskID, "mallory", "claude-code", "sess-1",
		SessionUsage{Buckets: []SessionUsageBucket{
			{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
		}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-holder: got %v, want ErrNotFound", err)
	}
	if rows := sessionUsageRows(t, s, lease, "sess-1"); len(rows) != 0 {
		t.Fatalf("usage rows after a non-holder report: got %+v, want none", rows)
	}
}

// nullable renders a nullable column for a failure message, so a mismatch
// reads as a value or "NULL" rather than as a pointer address.
func nullable[T any](p *T) string {
	if p == nil {
		return "NULL"
	}
	return fmt.Sprintf("%v", *p)
}

func assertUsageRows(t *testing.T, got, want []usageRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("usage rows: got %d (%+v), want %d (%+v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("usage row %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func assertRollupRows(t *testing.T, got, want []rollupRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("rollup rows: got %d (%+v), want %d (%+v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rollup row %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// --- TaskCost --------------------------------------------------------

// TestTaskCostReportsUsage covers the basic per-task rollup: one session's
// usage becomes one day/currency row and total, and Sessions counts the one
// session that billed it.
func TestTaskCostReportsUsage(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	tokens := TokenCounts{Input: 1000, Output: 1000}
	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: tokens},
	})

	// 1000*2.00 + 1000*10.00 per MTok = 12,000 micro-USD.
	tc, err := s.TaskCost(ctx, lease.TaskID, false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost: %v", err)
	}
	if len(tc.Days) != 1 || len(tc.Totals) != 1 {
		t.Fatalf("TaskCost shape: got %d days and %d totals, want 1 and 1", len(tc.Days), len(tc.Totals))
	}
	if tc.Days[0].Cost != "0.012000" || tc.Days[0].Tokens != tokens {
		t.Fatalf("TaskCost day: got cost %s tokens %+v, want 0.012000 and %+v",
			tc.Days[0].Cost, tc.Days[0].Tokens, tokens)
	}
	if tc.Totals[0].Currency != "USD" || tc.Totals[0].Cost != "0.012000" {
		t.Fatalf("TaskCost total: got %s %s, want USD 0.012000", tc.Totals[0].Currency, tc.Totals[0].Cost)
	}
	if tc.Sessions != 1 {
		t.Fatalf("Sessions: got %d, want 1", tc.Sessions)
	}
}

// Two sessions on the same task on the same day fold into one row, but each
// still counts toward Sessions.
func TestTaskCostFoldsSameDaySessions(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/.worktrees/one")

	for _, sid := range []string{"sess-1", "sess-2"} {
		if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", sid, nil); err != nil {
			t.Fatalf("touch %s: %v", sid, err)
		}
	}
	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
	})
	reportUsage(t, s, lease, "sess-2", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 3000}},
	})

	tc, err := s.TaskCost(ctx, lease.TaskID, false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost: %v", err)
	}
	if len(tc.Days) != 1 {
		t.Fatalf("days: got %d, want 1", len(tc.Days))
	}
	if tc.Days[0].Tokens != (TokenCounts{Output: 4000}) || tc.Days[0].Cost != "0.040000" {
		t.Fatalf("day: got tokens %+v cost %s, want Output 4000 cost 0.040000",
			tc.Days[0].Tokens, tc.Days[0].Cost)
	}
	if tc.Sessions != 2 {
		t.Fatalf("Sessions: got %d, want 2", tc.Sessions)
	}
}

// An unknown task id is an error, not a silent zero: it is the guard against
// reporting nothing for a typo'd id.
func TestTaskCostUnknownTask(t *testing.T) {
	s := openTaskStore(t)
	_, err := s.TaskCost(t.Context(), "WL-9999", false, time.Time{}, time.Time{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// A task that exists but has never had a billed session reports an empty
// report and zero sessions, not an error.
func TestTaskCostNoSessions(t *testing.T) {
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	tc, err := s.TaskCost(t.Context(), task.ID, false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost: %v", err)
	}
	if len(tc.Days) != 0 || len(tc.Totals) != 0 {
		t.Fatalf("TaskCost: got %+v, want an empty report", tc)
	}
	if tc.Sessions != 0 {
		t.Fatalf("Sessions: got %d, want 0", tc.Sessions)
	}
}

// A container task holds no lease itself; its own cost is empty unless
// includeChildren widens the scope to its child_of descendants — including a
// grandchild, which a non-recursive single-hop query would miss.
func TestTaskCostIncludeChildren(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	parent := leaseForTest(t, s, "host:/.worktrees/parent")
	child := usageSession(t, s, "host:/.worktrees/child", "sess-child")
	grandchild := usageSession(t, s, "host:/.worktrees/grandchild", "sess-grandchild")

	addChildOf := func(child, parent string) {
		t.Helper()
		if _, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "task.edge_added", nil,
			func(tx *sql.Tx, eventID int64) error {
				return AddEdge(tx, leaseTestNow, child, parent, "child_of", eventID)
			}); err != nil {
			t.Fatalf("AddEdge %s child_of %s: %v", child, parent, err)
		}
	}
	addChildOf(child.TaskID, parent.TaskID)
	addChildOf(grandchild.TaskID, child.TaskID)

	reportUsage(t, s, child, "sess-child", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
	})
	reportUsage(t, s, grandchild, "sess-grandchild", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 2000}},
	})

	without, err := s.TaskCost(ctx, parent.TaskID, false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost without children: %v", err)
	}
	if len(without.Days) != 0 || without.Sessions != 0 {
		t.Fatalf("without children: got %+v, want empty (a descendant's usage must not leak in)", without)
	}

	with, err := s.TaskCost(ctx, parent.TaskID, true, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost with children: %v", err)
	}
	// 3000 combined output tokens (child's 1000 plus the grandchild's 2000) at
	// 10.00/MTok = 0.030000. A non-recursive single-hop query would only reach
	// the child and total 0.010000, so this pins the grandchild's contribution.
	if len(with.Days) != 1 || with.Days[0].Cost != "0.030000" || with.Sessions != 2 {
		t.Fatalf("with children: got %+v, want the grandchild's usage folded in (cost 0.030000, 2 sessions)", with)
	}
}

// from/to clip which days count, on both the report and the session count.
func TestTaskCostWindow(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
		{Day: usageDay2.Add(13 * time.Hour), Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 2000}},
	})

	windows := []struct {
		name     string
		from, to time.Time
		wantDays []string
		wantCost string
	}{
		{"unbounded", time.Time{}, time.Time{}, []string{"2026-07-19", "2026-07-20"}, "0.030000"},
		{"first day only", usageDay1, usageDay1, []string{"2026-07-19"}, "0.010000"},
		{"second day only", usageDay2, usageDay2, []string{"2026-07-20"}, "0.020000"},
		{"after the window", usageDay2.AddDate(0, 0, 1), time.Time{}, nil, ""},
	}
	for _, w := range windows {
		t.Run(w.name, func(t *testing.T) {
			tc, err := s.TaskCost(ctx, lease.TaskID, false, w.from, w.to)
			if err != nil {
				t.Fatalf("TaskCost: %v", err)
			}
			if len(tc.Days) != len(w.wantDays) {
				t.Fatalf("days: got %d, want %d", len(tc.Days), len(w.wantDays))
			}
			for i, want := range w.wantDays {
				if got := tc.Days[i].Day.Format(time.DateOnly); got != want {
					t.Fatalf("day %d: got %s, want %s", i, got, want)
				}
			}
			if w.wantCost == "" {
				if len(tc.Totals) != 0 {
					t.Fatalf("totals outside the window: got %+v, want none", tc.Totals)
				}
				return
			}
			if tc.Totals[0].Cost != w.wantCost {
				t.Fatalf("window cost: got %s, want %s", tc.Totals[0].Cost, w.wantCost)
			}
		})
	}
}

// A model with no rate on file lands its tokens in UnpricedTokens rather than
// being billed at zero.
func TestTaskCostUnpricedTokens(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
		{Day: usageDay1, Model: "vendor-y-nightly", Tokens: TokenCounts{Input: 1000, Output: 500}},
	})

	tc, err := s.TaskCost(ctx, lease.TaskID, false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost: %v", err)
	}
	if len(tc.Totals) != 1 {
		t.Fatalf("totals: got %d, want 1", len(tc.Totals))
	}
	if tc.Totals[0].Cost != "0.010000" {
		t.Fatalf("cost: got %s, want 0.010000 (unpriced tokens must not be billed at zero)", tc.Totals[0].Cost)
	}
	if tc.Totals[0].UnpricedTokens != 1500 {
		t.Fatalf("unpriced tokens: got %d, want 1500", tc.Totals[0].UnpricedTokens)
	}
	if tc.Sessions != 1 {
		t.Fatalf("Sessions: got %d, want 1", tc.Sessions)
	}
}

// --- Project overhead usage -------------------------------------------

// The load-bearing case: overhead usage is a cumulative transcript total, so
// a second report must replace the first, never accumulate on top of it.
func TestReportProjectSessionUsageReplacesNotAccumulates(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-1", map[string][]SessionUsageBucket{"": {
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}},
	}}); err != nil {
		t.Fatalf("first report: %v", err)
	}
	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-1", map[string][]SessionUsageBucket{"": {
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 5, Output: 1}},
	}}); err != nil {
		t.Fatalf("second report: %v", err)
	}

	report, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
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

func TestReportProjectSessionUsageUnknownProject(t *testing.T) {
	s := openTaskStore(t)
	err := s.ReportProjectSessionUsage(t.Context(), "no-such-project", "claude-code", "sess-1",
		map[string][]SessionUsageBucket{"": {{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 1}}}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// An unknown agent or an empty external session id are rejected before any
// project lookup or write, per spec 052 §5.
func TestReportProjectSessionUsageRejectsInvalidInput(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	buckets := []SessionUsageBucket{{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 1}}}

	if err := s.ReportProjectSessionUsage(ctx, "horndb", "not-a-real-agent", "sess-1", map[string][]SessionUsageBucket{"": buckets}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown agent: got %v, want ErrInvalidInput", err)
	}
	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "", map[string][]SessionUsageBucket{"": buckets}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty external session id: got %v, want ErrInvalidInput", err)
	}
}

func TestProjectCostCombinesTaskAndOverheadOnSameDay(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-task")

	reportUsage(t, s, lease, "sess-task", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}},
	})
	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-overhead",
		map[string][]SessionUsageBucket{"": {{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 50, Output: 5}}}}); err != nil {
		t.Fatalf("overhead usage: %v", err)
	}

	report, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
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

// The FULL OUTER JOIN in ProjectCost exists for exactly this case: a day
// with overhead usage only, and no task-attributed usage at all, must still
// surface as a row. A plain (inner) join would drop it silently, which is
// the common "pure orchestration day" this feature exists to stop losing.
func TestProjectCostDayWithOverheadOnlyNoTaskUsage(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-overhead",
		map[string][]SessionUsageBucket{"": {{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}}}}); err != nil {
		t.Fatalf("overhead usage: %v", err)
	}

	report, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d days, want 1 (overhead-only day must not be dropped by the join): %+v", len(report.Days), report.Days)
	}
	d := report.Days[0]
	if d.Tokens.Input != 100 || d.Tokens.Output != 10 {
		t.Errorf("combined tokens = %+v, want the overhead-only totals (100 input, 10 output)", d.Tokens)
	}
	if d.OverheadTokens.Input != 100 || d.OverheadTokens.Output != 10 {
		t.Errorf("overhead tokens = %+v, want 100 input, 10 output", d.OverheadTokens)
	}
	if len(report.Totals) != 1 {
		t.Fatalf("got %d totals, want 1", len(report.Totals))
	}
	// 100*2.00 + 10*10.00 per MTok = 300 micro-USD. Asserted as an absolute
	// value on each field rather than Cost == OverheadCost: two values
	// derived from the same source column would pass that comparison even if
	// a bug scaled both of them identically.
	const wantCost = "0.000300"
	if report.Totals[0].Cost != wantCost {
		t.Errorf("total cost = %s, want %s", report.Totals[0].Cost, wantCost)
	}
	if report.Totals[0].OverheadCost != wantCost {
		t.Errorf("total overhead cost = %s, want %s", report.Totals[0].OverheadCost, wantCost)
	}
}

// Pins the wire-format contract at the boundary the join could break it:
// on a day with task-attributed usage but no overhead row at all, the
// overhead side of the join has no row to COALESCE from, and the fallback
// literal must still render at CostDay.Cost's six-fractional-digit shape,
// not the bare "0" a same-scale numeric zero would give.
func TestProjectCostOverheadCostIsSixDigitsWithNoOverheadUsage(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-task")

	reportUsage(t, s, lease, "sess-task", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}},
	})

	report, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d days, want 1: %+v", len(report.Days), report.Days)
	}
	if got := report.Days[0].OverheadCost; got != "0.000000" {
		t.Errorf("OverheadCost = %q, want \"0.000000\" exactly (six fractional digits)", got)
	}
	if len(report.Totals) != 1 {
		t.Fatalf("got %d totals, want 1", len(report.Totals))
	}
	if got := report.Totals[0].OverheadCost; got != "0.000000" {
		t.Errorf("total OverheadCost = %q, want \"0.000000\" exactly (six fractional digits)", got)
	}
}

// A re-report that no longer covers a previously-reported day must clear
// that day's rollup row entirely -- the distinct failure mode from a
// same-day shrink: DELETE ... RETURNING usage_day must surface the dropped
// day so the rollup is rebuilt to nothing for it, not left stale. Task 7's
// hook path re-reports transcripts that can shrink to cover fewer days.
func TestReportProjectSessionUsageDropsRollupForDayNoLongerReported(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-1", map[string][]SessionUsageBucket{"": {
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}},
		{Day: usageDay2, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 200, Output: 20}},
	}}); err != nil {
		t.Fatalf("first report: %v", err)
	}
	if got := projectOverheadRollupRows(t, s, "horndb"); len(got) != 2 {
		t.Fatalf("got %d overhead rollup rows after first report, want 2: %+v", len(got), got)
	}

	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-1", map[string][]SessionUsageBucket{"": {
		{Day: usageDay2, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 200, Output: 20}},
	}}); err != nil {
		t.Fatalf("second (shrinking) report: %v", err)
	}

	rollup := projectOverheadRollupRows(t, s, "horndb")
	if len(rollup) != 1 || rollup[0].Day != "2026-07-20" {
		t.Fatalf("overhead rollup after re-report: got %+v, want only day 2026-07-20", rollup)
	}

	report, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	for _, d := range report.Days {
		if d.Day.Format(time.DateOnly) == "2026-07-19" && d.OverheadTokens.Total() != 0 {
			t.Fatalf("day 2026-07-19 still reports overhead after being dropped from the re-report: %+v", d)
		}
	}
}

// TaskCost has no overhead concept: a project that also has overhead
// recorded must not leak into a task's report, which must carry the zero
// overhead shape rather than an empty string.
func TestTaskCostUnaffectedByProjectOverhead(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-task")

	reportUsage(t, s, lease, "sess-task", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}},
	})
	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-overhead",
		map[string][]SessionUsageBucket{"": {{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 999, Output: 999}}}}); err != nil {
		t.Fatalf("overhead usage: %v", err)
	}

	tc, err := s.TaskCost(ctx, lease.TaskID, false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost: %v", err)
	}
	if len(tc.Days) != 1 {
		t.Fatalf("got %d days, want 1", len(tc.Days))
	}
	if tc.Days[0].Tokens.Input != 100 {
		t.Errorf("task-attributed input = %d, want 100 (overhead must not leak in)", tc.Days[0].Tokens.Input)
	}
	if tc.Days[0].OverheadTokens != (TokenCounts{}) || tc.Days[0].OverheadCost != "0.000000" {
		t.Errorf("TaskCost day overhead: got tokens %+v cost %s, want zero", tc.Days[0].OverheadTokens, tc.Days[0].OverheadCost)
	}
	if len(tc.Totals) != 1 {
		t.Fatalf("got %d totals, want 1", len(tc.Totals))
	}
	if tc.Totals[0].OverheadTokens != (TokenCounts{}) || tc.Totals[0].OverheadCost != "0.000000" {
		t.Errorf("TaskCost total overhead: got tokens %+v cost %s, want zero", tc.Totals[0].OverheadTokens, tc.Totals[0].OverheadCost)
	}
}

// Usage priced in two different currencies must stay two totals, never
// summed together — CostReport's contract has no conversion rate to do that
// with.
func TestTaskCostMultipleCurrencies(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	if err := s.UpsertModelPrice(ctx, ModelPrice{
		Model: "vendor-eur-model", EffectiveFrom: usageDay1, Currency: "EUR", OutputMicros: 5_000_000,
	}); err != nil {
		t.Fatalf("seed EUR price: %v", err)
	}

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Output: 1000}},
		{Day: usageDay1, Model: "vendor-eur-model", Tokens: TokenCounts{Output: 2000}},
	})

	tc, err := s.TaskCost(ctx, lease.TaskID, false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost: %v", err)
	}
	if len(tc.Days) != 2 {
		t.Fatalf("days: got %d (%+v), want 2 (one per currency)", len(tc.Days), tc.Days)
	}
	if len(tc.Totals) != 2 {
		t.Fatalf("totals: got %d (%+v), want 2", len(tc.Totals), tc.Totals)
	}
	// ORDER BY usage_day, cost_currency: same day, so EUR sorts before USD.
	wantTotals := []CostTotal{
		{Currency: "EUR", Tokens: TokenCounts{Output: 2000}, Cost: "0.010000"},
		{Currency: "USD", Tokens: TokenCounts{Output: 1000}, Cost: "0.010000"},
	}
	for i, want := range wantTotals {
		got := tc.Totals[i]
		if got.Currency != want.Currency || got.Tokens != want.Tokens || got.Cost != want.Cost {
			t.Fatalf("total %d: got %+v, want %+v", i, got, want)
		}
	}
}

// A session's tokens must be counted once, whichever bucket they land in.
// The client re-reports a running transcript total on every heartbeat, and
// the destination can change between two of them: usage billed to a task
// while its lease was held comes back as overhead once the lease is gone.
// Both sides then hold the same tokens, and ProjectCost sums the two.
func TestProjectCostDoesNotDoubleCountUsageMovedToOverhead(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")

	reportUsage(t, s, lease, "sess-1", []SessionUsageBucket{
		{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}},
	})
	// The next heartbeat, same session and same cumulative transcript, finds
	// no lease to bill to and reports the identical tokens as overhead.
	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-1",
		map[string][]SessionUsageBucket{
			"": {{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}}},
		}); err != nil {
		t.Fatalf("session usage: %v", err)
	}

	report, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d days, want 1: %+v", len(report.Days), report.Days)
	}
	if got := report.Days[0].Tokens.Input; got != 100 {
		t.Errorf("combined input = %d, want 100 — the same session's tokens counted once, not once per bucket", got)
	}
}

// The healthy split and the duplicate are numerically identical within one
// session, so the whole-session replace has to keep the split intact while
// removing the duplicate: a task's own turns bill to its agent session, the
// remainder bills to overhead, and the total is what the transcript held.
func TestReportProjectSessionUsageSplitsWithoutDuplicating(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")
	taskID := lease.TaskID

	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-1",
		map[string][]SessionUsageBucket{
			taskID: {{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}}},
			"":     {{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 50, Output: 5}}},
		}); err != nil {
		t.Fatalf("session usage: %v", err)
	}

	report, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d days, want 1: %+v", len(report.Days), report.Days)
	}
	d := report.Days[0]
	if d.Tokens.Input != 150 {
		t.Errorf("combined input = %d, want 150 (100 task + 50 overhead)", d.Tokens.Input)
	}
	if d.OverheadTokens.Input != 50 {
		t.Errorf("overhead input = %d, want 50 — only the unattributed remainder", d.OverheadTokens.Input)
	}
}

// Reclassification in the other direction: tokens reported as overhead, then
// re-reported against the task once its session is reachable again. The
// overhead row must be released as the task row takes them, or the day
// doubles just as it did the other way round.
func TestReportProjectSessionUsageMovesOverheadBackToATask(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := usageSession(t, s, "host:/.worktrees/one", "sess-1")
	taskID := lease.TaskID
	buckets := []SessionUsageBucket{{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}}}

	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-1",
		map[string][]SessionUsageBucket{"": buckets}); err != nil {
		t.Fatalf("first report: %v", err)
	}
	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-1",
		map[string][]SessionUsageBucket{taskID: buckets}); err != nil {
		t.Fatalf("second report: %v", err)
	}

	report, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d days, want 1: %+v", len(report.Days), report.Days)
	}
	d := report.Days[0]
	if d.Tokens.Input != 100 {
		t.Errorf("combined input = %d, want 100", d.Tokens.Input)
	}
	if d.OverheadTokens.Input != 0 {
		t.Errorf("overhead input = %d, want 0 — the task took these tokens back", d.OverheadTokens.Input)
	}
}

// A task named in the classification that this actor never opened a session
// for cannot be billed, but the tokens were still spent: they land in
// overhead rather than being dropped (spec 052 §3).
func TestReportProjectSessionUsageUnreachableTaskBillsToOverhead(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	if err := s.ReportProjectSessionUsage(ctx, "horndb", "claude-code", "sess-1",
		map[string][]SessionUsageBucket{
			"HORN-999": {{Day: usageDay1, Model: "claude-sonnet-5", Tokens: TokenCounts{Input: 100, Output: 10}}},
		}); err != nil {
		t.Fatalf("session usage: %v", err)
	}

	report, err := s.ProjectCost(ctx, "horndb", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d days, want 1: %+v", len(report.Days), report.Days)
	}
	if got := report.Days[0].OverheadTokens.Input; got != 100 {
		t.Errorf("overhead input = %d, want 100 — an unreachable task's tokens are not dropped", got)
	}
}
