
Vendored skills are ordinary skills; provenance is metadata. Each carries an `upstream` frontmatter block naming `plugin`, `version` and `path`, and `merged-with` listing every other source folded in. A skill written from scratch has no `upstream` block, which is how the drift check tells the two apart. `plugins/claude/lode/skills/LICENSES.md` carries both MIT notices in full and per-skill provenance.

