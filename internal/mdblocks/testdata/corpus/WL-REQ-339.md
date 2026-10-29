
The Overview presents at most one primary **next decision**: its title, the
accountable actor, and what it rests on. Other concerns stay secondary: only
the highest-signal one is shown in the rail, without equal weight. A pinned
**focus** is a short note naming what the project is concentrating on now;
pinning changes orientation and ranking display only. Both are curated facts
on the project row, set and cleared with `lode project set decision <id>` and
`lode project set focus-note <id>` through `PATCH /api/v1/projects/{id}`
(`permProjectAdmin`), and the Overview renders each only when it is set. The
cockpit never displays a project-completion percentage. Progress is the state
of the decisions, deliverables, reviews, dependencies and observed facts that
define the project.

