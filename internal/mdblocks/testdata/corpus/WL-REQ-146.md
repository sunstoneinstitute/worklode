
`Transition(tx, now, taskID, from, to, eventID)` checks membership of the edge and that the task's current state equals `from`, atomically, appending `state_log`. Membership is:

```
legal(task, from, to) = coreEdge(from, to) OR (from, to) in workflowFor(task).entries
```

`workflowFor(task)` is resolved inside the same transaction. Wrong from-state and illegal edge both return `ErrBadTransition`. The error names the governing workflow when the edge exists in the vocabulary but the workflow does not declare it.

