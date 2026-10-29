
Not built: no `body_mode` column, deck renderer, layout or component registry exists. WL-971 plans it. Decision rows and `POST .../decide` (§6.4) are built. The design follows.

A `decision` task (WL-SPEC-77 §12) is the undertaking, and its `decisions` rows are the questions. A deck is that task's body rendered as slides, so the meeting's slides and its decisions are one object seen twice.

