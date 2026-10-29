
`doc_versions` and `rule_versions` gain a `change` column, `edit` or
`normalize`, set by `bumpDocVersion` and `bumpRuleVersion` and shown by
`lode doc versions` and `lode rule show`. Normalizing an accepted rule or
document writes a new version with `change = 'normalize'`, and that version
is accepted on write, with no review task, when both checks pass:

1. extracting the new text gives exactly the inline references of the old
   text, with the same field, line and target;
2. outside the rewritten reference spans, the old and new text are
   byte-identical.

If either check fails, nothing is written for that node and the derive
command reports it as an error. A draft rule or document, a plan, and any
unversioned field are rewritten through their normal write paths.
