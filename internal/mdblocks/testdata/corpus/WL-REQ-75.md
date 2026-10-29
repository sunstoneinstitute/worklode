
| Condition | Behaviour |
|---|---|
| Catalog endpoint unreachable at claim | Claim succeeds; brief warns `secrets: catalog unavailable`; credentialed steps are blocked |
| Declared name not in catalog | Brief warning naming it; ceremony proceeds for the rest |
| Operator declines consent | No materialization; brief records the declined names; credentialed steps block |
| `op` not installed or not signed in | Ceremony fails fast at claim with an install or sign-in hint |
| Keystore read fails at exec | Exit non-zero naming the item; the agent blocks, never retries or works around |
| Lease expires, worktree remains | Items persist until the next exit with a definite "gone", a resume re-materializes, or removal purges |
| Template missing, placeholder set differs from `cred.` set, invalid UTF-8, or item-name collision | Catalog read fails server-side, 500 plus log naming the entry; claims degrade as "catalog unavailable" |
| A `cred.*` value exceeds the keystore cap at pack | `lode secret pack` fails naming the item; the fix is catalog modelling |
| Rendered-file write fails | Exec exits non-zero naming the path; block signal |
| Two entries export the same `env` for one task | Exec exits non-zero naming both entries |


