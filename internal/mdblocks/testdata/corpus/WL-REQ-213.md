
A component is a trusted element inside a slide's prose, written as a PascalCase custom element in the markdown:

```markdown
<Compare left="Lead-time test" right="As commissioned" step="1">

- **Left:** needs only the published commissioning date

</Compare>
```

A registry in `internal/ui` holds one entry per element: allowed attributes with anchored patterns, and whether it may hold children. The registry feeds both bluemonday's allowlist and the vendored upgrade script `/assets/deck-components.js`, which finds each registered element by tag name and renders it in the browser. Rules:

- Tag matching is case-insensitive; the renderer restores the registered casing on output.
- The registry refuses any name that is an HTML element.
- Nesting is allowed where the entry says so; content between tags is ordinary markdown under CommonMark's HTML-block rule (tags on their own lines, blank line between tag and content).
- `step` (`\A[0-9]+\z`) is allowed on every registered element and maps to reveal's fragments.
- Never allowed on any element: `class`, `style`, `id`, any `on*` handler, any URL attribute except through the existing `linkHref` and `blobSrc` patterns.
- An unregistered element or unlisted attribute is stripped. The registry is closed; adding an entry changes this section and adds its test.

The first entry is `<Decision key="…">`: no children, one attribute matching a row `key`, rendering that row's question and widget inline. A `mermaid` fence is a code block and is unchanged.

