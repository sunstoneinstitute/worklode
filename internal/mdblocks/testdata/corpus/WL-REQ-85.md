
Each repo mapping carries a `done_state`: `merged`, `deployed_prod` or `released`. It is the state that counts as fully delivered for that repo, and it selects which delivery branch the resolver walks:

| `done_state` | Branch walked | Ignores |
|---|---|---|
| `released` | `merged → deployed_dev → released` | prod deploys |
| `deployed_prod` | `merged → deployed_dev → deployed_prod` | releases |
| `merged` | same as `deployed_prod` | releases |

A `done_state = merged` task still advances past `merged` when deploy facts exist, because `merged` is also the default for repos discovery has not profiled and a real signal outranks a default.

Discovery seeds `done_state` at `lode project repo add` through the GitHub App: prod environment gives `deployed_prod`, releases without a prod environment give `released`, neither gives `merged`. It is settable with `lode project repo edit <repo> --done-state`. Discovery never gates transitions. It runs only at `repo add`, so a repo that later gains a prod environment keeps its old value until set by hand. Without the App's **Actions: read** permission the environments call returns 403, discovery fails with a `discover repo done_state` warn log, and every repo keeps `merged`.

