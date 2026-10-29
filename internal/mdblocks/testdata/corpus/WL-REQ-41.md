
`(*Store) ProjectCost` reads `project_daily_cost` and `project_daily_overhead_cost` for the window and full-outer-joins them per (`usage_day`, `cost_currency`), so a day with overhead only still produces a row. Each `CostDay` and `CostTotal` carries the combined total in its top-level `Tokens`, `Cost` and `UnpricedTokens`, plus overhead's own share nested under `overhead`:

```go
type CostOverhead struct {
	TokenCounts
	CostAmount     string `json:"cost_amount"`
	UnpricedTokens int64  `json:"unpriced_tokens"`
}
```

It is nested so its keys cannot collide with the combined totals. `TaskCost` carries no overhead share. Currency conversion between buckets does not exist. Both are summed within one currency.

The project cockpit's automation-boundary card shows the combined "Agent spend, 30 days" figure and one added line with the overhead share (`ui.CockpitCostTotal.OverheadCostAmount`). No surface lists overhead by session. The cockpit is in WL-SPEC-82.

