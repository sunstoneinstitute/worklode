
| Kind | Class | Sections and anchors | Mutability after accept | Lifetime |
|---|---|---|---|---|
| `spec` | `wl:Spec` under `wl:DesignDoc` | yes, derived from the arrangement | revised (WL-SPEC-77#sec-9e) or amended in place under the ladder (WL-SPEC-77#sec-10a) | stays true after implementation, superseded, never consumed |
| `plan` | `wl:Plan`, sibling of `wl:DesignDoc` | none, nothing pins a claim to a plan | freely editable at any status | executed once every minted task closes |

The `adr` kind is retired. A decision is written into the spec owning the surface, and a reversal is an `amends` or `supersedes` edge on that section's rule. Every former ADR is a spec: its body opens with its old ref (`Was DP-ADR-2.`), `docs.former_adr` holds its old number, and WL-SPEC-77#sec-7aa resolves the old ref to it. No document, `docs.kind` value or ontology class is an ADR, and `lode doc add` and `lode doc import` refuse the kind.

A plan is a document rather than a task body so that review machinery (comment anchoring, revision tracking, the accept gate) is built once. Rationale worth keeping in a plan is promoted into the governing spec before the plan's tasks close.
