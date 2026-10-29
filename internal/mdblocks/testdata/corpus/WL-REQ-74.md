
Loaded in both contexts. When writing plans: every task lists the catalog names it needs; a needed secret with no catalog entry is a plan-level finding, fixed by adding the entry before the task is executable. When executing: run credentialed commands through `lode secret exec`; never probe `op`, ask the operator for values, or read `.worklode/secrets.env` expecting values. A needed but unavailable secret is a block signal, `lode work block` with a `missing-secret: NAME` reason. The skill contains no `op://` refs, so it is safe wherever skills live.

