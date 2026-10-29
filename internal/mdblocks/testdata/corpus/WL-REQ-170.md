
```
draft --(last approval on the submitted version, or lode doc accept by the owner)--> accepted --> superseded
draft --> superseded
accepted --(lode doc withdraw)--> withdrawn
plan: accepted <--> stale, accepted --> executed
```

| Status | Applies to | Meaning |
|---|---|---|
| `draft` | document | editable |
| `accepted` | document | passed review at a point in time. No promise it will be built |
| `superseded` | document | every rule it contains is withdrawn and has a successor. Set at accept (WL-SPEC-77#sec-9a). Stays readable and linkable |
| `stale` | plan | unexecuted plan covering a rule that was withdrawn or whose section was amended in place. Stored, set by the path that withdrew or amended it, cleared by re-acceptance |
| `withdrawn` | document | closed without a successor. Only an accepted or stale document is withdrawn |
| `executed` | plan | every task it minted has closed. Set by the store, terminal |
| `patched` | section | approved text modified in place since approval |

`withdrawn`, `superseded` and `executed` documents are hidden from `lode doc list` and the cockpit list unless `--status all` or a status is named.

`proposed`, `in_review` and `implemented` are not document statuses. "Under review" is a draft with an open `review` task. Submission is an event with no status column.
