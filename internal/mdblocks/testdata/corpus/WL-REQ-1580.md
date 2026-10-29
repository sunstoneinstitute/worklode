
Keystore: on macOS, one keychain item per secret, service `worklode:<task-id>`, account `<NAME>` or `<NAME>__<PLACEHOLDER>`, created by the `lode` binary so later unattended reads do not re-prompt. On Linux, a file encrypted to an ephemeral key held in ssh-agent (best effort).

Manifest: a 0600 file outside the worktree records, per materialized entry, its item names (purge's only enumeration), its exported env name, its template text, and, once rendered, the rendered file's absolute path. Persisting the template is what keeps exec offline.

