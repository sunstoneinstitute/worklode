---
name: lode-secrets
description: Use when writing Worklode plans or executing Worklode tasks that involve credentials, API keys, kubeconfigs, tokens, or other secrets - declaring catalog secret names on tasks at planning time, and running credentialed commands via `lode secrets exec` at execution time. Also use when a command fails for lack of a credential inside a Worklode worktree.
---

# Worklode task secrets

Tasks declare the secrets they need by symbolic name (spec 017). Values are
materialized into the OS keystore at claim time; you never see or handle them.

## Writing plans

- Every plan task lists the catalog secret names its executor will need.
  Browse them with `lode secrets catalog`.
- Put the names on the task: `lode task add --secrets NAME1,NAME2 ...`.
- A needed secret with no catalog entry is a plan-level finding: add the
  entry via a deployment-repo PR before the task is executable. Do not invent
  names — they are org-unique and env-var style (`^[A-Z][A-Z0-9_]*$`).
- A catalog entry holds a credential, not a whole credentialed asset — OS
  keystore items are size-capped (~2.5-3 KB), so an asset like a full
  kubeconfig has to be split into a plaintext template plus the client
  credential that actually needs protecting.

## Executing tasks

- Run credentialed commands via `lode secrets exec -- <command> [args...]`
  from inside the task worktree. The command's environment gets exactly the
  task's materialized names.
- `lode secrets status` shows declared vs materialized names.
- NEVER probe `op`, ask the operator for a value, or read
  `.worklode/secrets.env` expecting values — it holds `op://` references only.
- A needed-but-unavailable secret is a BLOCK signal, not something to work
  around: run `lode block --on <blocker>` or record
  `missing-secret: NAME` and stop. Do not retry, do not improvise.

This file contains no `op://` references and no values, by design.
