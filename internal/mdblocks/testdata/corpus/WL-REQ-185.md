
Every query reads the backbone's `docs`, `doc_sections`, `doc_edges`, `rules` and `rule_edges` rows through the server. There is no offline corpus: a client that cannot reach the server gets an error, never a partial answer.

| Question | Answered from |
|---|---|
| What documents exist, and their status | `docs` rows |
| Which rules a plan covers | `doc_edges` of type `covers`, each from the plan to one rule. A covered rule claims every section that arranges it or a rule superseding it (the `covered_sections` view) |
| Which spec sections a plan defers | `doc_edges` of type `defers`, with the owner on the edge row |
| Whether a plan has been executed | the plan's status (`executed`) and the server-computed `closed` flag on the tasks its acceptance minted |
| What amends a section | `rule_edges` of type `amends` whose object is the section's rule |
| Whether an amendment is in force | the status of the amending rule (§2.3) |
| Whether a document is superseded or withdrawn | its `status` (WL-SPEC-77 §9) |

Task closure is never derived client-side from a state string. `model.Task` carries a server-computed `closed` boolean, evaluated with the repo's own done-state predicate (WL-SPEC-75), and every query here reads that flag.

`--needs-planning`, `--needs-execution` and `--bare-superseded` are selectors on `GET /api/v1/docs` (`?needs_planning=`, `?needs_execution=`, `?bare_superseded=`), computed in the store. `lode doc todo` walks documents fetched from the server in the CLI.
