
An arrangement entry, a rule or a spec heading, stores its position and depth and nothing else about its place in the spec. Section numbers and anchors are derived when the spec is rendered, in reading order: the first entry at depth 1 is `1`, the first entry under it `1.1`, the next `1.2`, the next entry at depth 1 `2`. The anchor is the number with a `sec-` prefix, in Pandoc attribute syntax:

```markdown
## 2.1 Installation and setup {#sec-2.1}
```

Unnumbered sections take a slug (`{#sec-purpose}`). Heading text may be reworded freely. Adding, moving or unarranging an entry renumbers the entries after it, and nothing refuses that. What stays fixed is the rule: its ref never changes, and it leaves a spec only through `lode rule unarrange` (WL-REQ-1297) or `lode rule supersede` (WL-REQ-1646). `--update-section-anchors` on `lode doc add` and `lode doc edit` rewrites the numbers and anchors in an input file to agree with its heading structure.

**A section reference is a locator.** `<doc>#sec-N` is shorthand for the entry at that anchor. In `covers`, governing links and refs in rule text it resolves against the document's current version when it is written, and in a URL against the version the URL shows. It carries no identity. The rule ref is the identity, a rule's IRI is built from it (WL-SPEC-79#sec-10.2), and a rule's page (WL-REQ-1633) is the durable deep link to a section. The renderer prints the derived number and the rule ref on every rule heading (WL-REQ-1299).

**Cite rules by ref.** Prose, code comments, commit messages, pull request bodies and docs cite a rule by its ref (`WL-REQ-<n>`, `WL-RULE-<n>`), never by a spec section number. The hooks and the gate check enforce it (WL-REQ-1791).

**One model at every size.** Informative. A two-section spec and a hundred-section spec use the same rules, edges, plans and governing links. Nothing in the store keys on document size.

An entry with a slug is unnumbered, and so are the entries under it: each takes the anchor `<slug>.<n>`, counting from 1 in reading order, and no number.
