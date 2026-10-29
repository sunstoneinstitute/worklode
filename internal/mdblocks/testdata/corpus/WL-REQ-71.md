
Claim time is the one moment a human is guaranteed present. The claim hook (WL-SPEC-80) runs this after lease and worktree bind and before brief injection:

1. Fetch the catalog and resolve declared plus baseline names to `op://` refs.
2. Consent: non-baseline entries are listed once each, by name and description, and the operator answers one yes/no for the set. Decline means no materialization; the claim still succeeds. When there is no operator to ask (`--json`, or stdin is not a terminal), nothing is asked and nothing is recorded: materialization is deferred until `lode work resume` runs in a terminal.
3. Write `.worklode/secrets.env` in the worktree in `op run` env-file format, references only. A plain entry contributes `NAME=op://…`; a templated entry contributes one line per credential under the item name `<NAME>__<PLACEHOLDER>`.
4. Run `op run --env-file .worklode/secrets.env -- lode secret pack`. One `op run` resolves every reference under one 1Password authorization. `lode secret pack` writes each resolved value into the OS keystore (WL-SPEC-74#sec-10.4a), one item per env-file line, and exits. Values never touch disk or the shell.
5. The backbone logs a `secrets_materialized` event with task, actor and entry names only. Values, item names, templates and refs never enter the event log.

Re-materialization: `lode work resume` checks the keystore and re-runs the ceremony if items are missing or the declaration changed, which also propagates catalog template edits.

