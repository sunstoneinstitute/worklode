
`lode task reopen` also clears the task's `task_commits` in the same transaction, so the next webhook cannot resolve the task straight back to the state it left. Delivery has to be re-earned by new work landing.

