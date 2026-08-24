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
	// OverheadTokens/OverheadCost/OverheadUnpricedTokens are the portion of
	// the totals above with no task to bill to (spec 052): a main-checkout
	// orchestration session, or a worktree this actor no longer held the
	// lease on when it reported. Always zero for a TaskCost report, which
	// has no overhead concept.
	OverheadTokens         TokenCounts
	OverheadCost           string
	OverheadUnpricedTokens int64
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
	// OverheadTokens/OverheadCost/OverheadUnpricedTokens are the portion of
	// the totals above with no task to bill to (spec 052): a main-checkout
	// orchestration session, or a worktree this actor no longer held the
	// lease on when it reported. Always zero for a TaskCost report, which
	// has no overhead concept.
	OverheadTokens         TokenCounts
	OverheadCost           string
	OverheadUnpricedTokens int64
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

// ReportProjectSessionUsage records ONE session's COMPLETE usage across one
// project: every task it billed, keyed by task id, plus the remainder under
// the "" key -- turns with no task to bill to. It replaces the session's
// entire footprint in the project, task rows and overhead rows together, in
// one transaction.
//
// That whole-session scope is the point (spec 052 §2). The client re-parses
// its full transcript on every heartbeat and re-posts a running total, and a
// turn's destination can change between two reports: a directory that
// resolved to a task while its lease was held resolves to overhead once the
// lease is gone. Replacing per destination -- one key per agent_sessions row,
// another per project overhead row -- leaves the vacated side holding its
// copy, because no single write spans both, and ProjectCost sums them. There
// is no key here to leave behind: a session's tokens are written once per
// report, wherever they now belong, so cross-bucket duplication has no
// representation rather than being detected and corrected.
//
// A task whose agent_sessions row cannot be found -- this actor never opened
// one, or the lease was swept -- bills to overhead rather than being dropped.
// A task that HAD a row and is absent from byTask has its usage cleared, so a
// reclassification away from it takes effect instead of stranding rows.
//
// ErrInvalidInput for an unknown agent or an empty external session id;
// ErrNotFound for an unknown project.
func (s *Store) ReportProjectSessionUsage(ctx context.Context, projectID, agent, externalSessionID string, byTask map[string][]SessionUsageBucket) error {
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
		sessions, err := projectSessionRowsTx(ctx, tx, projectID, agent, externalSessionID)
		if err != nil {
			return err
		}

		overhead := append([]SessionUsageBucket(nil), byTask[""]...)
		billed := map[string]bool{}

		// Newest row per task wins; any older row for the same task is
		// cleared, so a task never holds usage under two leases at once.
		// applySessionUsageTx, not the bare replace, because the session
		// row's own headline totals and the project rollup are part of what
		// a usage write means.
		for _, row := range sessions {
			buckets := []SessionUsageBucket(nil)
			if !billed[row.taskID] {
				buckets = byTask[row.taskID]
				billed[row.taskID] = true
			}
			if err := applySessionUsageTx(ctx, tx, row.sessionID, buckets); err != nil {
				return err
			}
		}

		// A task named in the classification with no agent_sessions row to
		// write to still spent the tokens; overhead is where they land.
		for taskID, buckets := range byTask {
			if taskID == "" || billed[taskID] {
				continue
			}
			overhead = append(overhead, buckets...)
		}

		_, _, _, overheadDays, err := replaceProjectOverheadUsageTx(ctx, tx, projectID, agent, externalSessionID, overhead)
		if err != nil {
			return err
		}

		if len(overheadDays) > 0 {
			return recomputeProjectOverheadDailyCostTx(ctx, tx, projectID, overheadDays)
		}
		return nil
	})
}

// projectSessionRow is one agent_sessions row this session opened somewhere in
// the project, with the task its lease belongs to.
type projectSessionRow struct {
	sessionID int64
	taskID    string
}

// projectSessionRowsTx finds every agent_sessions row one external session id
// opened in a project, newest lease first. Released leases are included on
// purpose: their usage rows are still counted by recomputeProjectDailyCostTx,
// so a whole-session replace has to be able to reach and rewrite them.
func projectSessionRowsTx(ctx context.Context, tx *sql.Tx, projectID, agent, externalSessionID string) ([]projectSessionRow, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT s.id, t.id
		   FROM agent_sessions s
		   JOIN leases l ON l.id = s.lease_id
		   JOIN tasks  t ON t.id = l.task_id
		  WHERE s.agent = $1 AND s.external_session_id = $2 AND t.project_id = $3
		  ORDER BY s.started_at DESC, s.id DESC`,
		agent, externalSessionID, projectID)
	if err != nil {
		return nil, fmt.Errorf("find agent sessions for %s/%s in %s: %w", agent, externalSessionID, projectID, err)
	}
	defer rows.Close()
	var out []projectSessionRow
	for rows.Next() {
		var r projectSessionRow
		if err := rows.Scan(&r.sessionID, &r.taskID); err != nil {
			return nil, fmt.Errorf("scan agent session row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find agent sessions for %s/%s in %s: %w", agent, externalSessionID, projectID, err)
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
		        COALESCE(o.cost_amount, 0)::numeric(14,6)::text,
		        COALESCE(o.unpriced_tokens,0)
		   FROM t FULL OUTER JOIN o
		     ON t.usage_day = o.usage_day AND t.cost_currency = o.cost_currency
		  ORDER BY 1, 2`, args...)
	if err != nil {
		return nil, fmt.Errorf("read cost for project %s: %w", projectID, err)
	}
	return scanProjectCostReport(rows, "project "+projectID)
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
		d.OverheadCost = "0.000000" // TaskCost has no overhead concept (spec 052 §2)
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
			OverheadCost:   "0.000000",
		})
	}
	return report, nil
}

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

type costTotal struct {
	tokens   TokenCounts
	amount   microAmount
	unpriced int64

	overheadTokens   TokenCounts
	overheadAmount   microAmount
	overheadUnpriced int64
}
