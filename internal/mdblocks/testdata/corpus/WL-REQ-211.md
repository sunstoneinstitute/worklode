
A task body's front matter names its rendering engine under one key:

```markdown
---
mode: reveal
---
```

| Value | Renders as |
|---|---|
| `doc` (default) | a document |
| `reveal` | a slide deck through reveal.js |

An unknown value renders as `doc` and is reported by `lode doctor`. The mode picks which trusted renderer runs. Every fragment of author content still passes the same bluemonday policy (WL-SPEC-78#sec-8.6a). No mode introduces a markup path around it.

