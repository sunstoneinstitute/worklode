package model

// TokenCounts is the per-class token breakdown, shared by a cost report's
// daily rows and its window totals. See store.TokenCounts for why the
// classes travel separately rather than as one total.
type TokenCounts struct {
	InputTokens        int64 `json:"input_tokens"`
	CacheWrite5mTokens int64 `json:"cache_write_5m_tokens"`
	CacheWrite1hTokens int64 `json:"cache_write_1h_tokens"`
	CacheReadTokens    int64 `json:"cache_read_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
}

// CostDay is one day of a project's accounted usage in one currency.
// CostAmount is a decimal string for the same reason the agent session
// endpoints use one: numeric(14,6) does not survive a float64.
type CostDay struct {
	Day      string `json:"day"`
	Currency string `json:"currency"`
	TokenCounts
	CostAmount string `json:"cost_amount"`
	// UnpricedTokens are tokens whose model had no rate on file, so
	// CostAmount understates the bill by whatever they were worth.
	UnpricedTokens int64 `json:"unpriced_tokens"`
}

// CostTotals is the window total for one currency. Totals are per currency
// because summing across them needs a dated conversion rate the server does
// not own.
type CostTotals struct {
	Currency string `json:"currency"`
	TokenCounts
	CostAmount     string `json:"cost_amount"`
	UnpricedTokens int64  `json:"unpriced_tokens"`
}

// ProjectCost is a project's cost over a window: one row per day (ascending)
// plus per-currency totals. Currencies are never summed together.
type ProjectCost struct {
	Days   []CostDay    `json:"days"`
	Totals []CostTotals `json:"totals"`
}

// ProjectDetail is the wire form of GET /api/v1/projects/{id}: a Project
// plus its accounted cost. The list-shape fields are embedded so the two
// endpoints cannot drift apart.
type ProjectDetail struct {
	Project
	Cost ProjectCost `json:"cost"`
}
