---
type: llm
weight: 1
---

Pass only if the frontmatter has `status: draft` and `covers: NO-SPEC`.
Fail if `covers` is omitted, empty, or uses `implements`, or if it invents
keys such as `title`, `coverage`, `amends` or `replaces`.
