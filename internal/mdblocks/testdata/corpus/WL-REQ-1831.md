
`ref` arguments accept spoken forms ("spec seventy-seven", "task 512", "spec-77"), resolved against the call's `project` handle. A leading kind word wins over the task-id shape. `<PREFIX>-<n>` is read as a task id only when PREFIX is a project key. Anything else, such as the slug `rollout-2`, reaches the API's resolver as written.
