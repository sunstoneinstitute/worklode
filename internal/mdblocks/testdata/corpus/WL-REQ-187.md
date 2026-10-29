
For a requirement section `S` of an accepted spec, the query looks at every live plan with a `covers` edge that claims `S`, and every plan that `defers` `S`. A plan discharges `S` when its status is `accepted`, `superseded` or `executed`. Draft plans never discharge: an unapproved split must not hide a gap.

| Outcome | Rule |
|---|---|
| planned | some discharging plan covers `S` and no draft plan also covers it |
| plan-draft | a draft plan covers `S` |
| deferred | no plan covers `S`, and some discharging plan `defers` `S` to a named owner |
| unplanned | no plan covers `S` |

Rules:

- A `covers` edge means the plan builds the whole rule, so one covering plan discharges it. A rule covered by more than one plan counts as planned only once none of those plans is a draft: until then the rule is `plan-draft`. This is how a rule shared by plans that each build part of it is reported until it is split (WL-SPEC-77 §19).
- Only requirements are reported. An invariant or informative rule is never a gap.
- A deferral discharges nothing. It names who is owed the rest, and the report prints the owner beside the anchor: the owner's slug, or the reference verbatim when it did not resolve. A draft plan's deferral classifies nothing. A deferral is delivered when any plan discharges `S`; the owner is never a gate on who may deliver.
- An undischarged section reports the strongest outcome that applies: `plan-draft` over `deferred` over `unplanned`.
- A whole-document `covers` entry resolves to every requirement of the document when the plan is written, so it covers each of them.
- Overlap is legal: two plans covering one section is two plans touching it.
- A plan whose `covers` is `NO-SPEC` (§3.3) contributes to nothing and is never itself a gap.
- A deleted document takes part on neither end.

A spec is listed when at least one requirement section is not planned, with the gap count, the anchors, and each anchor's outcome:

```
WL-SPEC-<n>   accepted   3/9 need planning   sec-2.4(plan-draft) sec-4(unplanned) sec-6(deferred:<owner>)
```

Planning coverage and implementation coverage are different questions. `covers` says someone undertook to build a section; `implements` says the running code satisfies it (WL-SPEC-77 §13).
