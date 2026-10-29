
```sql
projects.default_deliverables text[] NOT NULL DEFAULT '{}'
tasks.deliverables            text[]           -- NULL = inherit
```

`text[]` because the list is ordered, short, and mixes two reference kinds. There is no CHECK constraint: a reference is valid only against the deliverables of its own project, which the store checks (`store.ValidDoneRef`). It rejects an unknown reserved word and a deliverable id from another project.

