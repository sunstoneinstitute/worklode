
| Command | Purpose |
|---|---|
| `lode doc add --kind spec\|plan --slug <slug> --file <path> [--number] [--owner] [--update-section-anchors]` | author a draft. The input's header becomes rows (WL-SPEC-77#sec-7) |
| `lode doc edit <ref> [--file] [--title] [--issued] [--note\|--substantive] [--if-version]` | in-place edit of the body or a column, optional compare-and-swap, server-enforced substantive rules (WL-SPEC-77#sec-9f, WL-SPEC-77#sec-10b) |
| `lode doc link <ref> --<type> <target>`, `lode doc unlink <ref> --<type> <target>` | add or remove one of the document's outgoing edges (WL-REQ-1288, WL-SPEC-77#sec-9f) |
| `lode rule show`, `list`, `versions`, `edit`, `link`, `unlink`, `set`, `owner`, `tags`, `supersede` | read rules, edit one rule, write rule edges and metadata, apply a refactor map (WL-SPEC-77#sec-4h, WL-REQ-1355, WL-SPEC-77#sec-4i, WL-SPEC-77#sec-4j, WL-SPEC-77#sec-4o) |
| `lode task govern`, `lode task ungovern` | change a task's governing links by hand (WL-REQ-1356) |
| `lode doc submit <ref>` | emit `wl:DocumentSubmitted`, mint the review task |
| `lode doc set reviewers <actor>... <ref>`, `lode doc reviewers <ref>` | assign and read reviewers |
| `lode doc accept <ref>` | owner's manual commit. On a plan, mints its tasks |
| `lode doc revise <ref> [--file\|--accept\|--discard]` | open, update, land or discard a candidate revision |
| `lode doc withdraw <ref>` | close an accepted or stale document without a successor |
| `lode doc delete <ref>`, `lode doc undelete <ref>` | soft-delete and restore |
| `lode doc transfer <ref>... --to <actor> [--from <actor>]` | hand ownership |
| `lode doc note <ref>#sec-N --body` | attach a note to an anchor |
| `lode doc list [--kind --status --owner --needs-planning --needs-execution --bare-superseded --has-notes --unresolved --older-than --deleted]` | the map |
| `lode show <ref> [--inline] [--section sec-N]`, `lode show <rule> [--version <v>]` | read. `--inline` folds in-force amendments and supersessions |
| `lode doc show <ref> [--json] [--version <v>]`, `lode doc versions <ref>`, `lode doc sections` | structured sections with `edges`/`edges_in`, one past version, the version list, section listing |
| `lode doc lint [<file>]` | anchors and depth of a draft, or the corpus's dangling references |
| `lode doc resolve` | re-resolve stranded `covers` entries (WL-SPEC-77#sec-4p), admin |
| `lode doc todo <ref> [--deps]` | one spec's remaining work |
| `lode doc referrers <ref>#sec-N` | the in-place amendment gate's set |
| `lode doc progress` | spec progress (WL-SPEC-85) |
| `lode doc import [--dry-run]` | corpus import, admin |
| `lode graph drift`, `lode graph gaps` | drift and coverage gaps (WL-SPEC-77#sec-13b) |
| `lode task escalate --to plan\|spec --reason` | the ladder's human step |
| `lode event tail [--type --since --follow]`, `lode event subscribers`, `lode event seek <name> --to <offset>` | the log. `--follow` and `seek` are admin |

Reads are `GET /api/v1/docs`, `/docs/{id}` (with `?version=`), `/docs/resolve?ref=`, and `GET /api/v1/rules`, `/rules/{id}`, `/rules/{id}/versions[/{n}]`. Writes include `POST /docs`, `POST`/`DELETE /docs/{id}/edges`, `PUT /docs/{id}/edges` (import only), `/docs/{id}/submit`, `/docs/{id}/accept`, `/docs/{id}/revision` with `DELETE` to discard, `/docs/{id}/owner`, `POST /docs/covers/resolve`, `PUT`/`PATCH /rules/{id}`, `POST`/`DELETE /rules/{id}/edges`, `POST /projects/{id}/rules/supersede`, all through `routeGuards`.

Review is crit, with comments anchored to sections. The lode plugin ships skills for authoring, review, accepting, offering decomposition and plan review; every state change is one of the verbs in this table (WL-SPEC-81#sec-7). The cockpit's document views are in WL-SPEC-82.

