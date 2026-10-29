
The gate is opt-in per repository. A repository opts in with a `[gate]` table
in `.worklode/config.toml` that names the paths it guards:

```toml
[gate]
paths = ["internal/cmd/**", "internal/model/**", "deploy/base/**", "ns/*.ttl"]
regex = ["^internal/store/(tasks|claim|ranking)\\.go$"]
trailer = "Spec:"
```

`paths` are globstar patterns and `regex` is for the cases a glob cannot say.
At least one of the two must be non-empty. `trailer` is the key the commit or
pull request body must carry (WL-REQ-7). It defaults to `Spec:` and must end in
a colon. Without a `[gate]` table nothing runs.

The list is a heuristic that decides whether the gate asks, never whether a
change is a "what" change. A repository picks the paths where "what" changes
concentrate. Everything outside the list merges without the gate asking.

