
Every user-editable field rendered as Markdown on a Worklode doc, rule, task,
milestone, deliverable, idea, decision or approval has the Go type
`model.Markdown` (a named `string`) in that kind's entity type and in its
write inputs, and no other field does. The fields are: doc title and body, doc
note body, rule heading and body, task title and body, milestone title,
deliverable description, idea title and body, decision question and context,
and approval note. A field's name in the `inline_refs` table is its JSON name
(`body`, `title`), or `note/<id>` for a doc note.

Checks: a model test walks the entity and write-input structs of those eight
kinds and fails when a `model.Markdown` field is missing from the store's list
of extracting write paths, or when a field listed here is a plain `string`.
Read-only view types are out of its scope.
