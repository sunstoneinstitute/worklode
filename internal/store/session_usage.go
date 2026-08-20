package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// SessionUsageBucket is one session's tokens for one day, on one model, at one
// billing speed. A session is not a single bucket: it routinely spans midnight
// and routinely mixes models — a main loop on one, subagents on another — at
// rates that differ several-fold, and neither split can be recovered from a
// session total after the fact.
type SessionUsageBucket struct {
	// Day is the UTC calendar day the tokens were billed on. Only the date
	// part is significant.
	Day    time.Time
	Model  string
	Speed  string
	Tokens TokenCounts
}

// CostDay is one day's usage and cost, in one currency. Shared by a
// project's and a task's cost report.
type CostDay struct {
	Day      time.Time
	Currency string
	Tokens   TokenCounts
	// Cost is a decimal string with six fractional digits, so numeric(14,6)
	// round-trips exactly.
	Cost string
	// UnpricedTokens are tokens whose model had no rate on file. Cost
	// understates the bill by whatever they were worth.
	UnpricedTokens int64
}

// CostReport is a cost over a window: one row per day (ascending) plus
// per-currency totals. Currencies are never summed together — that needs a
// dated conversion rate this package has no business owning. Shared by a
// project's cost (ProjectCost) and a task's (TaskCost).
type CostReport struct {
	Days   []CostDay
	Totals []CostTotal
}

// CostTotal is the window total for one currency.
type CostTotal struct {
	Currency       string
	Tokens         TokenCounts
	Cost           string
	UnpricedTokens int64
}

// TaskCost is one task's usage and cost: the same per-day, per-currency
// report a project gets, plus the number of agent sessions behind it.
type TaskCost struct {
	CostReport
	Sessions int
}

// usageColumns is the token-class column list shared by the usage table and
// its rollup, in TokenCounts field order.
const usageColumns = `input_tokens, cache_write_5m_tokens, cache_write_1h_tokens,
	cache_read_tokens, output_tokens`

