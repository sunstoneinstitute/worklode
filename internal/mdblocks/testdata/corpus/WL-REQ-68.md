
Secret names match `^[A-Z][A-Z0-9_]*$` and are org-unique, never per-project, because a repo may serve several projects. The grammar also rejects loader-sensitive names at every gate: anything starting with `LD_` or `DYLD_`, and `PATH`, `IFS`, `ENV`, `BASH_ENV`, `PYTHONPATH` and the rest of the listed shell and runtime loading variables. The same grammar governs `cred.<PLACEHOLDER>` keys and `env` names. The one gate is `internal/secrets.ValidName`; the task `secrets` field, `lode secret pack`, the catalog parser, the manifest and the keystore all call it.

