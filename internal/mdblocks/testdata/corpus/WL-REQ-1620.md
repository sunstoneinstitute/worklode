
Every write of the `automations` key validates the automations of WL-REQ-155, with errors naming the automation:

- At most 50 automations. Names valid, unique.
- `on.to`, `on.from` and every `then` state in the vocabulary.
- Every automation edge (`on.to`, target) is a core edge or a row of the entry table of WL-REQ-144. `ready -> deployed_prod` is unstorable.
- `when.kind` values are valid kinds. `when.workflow` values name workflows in the same object, or `default`.
- `prompt` present and non-empty when `choose` has more than one target.
- `then` has exactly one of `to` / `choose`; `choose` lists 2 to 5 distinct states (1 with `prompt`).

Edge legality is checked against the vocabulary-wide superset because the governing workflow is per task. The guard refuses the edge at fire time for any task whose workflow does not declare it.

