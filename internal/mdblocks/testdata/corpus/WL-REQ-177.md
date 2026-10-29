
Three objects:

| Object | Home |
|---|---|
| Design content (rules) | backbone, projected to the graph |
| Work to produce it | `wl:Task` |
| "This repo at this commit satisfies rule X" | git, in `.worklode/implements.yaml` |

```yaml
implements:
  - rule: wlid:rule/worklode/94
    pinned:  /docs/WL-SPEC-75/2
    by:      [internal/store/lease.go, internal/store/sweeper.go]
```

The claiming component is derived from `by:` paths through `components.yaml`'s first-match mapping. The manifest has no `component:` field. Paths spanning several components split into one claim per component. A path matching no component is a fatal publication error. Every mapped repo has at least one component: with no `components.yaml` an implicit component whose IRI is the repo coordinates (`/components/github.com/sunstoneinstitute/worklode`) matches the whole repo, and promoting it to an explicit one leaves the IRI unchanged.
