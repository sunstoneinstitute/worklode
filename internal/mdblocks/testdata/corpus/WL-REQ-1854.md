
`lode <kind> derive` rewrites an inline reference into its plain form only
when the reference has one meaning regardless of when it was written:

| Found | Rewritten to |
|---|---|
| An absolute Worklode URL (scheme and host), bare or as `[<ref>](<url>)` | `<ref>` |
| `[<other text>](<url>)` | `[<other text>](<ref>)` |
| A full section ref `<KEY>-SPEC-<n>#sec-<anchor>` whose stored row targets a rule (WL-RULE-1849) | `<KEY>-SPEC-<n>#rule-<m>`, keeping the spec as context |
| A full section ref whose stored row targets the doc | left as written |
| A rule ref with the infix its kind does not use | the infix `FormatRuleRef` picks |

Short forms (a bare doc number, a slug or path, a URL path with no scheme
and host such as `/<ref>`, and keyword forms such as
`spec <n>`, `spec <n> §<a>` and `ADR <n>`) still yield edges but are never
rewritten, because the number they name can mean a different document
depending on when the text was written (WL-REQ-1650).

References inside fenced blocks are neither rewritten nor extracted
(WL-REQ-1851). These are left as written, while each reference in them still
yields an edge:

- references inside inline code, including a code span that continues onto
  the next line, because they are often literal examples;
- references inside quotation marks, because they are usually a quoted
  example of a form;
- a section range or list (`§3–§4`, `§3, §5`), because rewriting one part
  would leave the rest in another form.

A section ref is rewritten from its stored row, never by resolving the anchor
again at derive time, because the arrangement may have moved since the text
was written. A rewrite never changes which target a reference resolves to, and
never adds or removes a line.
