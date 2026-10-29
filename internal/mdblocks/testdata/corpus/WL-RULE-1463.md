
The manual close is `lode task set state merged <id>`, which moves a task from `ready`, `in_progress` or `in_review` to `merged`. It runs the done check for the task's declaration, reports what it finds and ends the active lease.

