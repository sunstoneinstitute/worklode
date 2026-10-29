
Metrics live in a nil-safe `Metrics` in `internal/review/metrics.go` with every
label bounded by a `CHECK` or enum.

| Metric | Type | Labels or buckets |
|---|---|---|
| `worklode_review_rounds_total` | counter | `subject`, `client` |
| `worklode_review_threads_total` | counter | `intent` |
| `worklode_review_open_threads` | gauge | `subject` |
| `worklode_review_question_latency_seconds` | histogram | 5, 15, 60, 300, 900, 3600 |
| `worklode_review_time_to_verdict_seconds` | histogram | 300, 1800, 7200, 86400, 604800 |
| `worklode_review_verdicts_total` | counter | `verdict`, `reviewer_kind` in `human`, `agent` |
| `worklode_review_skimmed_blocks_total` | counter | `decided_by` in `author`, `policy` |
| `worklode_review_guide_skims_refused_total` | counter | `rule` |

