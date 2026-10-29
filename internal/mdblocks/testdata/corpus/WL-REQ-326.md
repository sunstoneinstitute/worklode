
`assay` finds facts itself rather than asking for anything it could look up. Sources in order: `lode show <ref>` for a document or section (`lode show WL-SPEC-77#sec-9`), then `.worklode/cache/docs/` (WL-REQ-327) when the backbone is unreachable.

The destination (a spec, a plan, a task body, or a loose idea) is named at invocation, because it changes the interview. Without one, the skill asks before the first round and stops if it cannot get one. When the frontier empties, the settled design tree is written to the destination: a spec or plan destination produces a conforming document (frontmatter, `{#sec-N}` anchors), a task-body destination writes through the CLI, an idea destination writes a file the user names. The skill never accepts a document and never syncs. Document authoring is described in WL-SPEC-77#sec-9.

