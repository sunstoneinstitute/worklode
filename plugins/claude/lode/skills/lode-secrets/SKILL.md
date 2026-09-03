---
name: lode-secrets
description: Use when writing Worklode plans or executing Worklode tasks that involve credentials, API keys, kubeconfigs, tokens, or other secrets - declaring catalog secret names on tasks at planning time, and running credentialed commands via `lode secret exec` at execution time. Also use when a command fails for lack of a credential inside a Worklode worktree.
---

# Worklode task secrets

Tasks declare the secrets they need by symbolic name (spec 017). Values are
materialized into the OS keystore at claim time; you never see or handle them.

## Writing plans

- Every plan task lists the catalog secret names its executor will need.
  Browse them with `lode secret catalog`.
- Put the names on the task: `lode task add --secrets NAME1,NAME2 ...`.
- A needed secret with no catalog entry is a plan-level finding. The entry is
  added to the `worklode-secrets-catalog` 1Password item by an operator with
  access to it — there is no PR to open and no repo file to edit, so do not
  attempt it yourself. Mint a task and block, per "Executing tasks" below. Do
  not invent names — they are org-unique and env-var style
  (`^[A-Z][A-Z0-9_]*$`), and loader-sensitive names are rejected: nothing
  starting `LD_` or `DYLD_`, and not `PATH`, `IFS`, `ENV`, `BASH_ENV`,
  `PYTHONPATH`, `NODE_OPTIONS`, `CLASSPATH` and friends (ADR 047).
- A catalog entry holds a credential, not a whole credentialed asset — on
  macOS and Windows a keystore item is capped at ~2.5-3 KB, so an asset like a
  full kubeconfig has to be split into a plaintext template plus the client
  credential that actually needs protecting.

## Executing tasks

- Run credentialed commands via `lode secret exec -- <command> [args...]`
  from inside the task worktree. The command's environment gets exactly the
  task's materialized names, plus the shell plumbing (`PATH`, `HOME`, locale).
  Credential-shaped variables from the operator's own shell — `AWS_*`,
  `ANTHROPIC_API_KEY`, anything containing `TOKEN`/`SECRET`/`PASSWORD` — are
  stripped (ADR 050), so a command that only worked because such a variable
  was exported now fails. A missing *credential* is a missing declared secret,
  handled below. A stripped non-credential — `AWS_REGION` and its kind go with
  their namespace — is a report that the pattern is too broad: say so and
  block, do not re-export it and do not declare it as a secret.
- `lode secret status` shows declared vs materialized names.
- NEVER probe `op`, ask the operator for a value, or read
  `.worklode/secrets.env` expecting values — it holds `op://` references only.
- Items survive leaving the worktree — the lease is still yours. Only `lode
  work submit`, `lode work block` and worktree removal purge them;
  `lode secret purge --task <id>` is the manual escape hatch.
- A needed-but-unavailable secret is a BLOCK signal, not something to work
  around. `lode work block` takes a blocking task id, so mint one and block on it:

  ```
  lode task add --title "Add catalog entry NAME to the secrets catalog" --kind chore
  lode work block --on <the id printed by the previous command>
  ```

  Then stop. Do not retry, do not improvise.

This file contains no `op://` references and no values, by design.
