
Overhead has no lease and no task, so it cannot live in `agent_sessions` or `agent_session_usage`. Two tables hold it.

`project_overhead_usage`, one row per (project, agent, external session id, day, model, speed), replaced wholesale per (project, agent, external session id):

| Column | Type | Constraint |
|---|---|---|
| `project_id` | text | FK `projects(id)` ON DELETE CASCADE |
| `agent`, `external_session_id` | text | NOT NULL |
| `usage_day` | date | NOT NULL |
| `model` | text | NOT NULL |
| `speed` | text | `standard` (default) or `fast` |
| `input_tokens`, `cache_write_5m_tokens`, `cache_write_1h_tokens`, `cache_read_tokens`, `output_tokens` | bigint | NOT NULL, >= 0, default 0 |
| `cost_amount` | numeric(14,6) | NULL means no price on file for that model and day |
| `cost_currency` | text | `^[A-Z]{3}$`, default `USD` |

Primary key is the full six-column key. Index `project_overhead_usage_day` on `usage_day` supports the rollup recompute.

`project_daily_overhead_cost` is the derived rollup, keyed by (`project_id`, `usage_day`, `cost_currency`), with the five token columns, `cost_amount numeric(14,6) NOT NULL DEFAULT 0` and `unpriced_tokens bigint`. It is recomputed from scratch for every affected (project, day) when a report is replaced. It is its own table so neither rollup can absorb the other's rows and a reader can tell task spend from overhead at the storage layer. `model_prices` is shared. Overhead is priced by `modelPriceFor` exactly as task usage is.

