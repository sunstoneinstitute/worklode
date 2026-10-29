
Without `--inline`, `lode show` prints the document with ref resolution. With `--inline` it folds in, under each section, the rules that amend the section's rule:

- A rule that `amends` it is inlined beneath it, its headings flattened to bold lines so borrowed text cannot reshape the outline.
- Only in-force amendments are inlined (§2.3). A pending one is listed as `> Pending <rule> (not yet effective)`. A withdrawn one is left out.
- Inlining is transitive: an amendment that is itself amended is expanded, to a depth of 8, with a visited set per expansion path so a mutually amending pair stops.
- Every inlined block leads with its attribution on its own line, naming the acting rule and where it is arranged: `**[amending WL-RULE-<b> (WL-SPEC-<m>#sec-<y>)]:**`. The marker is also the citation.
- Multiple amendments of one section render in section order, then by the amending rule's project key and number.
- The output opens with a comment naming the document and stating that it is a consolidated view, never a source document. Nothing writes it back.

`--section` prints one section with its whole subtree; a claim against a parent is a claim against what it contains. The same rendering backs the cockpit's document page.

