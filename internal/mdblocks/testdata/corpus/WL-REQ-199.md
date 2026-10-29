
`<PROJECTKEY>-<TYPE>-<n>` resolves by its key, never by caller choice:

| Tier | Key | Resolved by | Unresolvable is |
|---|---|---|---|
| 1 | the current project (`project_key` in `.worklode/config.toml`) | this project's `docs` rows | a defect |
| 2 | another registered project | that project's `docs` rows, via the project list's key→project mapping | a defect |
| 3 | a key no registered project carries | shape validation alone | `unresolved: project <KEY> not known here`, exit code unaffected |

`<TYPE>` is checked against the target's `kind`; a mismatch is a defect. `.worklode/config.toml` carries `current_project` and an optional `project_key`; a checkout without the key still resolves through the backbone. The resolver never reads another repo off disk. A colon-form reference such as `rdf-registry:ADR-0006` parses under no tier and is reported `unresolved`.

