
The Worklode inline reference scanner reads a `model.Markdown` field line by
line and returns one inline reference per occurrence of:

1. a task id `<KEY>-<n>`, where `<KEY>` is a key some project on this instance
   carries;
2. a node ref `<KEY>-<INFIX>-<n>` for every infix in the `internal/refkind`
   registry (`refkind.All`), with an optional `#sec-<anchor>` or
   `#rule-<n>` fragment on a doc ref (WL-REQ-1653).
   The scanner builds its pattern from the registry, so a new infix is
   scanned without a scanner change;
3. the keyword forms the cockpit autolinks (`spec <n> §<a>`, `ADR <n>`);
4. a Worklode URL in any shape the cockpit serves for a node, whether bare or
   as the target of a Markdown link.

Task kinds play no part in scanning: a task id carries no kind.

It skips fenced code blocks and scans inline code, because a ref written in
backticks is still a reference. A reference to the node itself is dropped. A
reference whose target does not resolve (WL-REQ-1357) produces no edge and is
reported by `lode <kind> derive --dry-run` as unresolved. The scanner is a
pure function of the field and the set of project keys, so the write path, the
derive command and the doctor check all report the same references.
