package store

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
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
	sess, err := s.AgentSession(t.Context(), lease.ID, "claude-code", sessionID)
	if err != nil {
		t.Fatalf("read session %s: %v", sessionID, err)
	}
	rows, err := s.db.Query(
		`SELECT usage_day, model, speed, `+usageColumns+`, cost_amount::text, cost_currency
		   FROM agent_session_usage WHERE agent_session_id = $1
		  ORDER BY usage_day, model, speed`, sess.ID)
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

	touchUsage := func(buckets []SessionUsageBucket) *AgentSession {
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
