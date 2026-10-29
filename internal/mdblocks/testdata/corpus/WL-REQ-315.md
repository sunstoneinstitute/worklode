
`lode show` infers an id's kind from its shape or takes it from a flag. The type segment of a typed id (`<KEY>-<TYPE>-<n>`) is the dispatch key; an id with no type segment is a task. The typed grammar is checked before the task grammar, so a document reference never parses as a task id.

| Argument shape | Routed to |
|---|---|
| `12`, `WL-12` | `task show` (bare numbers per §6.4) |
| `WL-SPEC-77`, `WL-ADR-7`, `WL-PLAN-4`, optional `#sec-...`; a document slug or corpus path | the document renderer (WL-SPEC-77) |
| `WL-MILE-2` | the milestone |
| `WL-DEL-3` | the deliverable |
| `WL-REQ-<n>` (also `WL-RULE-<n>`) | the rule; `--version <v>` reads one version |
| any other `<KEY>-<TYPE>-<n>` | an error listing the known type codes |

Explicit flags: `--spec 15`, `--adr 7`, `--plan 4`, `--milestone 2`, `--deliverable 3`, `--task 12`, `--project <id>`, or the generic `--kind <K> <ordinal>` with `K` in `task|spec|adr|plan|milestone|project|deliverable`. Ordinal-taking flags take a bare ordinal, never a typed id. A per-kind flag and a positional id are mutually exclusive. For a spec or ADR, `--section <anchor>` narrows the render to one section and `--inline` folds in-force amendments and supersessions into the text.