// replaceSessionUsageTx writes buckets as the complete usage of one session,
// pricing each against the rate in effect on its own day, and returns the
// session-level rollup.
//
// Replacement, never accumulation: the source of these numbers is a
// cumulative transcript, so a second report of the same session carries
// absolute totals that must overwrite the first, not add to it. That also
// makes reporting idempotent — the common case, since a session can be ended,
// reopened by a later heartbeat, and ended again.
//
// The returned currency is empty when priced buckets disagree on one, in which
// case the session-level amount is left unset: a single scalar cannot honestly
// hold two currencies, and the per-currency detail is in the usage rows.
func replaceSessionUsageTx(ctx context.Context, tx *sql.Tx, sessionID int64, buckets []SessionUsageBucket) (
	totals TokenCounts, cost string, currency string, days []time.Time, err error) {

	daySet := map[time.Time]bool{}

	rows, err := tx.QueryContext(ctx,
		`DELETE FROM agent_session_usage WHERE agent_session_id = $1 RETURNING usage_day`, sessionID)
	if err != nil {
		return totals, "", "", nil, fmt.Errorf("clear usage for session %d: %w", sessionID, err)
	}
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			rows.Close()
			return totals, "", "", nil, fmt.Errorf("scan cleared usage day: %w", err)
		}
		daySet[day.UTC()] = true
	}
	if err := rows.Err(); err != nil {
		return totals, "", "", nil, fmt.Errorf("clear usage for session %d: %w", sessionID, err)
	}
	rows.Close()

	// Merge before writing, so a caller that reports the same (day, model,
	// speed) twice produces one row whose cost matches its tokens. Doing this
	// with an ON CONFLICT sum instead would leave the stored amount priced for
	// only the first half of the row.
	merged, err := mergeUsageBuckets(buckets)
	if err != nil {
		return totals, "", "", nil, err
	}

	// Accumulated per currency so a session that mixes them is detected
	// rather than silently added up.
	byCurrency := map[string]*microAmount{}

	// The rows are collected into parallel arrays and written by one INSERT:
	// pricing is per bucket, but the write need not be, and this is the
	// heartbeat path.
	w := usageRowArrays{}
	for _, b := range merged {
		price, err := modelPriceFor(ctx, tx, b.Model, b.Speed, b.Day)
		if err != nil {
			return totals, "", "", nil, err
		}
		var amount *string // NULL when unpriced — deliberately not zero
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
			`INSERT INTO agent_session_usage
			   (agent_session_id, usage_day, model, speed, `+usageColumns+`,
			    cost_amount, cost_currency)
			 SELECT $1::bigint, u.usage_day, u.model, u.speed,
			        u.input_tokens, u.cache_write_5m_tokens, u.cache_write_1h_tokens,
			        u.cache_read_tokens, u.output_tokens,
			        u.cost_amount::numeric, u.cost_currency
			   FROM unnest($2::date[], $3::text[], $4::text[], $5::bigint[], $6::bigint[],
			               $7::bigint[], $8::bigint[], $9::bigint[], $10::text[], $11::text[])
			        AS u(usage_day, model, speed, input_tokens, cache_write_5m_tokens,
			             cache_write_1h_tokens, cache_read_tokens, output_tokens,
			             cost_amount, cost_currency)`,
			sessionID, w.days, w.models, w.speeds, w.input, w.cacheWrite5m, w.cacheWrite1h,
			w.cacheRead, w.output, w.amounts, w.currencies,
		); err != nil {
			return totals, "", "", nil, fmt.Errorf("record usage for session %d: %w", sessionID, err)
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

// usageRowArrays is one INSERT's worth of agent_session_usage rows, held
// column-wise so the whole set binds as arrays. cost_amount is carried as a
// decimal string (Cost's own output) and cast per element, so an unpriced
// bucket stays NULL rather than becoming a zero amount.
type usageRowArrays struct {
	days                              []time.Time
	models, speeds                    []string
	input, cacheWrite5m, cacheWrite1h []int64
	cacheRead, output                 []int64
	amounts                           []*string
	currencies                        []string
}

func (w *usageRowArrays) add(b SessionUsageBucket, amount *string, currency string) {
	w.days = append(w.days, b.Day)
	w.models = append(w.models, b.Model)
	w.speeds = append(w.speeds, b.Speed)
	w.input = append(w.input, b.Tokens.Input)
	w.cacheWrite5m = append(w.cacheWrite5m, b.Tokens.CacheWrite5m)
	w.cacheWrite1h = append(w.cacheWrite1h, b.Tokens.CacheWrite1h)
	w.cacheRead = append(w.cacheRead, b.Tokens.CacheRead)
	w.output = append(w.output, b.Tokens.Output)
	w.amounts = append(w.amounts, amount)
	w.currencies = append(w.currencies, currency)
}

// usageKey identifies one usage row within a session.
type usageKey struct {
	day   time.Time
	model string
	speed string
}

// mergeUsageBuckets normalizes each bucket's day and speed, drops the ones
// with nothing billed, and folds duplicates together. The result is ordered
// deterministically so a replay writes rows in a stable sequence.
func mergeUsageBuckets(buckets []SessionUsageBucket) ([]SessionUsageBucket, error) {
	byKey := map[usageKey]*TokenCounts{}
	var order []usageKey
	for _, b := range buckets {
		if b.Model == "" {
			return nil, fmt.Errorf("usage bucket needs a model: %w", ErrInvalidInput)
		}
		if b.Tokens.Input < 0 || b.Tokens.CacheWrite5m < 0 || b.Tokens.CacheWrite1h < 0 ||
			b.Tokens.CacheRead < 0 || b.Tokens.Output < 0 {
			return nil, fmt.Errorf("usage bucket for %s has negative tokens: %w", b.Model, ErrInvalidInput)
		}
		if b.Tokens.Total() == 0 {
			continue // nothing billed; not worth a row
		}
		if b.Day.IsZero() {
			return nil, fmt.Errorf("usage bucket for %s needs a day: %w", b.Model, ErrInvalidInput)
		}
		k := usageKey{
			day:   b.Day.UTC().Truncate(24 * time.Hour),
			model: b.Model,
			speed: NormalizeSpeed(b.Speed),
		}
		acc, ok := byKey[k]
		if !ok {
			acc = &TokenCounts{}
			byKey[k] = acc
			order = append(order, k)
		}
		acc.Add(b.Tokens)
	}

	sort.Slice(order, func(i, j int) bool {
		if !order[i].day.Equal(order[j].day) {
			return order[i].day.Before(order[j].day)
		}
		if order[i].model != order[j].model {
			return order[i].model < order[j].model
		}
		return order[i].speed < order[j].speed
	})

	out := make([]SessionUsageBucket, 0, len(order))
	for _, k := range order {
		out = append(out, SessionUsageBucket{
			Day: k.day, Model: k.model, Speed: k.speed, Tokens: *byKey[k],
		})
	}
	return out, nil
}

// projectForSessionTx resolves the project a session's work belongs to, up the
// session -> lease -> task chain.
func projectForSessionTx(ctx context.Context, tx *sql.Tx, sessionID int64) (string, error) {
	var projectID string
	err := tx.QueryRowContext(ctx,
		`SELECT t.project_id
		   FROM agent_sessions s
		   JOIN leases l ON l.id = s.lease_id
		   JOIN tasks  t ON t.id = l.task_id
		  WHERE s.id = $1`, sessionID).Scan(&projectID)
	if err != nil {
		return "", fmt.Errorf("resolve project for agent session %d: %w", sessionID, err)
	}
	return projectID, nil
}

// recomputeProjectDailyCostTx rebuilds the rollup for one project on the given
// days, from scratch. Recomputing rather than adjusting is what keeps the
// rollup incapable of drifting away from the usage rows it summarizes: it can
// be rerun at any time, and a replaced session's old numbers cannot linger.
//
// Deliberately not filtered on tasks.deleted_at: the tokens were spent, and
// dropping a tombstoned task's usage would stop the rollup reconciling against
// agent_session_usage.
// Every affected day goes in one DELETE and one INSERT: this runs on every
// heartbeat that carries usage, and a session spanning midnight or a backfill
// replaying weeks would otherwise pay two round trips per day.
func recomputeProjectDailyCostTx(ctx context.Context, tx *sql.Tx, projectID string, days []time.Time) error {
	if len(days) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM project_daily_cost WHERE project_id = $1 AND usage_day = ANY($2::date[])`,
		projectID, days); err != nil {
		return fmt.Errorf("clear rollup for %s on %d day(s): %w", projectID, len(days), err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO project_daily_cost
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
		   FROM agent_session_usage u
		   JOIN agent_sessions s ON s.id = u.agent_session_id
		   JOIN leases l ON l.id = s.lease_id
		   JOIN tasks  t ON t.id = l.task_id
		  WHERE t.project_id = $1 AND u.usage_day = ANY($2::date[])
		  GROUP BY u.usage_day, u.cost_currency`,
		projectID, days); err != nil {
		return fmt.Errorf("rebuild rollup for %s on %d day(s): %w", projectID, len(days), err)
	}
	return nil
}

