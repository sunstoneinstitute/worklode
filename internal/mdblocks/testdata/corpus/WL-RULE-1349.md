
Every shape that crosses the HTTP boundary is declared once, in `internal/model`: entities, response projections and request bodies alike. `internal/store` scans rows into it, `internal/api` serializes it, `internal/cli` decodes it, `internal/ui` embeds it. The test for whether a type belongs there is whether its value crosses the wire. The Go field name is the wire name: `model.Task.Project` carries `json:"project"`, and the column name stays in the scan.

`internal/model` imports the standard library only, so every layer can depend on it and nothing depends back. A model type owns its wire invariants. For example, a nil slice the JSON must render as `[]` is filled by the store, since no conversion step exists to fix it.

Only these types stay package-local. The list is closed, and adding to it changes this section.

- `internal/ui` view types such as `BoardView`, `PageProps`, `TimelineRow`. They carry page-shell state and pre-formatted strings and are never serialized. They compose model types (`TaskView{Task model.Task}`) instead of restating their fields.
- `internal/store` scan plumbing: `rowScanner`, filter and argument builders.
- `internal/api` transport internals: `Subject`, session records, route guard entries.
- An `internal/store` private type where the stored shape and the wire shape cannot be one struct, recorded here with the field that diverged and why. None exists today.
- json-tagged structs serialized into a cookie, a state parameter or a local file, never into an HTTP body. Each is listed with its reason in the `allowed` map of `internal/model/modelrule_test.go`: `api.oauthState`, `api.cliIntent`, `api.stateChange`, and the four shapes of the CLI's on-disk remote cache.

An opaque stored payload passing through, such as a `state_log` change, is a `json.RawMessage`. A shape with variants is one flat struct discriminated by a `type` field, as `model.TimelineEntry` is.

Two tests enforce this.

| Test | Checks |
|---|---|
| `internal/model/deps_test.go` `TestModelImportsStdlibOnly` | `internal/model`'s transitive imports are standard library only |
| `internal/model/modelrule_test.go` `TestNoWireStructsOutsideModel` | `internal/api`, `internal/cli`, `internal/overview`: no json-tagged struct, named or anonymous, outside `allowed`, and no map literal handed to an HTTP body (`writeJSON`'s body or `(*Client).do`'s). `internal/cmd`: no anonymous json-tagged struct; its named ones are `--json` stdout contracts that cross no HTTP boundary. `internal/hooks`: the body rule only, since its declared types are GitHub's and Flux's inbound payloads. An `allowed` entry matching no declaration fails as stale, and a scanned package that parses no files fails. |
| `internal/model/modelrule_test.go` `TestModelDeclaresNoUntypedMaps` | no json-tagged field in `internal/model` is a `map[...]any`, directly or under a slice or pointer |

`TestGuardCatchesTheDodges` and `TestUntypedMapGuardCatchesTheDodges` run the checks over known-bad source so each rule still matches something. The checks do not see a body built by a helper that returns a map, or one marshalled to bytes before it reaches a body argument. Review catches those.

