
`file(what, title, body?, parent?, doc?)` resolves `what` through `KindAliases` and calls the task, document or rule add route. "File this as a bug" becomes `file(what: "bug", ...)` and creates a task of kind bug. A word with no alias returns the closest kinds for Claude to read back, and creates nothing.

`task_add`, `doc_add` and `rule_add` stay as the typed paths. `task_list` and `doc_list` accept the same words as a kind filter ("show me open bugs").