// ProjectCost reports a project's usage and cost per day over [from, to],
// inclusive on both ends, plus per-currency totals. A zero from or to is
// unbounded on that side. Days with no recorded usage are omitted rather than
// returned as zeroes.
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
		`SELECT usage_day, cost_currency, `+usageColumns+`,
		        cost_amount::text, unpriced_tokens
		   FROM project_daily_cost
		  WHERE `+where+`
		  ORDER BY usage_day, cost_currency`, args...)
	if err != nil {
		return nil, fmt.Errorf("read cost for project %s: %w", projectID, err)
	}
	return scanCostReport(rows, "project "+projectID)
}

// TaskCost reports a task's usage and cost per day over [from, to], inclusive
// on both ends, plus per-currency totals and the number of agent sessions
// that billed usage in the window. includeChildren widens the scope to the
// task's child_of descendants (spec 004 §6.1) — a container task holds no
// lease itself, so without it a container's cost always reads as zero.
// Returns ErrNotFound when taskID does not exist, so a typo'd id reports as
// an error rather than a silent zero. The scope is deliberately not filtered on
// deleted_at: this is a fetch by id (044 §4), and a tombstoned descendant's
// tokens were still spent.
func (s *Store) TaskCost(ctx context.Context, taskID string, includeChildren bool,
	from, to time.Time) (*TaskCost, error) {

	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM tasks WHERE id = $1`, taskID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("check task %s exists: %w", taskID, err)
	}

	scope := `WITH scope(task) AS (SELECT $1::text)`
	if includeChildren {
		scope = `WITH RECURSIVE scope(task) AS (
			SELECT $1::text
			UNION
			SELECT e.from_task FROM task_edges e JOIN scope sc ON e.to_task = sc.task AND e.type = 'child_of'
		)`
	}

	where := "l.task_id IN (SELECT task FROM scope)"
	args := []any{taskID}
	if !from.IsZero() {
		args = append(args, from.UTC().Truncate(24*time.Hour))
		where += fmt.Sprintf(" AND u.usage_day >= $%d", len(args))
	}
	if !to.IsZero() {
		args = append(args, to.UTC().Truncate(24*time.Hour))
		where += fmt.Sprintf(" AND u.usage_day <= $%d", len(args))
	}

	fromJoin := `FROM agent_session_usage u
		    JOIN agent_sessions s ON s.id = u.agent_session_id
		    JOIN leases l ON l.id = s.lease_id
		   WHERE ` + where

	// ::numeric(14,6) pins the six-digit shape: without it an all-unpriced
	// group's SUM renders as "0" and a mixed-scale sum as e.g. "1.5", instead
	// of the shape cost_amount always carries on the wire.
	rows, err := s.db.QueryContext(ctx,
		scope+`
		SELECT u.usage_day, u.cost_currency,
		       SUM(u.input_tokens), SUM(u.cache_write_5m_tokens), SUM(u.cache_write_1h_tokens),
		       SUM(u.cache_read_tokens), SUM(u.output_tokens),
		       SUM(COALESCE(u.cost_amount, 0))::numeric(14,6)::text,
		       SUM(CASE WHEN u.cost_amount IS NULL
		                THEN u.input_tokens + u.cache_write_5m_tokens +
		                     u.cache_write_1h_tokens + u.cache_read_tokens +
		                     u.output_tokens
		                ELSE 0 END)
		  `+fromJoin+`
		 GROUP BY u.usage_day, u.cost_currency
		 ORDER BY u.usage_day, u.cost_currency`, args...)
	if err != nil {
		return nil, fmt.Errorf("read cost for task %s: %w", taskID, err)
	}
	report, err := scanCostReport(rows, "task "+taskID)
	if err != nil {
		return nil, err
	}

	var sessions int
	if err := s.db.QueryRowContext(ctx,
		scope+`
		SELECT COUNT(DISTINCT s.id) `+fromJoin, args...).Scan(&sessions); err != nil {
		return nil, fmt.Errorf("count sessions for task %s: %w", taskID, err)
	}

	return &TaskCost{CostReport: *report, Sessions: sessions}, nil
}

// scanCostReport scans a usage_day/cost_currency query in the CostReport
// column shape — used by both ProjectCost (from the project_daily_cost
// rollup) and TaskCost (aggregated live from agent_session_usage) — into
// per-day rows plus per-currency totals. desc names the caller for error
// messages. Closes rows.
func scanCostReport(rows *sql.Rows, desc string) (*CostReport, error) {
	defer rows.Close()
	report := &CostReport{}
	totals := map[string]*costTotal{}
	var order []string
	for rows.Next() {
		var d CostDay
		if err := rows.Scan(&d.Day, &d.Currency, &d.Tokens.Input, &d.Tokens.CacheWrite5m,
			&d.Tokens.CacheWrite1h, &d.Tokens.CacheRead, &d.Tokens.Output,
			&d.Cost, &d.UnpricedTokens); err != nil {
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
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read cost for %s: %w", desc, err)
	}

	for _, c := range order {
		t := totals[c]
		report.Totals = append(report.Totals, CostTotal{
			Currency:       c,
			Tokens:         t.tokens,
			Cost:           t.amount.String(),
			UnpricedTokens: t.unpriced,
		})
	}
	return report, nil
}

type costTotal struct {
	tokens   TokenCounts
	amount   microAmount
	unpriced int64
}
