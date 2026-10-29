
Each command derives the nodes of its kind that its filters select:

| Flag | Effect |
|---|---|
| `[<ref>...]` | these nodes only |
| `--project <id>` | default: the current project |
| `--status <s>`, `--kind <k>` | the kind's own list filters |
| `--all` | every node of the kind in the project, no other filter |
| `--dry-run` | prints, per node, the edges it would add and remove (classified ones included), the rewrites it would make and unresolved references; writes nothing, and with `--classify` still calls the classifier |
| `--diff` | prints the field changes as a unified diff and the edge changes as `+`/`-` lines; writes nothing |
| `--classify` | also runs the `jev` classifier on references it has not classified |
| `--reclassify` | with `--classify`: also re-runs references whose newest row has an older `classifier_version` |
| `--stats` | the quality report; writes nothing |
| `--json` | machine output for any of the above |

The server routes are `POST /api/v1/projects/{id}/refs/derive` (body
`model.RefDeriveParams`) and `GET /api/v1/projects/{id}/refs/stats`. Each node
is written in its own transaction, so one failing node does not undo the
others.
