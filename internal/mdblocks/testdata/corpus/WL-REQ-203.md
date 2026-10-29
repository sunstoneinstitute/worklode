
```yaml
covers:
  - WL-REQ-<a>
  - WL-SPEC-<n>#sec-3
  - WL-SPEC-<m>
```

Each entry is a requirement ref (`WL-REQ-<n>`), a `<doc>#sec-N` reference, which resolves to the requirements at that anchor and under it when the plan is written, or a whole-document reference, which resolves to every requirement the document contains. Invariants and informative rules in scope are skipped, and a direct ref to one is refused. After the plan executes, every requirement it covers is satisfied: there are no coverage levels. The plan's covered set is the union of its entries, so nested entries overlap harmlessly.

Three cases that look like partial coverage are expressed another way:

| Case | Expression |
|---|---|
| the plan builds part of a requirement | split the requirement (WL-SPEC-77 §19) so the plan covers whole requirements |
| the plan must obey a rule but builds nothing in it | the rule is an `invariant` and governs the plan's tasks already; no `covers` entry |
| the rule has nothing to build (rationale, context) | the rule is `informative`; no `covers` entry |

A `covers` entry carrying a `coverage:` or `fullCoverageWith:` key is refused. `implements` on a plan is read as `covers`; a plan carrying both keys is refused. Key order: lifecycle, `covers`, `defers`, dependency.

