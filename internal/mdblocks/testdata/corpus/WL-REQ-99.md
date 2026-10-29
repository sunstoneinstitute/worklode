
A rally is a `kind = 'rally'` task. Its content is its `dependsOn` edges to its members. It is how a person overrides the computed order without editing every task's priority.

```
lode task add --kind rally --draft --title "Ship the cockpit"
lode task block <rally-id> --by <task-id>      # once per member
lode task publish <rally-id>                   # draft -> ready activates it
lode project rally [<project>]                 # the active rally and its members
```

A draft rally is inert. A rally is active when its state is neither `draft` nor closed. **At most one rally is active per project**, enforced by a partial unique index on `tasks (project_id)` over rows where kind is `rally` and state is not `draft`, `merged`, `deployed_dev`, `deployed_prod`, `released` or `abandoned`. Retiring a rally means closing it. Membership is the transitive open-blocker closure of the active rally, the same relation `lode task blockers` walks. The rally arm is soft: when every member is closed or leased, agents drift to other work.

