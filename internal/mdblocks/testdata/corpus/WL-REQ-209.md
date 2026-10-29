
Not built: no `serves:` key, intent class or intent query exists in the code. WL-970 plans it. The design follows.

An intent is a durable statement of an outcome that owns the specs written to reach it. It is the layer above specs.

| Level | Question | Kind |
|---|---|---|
| Intent | why at all | intent |
| Spec | what | spec |
| Plan | how | plan |
| Task | who, when | task |

An intent carries exactly three things: an outcome (one sentence, stated in the world), a boundary (what is deliberately excluded), and a status (`open`, `satisfied`, `abandoned`). It is neither a milestone (a date), a tag (cannot carry a boundary or be abandoned), nor a container task (groupings are queries). Intents are few: fewer than fifteen as a working ceiling.

