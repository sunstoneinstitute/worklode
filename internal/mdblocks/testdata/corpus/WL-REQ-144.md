
The optional states are the remaining four. The only edges a workflow chooses are the entries into them:

| Optional state | Legal entry edges |
|---|---|
| `in_review` | `in_progress -> in_review` |
| `deployed_dev` | `merged -> deployed_dev` |
| `deployed_prod` | `merged -> deployed_prod`, `deployed_dev -> deployed_prod` |
| `released` | `merged -> released`, `deployed_dev -> released` |

This table is the full set of legal entries and is a global invariant. `deployed_prod -> released` is illegal everywhere, and nothing can create `ready -> deployed_prod`. When a workflow declares an optional state and omits `transitions`, all entries from this table among its declared states are implied.

