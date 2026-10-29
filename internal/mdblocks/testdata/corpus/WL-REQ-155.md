
```json
{
  "name": "release-on-merge",
  "on": { "to": "merged" },
  "when": { "kind": ["feature", "bug", "chore"] },
  "then": { "to": "released" }
}
```

| Field | Meaning |
|---|---|
| `name` | Slug `[a-z0-9-]{1,40}`, unique in the project. Never a metric label. |
| `on` | Trigger. `to` (required) names the state entered. `from` and `actor` optionally narrow it. Object form so later trigger types are additive. |
| `when` | Optional filters, all must hold: `kind` (task kinds), `workflow` (governing workflow names). Deliberately small. |
| `prompt` | Optional prose condition, at most 2000 characters, evaluated by the LLM. Required when `then.choose` has more than one target. |
| `then` | Exactly one of `to` (one target) or `choose` (2 to 5 candidate targets, LLM-picked). A single-element `choose` with a `prompt` means "fire only if the model confirms". |

Every automation names its edges statically: source `on.to`, targets from `then`. Nothing at evaluation time can change which edge an automation is about.

