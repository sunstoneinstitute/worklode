---
type: llm
weight: 1
---

The answer must say the reference is not fine: a filename does not resolve,
it silently stays an unresolved reference instead of an edge. It must offer a
form that resolves (the document slug, the `<KEY>-SPEC-<n>` shorthand such as
`WL-SPEC-42`, or the bare number `42`, optionally with `#sec-N`), and say how
to verify (`lode doc lint`, or `edges_in` from `lode doc show <ref> --json`).
