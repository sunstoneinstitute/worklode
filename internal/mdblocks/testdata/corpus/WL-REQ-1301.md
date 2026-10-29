
One migration makes every spec match section 19.1 before stored spec bodies stop being read.

Each rule whose text is empty becomes a spec heading at the same position and depth, and the rule row and its versions are deleted, since they never held text. Before deletion, every `covers` edge to it is re-resolved to the rules grouped under the new heading, the same resolution a `<doc>#sec-N` entry gets (section 4). Every `governedBy` link to it moves to those rules at their current versions. A reference to the deleted rule's ref in rule text is reported, not rewritten.

Text before the first anchored heading, and each unanchored section within the anchor depth, becomes template text where it stood. Every `informative` rule is reclassified: a stance other rules refine becomes a `principle`, and the rest becomes template text at its position. Template text found inside a rule body (a "not built" line, a non-goals sentence, an overview paragraph) is moved to the spec at that position by a subagent pass over the report, since no mechanical test separates it. Each term a rule uses that no rule defines gets a `definition` rule in the project glossary, drafted from the glossary tables and accepted by the owner, before any `needs` edge is written.

The migration writes a report naming each heading it converted, each rule it created and each ref it could not rewrite, so an owner can reclassify a created rule (`lode rule set <ref> --kind requirement|catalogue|invariant|definition|principle`), split it under section 4c, or move a passage to template text. From then on section 19.1's write gate keeps specs to rules and spec headings.

