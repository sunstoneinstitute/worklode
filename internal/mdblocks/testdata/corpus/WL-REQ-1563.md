
`store.Open` takes `store.WithMetrics(reg)`, which registers `collectors.NewDBStatsCollector(db, "worklode")` (the pool is capped at 16, waits are the saturation signal) and the store's domain metrics.

