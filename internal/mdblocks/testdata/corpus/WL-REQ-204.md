
```yaml
defers:
  - spec: WL-SPEC-<n>#sec-12
    to: WL-SPEC-<m>
```

| Key | Required | Value |
|---|---|---|
| `spec` | yes | a reference with a `#sec-N` fragment: the section handed off |
| `to` | yes | the owner document, whose plans are expected to cover the section. Usually a spec; a plan is legal when the successor exists. No fragment. |

Each entry asserts that this plan explicitly defers that section to that owner. The deferring plan contributes no coverage and the section stays a gap, reported `deferred` with its owner. Rules: section-scoped (a whole-document deferral is refused); the owner is mandatory; plan-only; never to itself; one owner per section per plan. A executed plan's deferral stands. Each entry becomes one `doc_edges` row of type `defers` with the owner in `owner_doc`, or verbatim in `owner_external` when it does not resolve; an unresolvable `spec` is kept verbatim as an external reference and reads as unplanned.
