
A move into the `completed` task state is refused when any pull request is attributed to the task (a `github_pull_requests` row with its `task_id`), whatever that pull request's state. `completed` is for work whose outcome is not code, and a task with a pull request closes through the merge, or through `lode task abandon` when the code is dropped.

The check runs inside `Transition` when the target is `completed`, so `lode task complete`, the API and MCP all hit it from every from-state, `ready` included. The refusal is `ErrBadTransition`, and its message names the pull requests (`<repo>#<number>`) and says to merge or abandon instead.
