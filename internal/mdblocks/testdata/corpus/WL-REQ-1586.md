
`lode task set state merged <id>` reads the current state and moves the task to `merged` from wherever it is, the way `abandon` does. It does not require `in_review`. `draft` and the terminal states refuse it. The same command accepts `deployed_dev`, `deployed_prod` and `released`, so a missed webhook can be reconciled by hand without widening the state machine (WL-REQ-84). It is the right close for work with no webhook (an admin UI change, a CMS edit). On a task with children it is an error naming the roll-up rule (WL-REQ-90).

