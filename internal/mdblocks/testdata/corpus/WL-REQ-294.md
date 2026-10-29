
Worklode learns about GitHub through webhooks. When one never arrives, the backbone keeps a stale picture. Three commands cover the gap, split on a permission boundary.

| Command | Audience | Auth | Job |
|---|---|---|---|
| `lode doctor` | developer | none, works offline | is my setup correct |
| `lode project health [repo]` | operator | admin | is ingestion working for this repo |
| `lode task reconcile [--repo X \| --task Y] [--since D] [--dry-run]` | operator | admin | repair what ingestion missed |

Reconcile compares what the backbone recorded against what GitHub did. `lode graph drift` (WL-SPEC-79 §11) compares declared architecture against observed code; the two share nothing.

