
A `decisions` slide renders the task's own `decisions` rows in `position` order. It carries no question list. With `group:` it renders only rows whose `group` column matches. `<Decision key>` renders the same row, widget and answer anywhere in prose; a row shown twice is one row. A `key` naming no row renders a visible "no such question" marker. Adding a question is `lode decision add` on the task; the body never changes.

Each row's widget follows its own `response_type` (`yes_no` renders two radios, `single_select` its options with descriptions, `freetext` a text box, and so on for the six). The deck carries no response schema. Where a type takes notes, a Why control writes to `answer.notes`. A row shows its `question`; its `context` is hidden until expanded. A filled or hollow dot shows whether it is answered.

