
```
lode doc todo <ref> [--deps] [--json]
```

One ordered work list for a named document, joining the planning gap, the unexecuted plans, and the ordering between them. The walk goes spec → current sections → covering plans with their §1.3 outcome → the tasks each plan's acceptance minted and their `closed` flags. Every item is typed by the act that discharges it:

| Type | Condition | Discharged by |
|---|---|---|
| `unplanned` | no plan covers the section | writing a plan |
| `plan-draft` | a plan with `status: draft` covers it, or the named spec is itself a draft | a human accepting the plan or spec |
| `unexecuted` | covering plan accepted, a minted task open or none minted | executing the plan |
| `blocked` | covering plan accepted, a plan it `requires` is not discharged | the blocking plan |

Rules:

- A section whose rule is not a requirement yields no item. A deferred section yields no item.
- A draft covering plan suppresses the section's `unplanned` item. The pending act is accepting that plan.
- A document's `unplanned` items collapse to one item naming their anchors, ranked ahead of that document's plan items.
- A superseded covering plan is executed and yields no item.
- `blocked` keys on `requires`, the key plans actually write. Nothing keys on `blocks`.
- Items sort topologically over plan `requires`; within a rank, document order over the spec's sections breaks the tie, so two runs over an unchanged corpus agree exactly.
- `--deps` follows `requires` transitively. Without it the walk stops at the named document and a footer names the unfollowed edges. The `requires` graph may contain cycles; the walk marks visited documents and reports a cycle in the footer.
- A `draft` spec emits a `plan-draft` item against the document itself, ranked first, and the walk continues. An empty list means the spec is finished, and only a finished spec may print one.
- A `covers` target naming no document in the corpus is reported in the footer, since its sections read as unplanned.
- `NO-SPEC` is an error here, and so is a ref that names no document.
- The exit status is `0` whether or not work remains. `--json` emits the items plus the footer diagnostics as a sibling key.

The cost is one `GET /docs/{id}` per document, issued concurrently, and one task-closure request per project.
