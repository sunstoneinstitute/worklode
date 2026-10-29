
Not built (see "Designed, not built").

Cmd+K (Ctrl+K off macOS) opens a jump box on every page. The actor types a
reference and Enter navigates to `/<ref>`, the canonical shortcut route.
Completion narrows in three stages, and every entry shows the item's title
beside its ref:

1. **Project key.** While the input holds no `-`, only project keys complete.
2. **Key and dash.** On exactly `<KEY>-`, the list holds one entry per
   document infix the project uses (`REQ`, `PLAN`, `SPEC`, `ADR`) and up
   to five tasks, preferring the newest task of each task kind.
3. **Narrowed.** Once a digit follows the dash (`WL-5`) the list holds tasks
   whose ref starts with the input. Once an infix and dash follow
   (`WL-SPEC-`) it holds documents or rules of that kind whose ref starts
   with the input.

Narrowed entries sort by the first tier that holds, newest first within a tier:

1. review tasks assigned to the actor
2. other tasks assigned to the actor
3. in-progress tasks the actor is working on
4. blocked tasks the actor owns
5. items in projects the actor leads
6. items in projects where the actor is on the crew
7. everything else the actor can read

A document enters tier 1 when the actor is one of its requested reviewers and
tier 2 when the actor owns it. Completion comes from one session-gated JSON
route under `permWebRead`, queried per keystroke after a short debounce. The
box follows the ARIA combobox pattern: arrow keys move the selection, Escape
closes it, and a query with no match says so.

