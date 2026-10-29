
WL-SPEC-79 §2 defines these terms as part of the `wl:` vocabulary; the table lists the ones plan coverage reads. They live in `ns/` (WL-SPEC-77 §14). All are `wl:layer wlc:execution`. A predicate is named so that the triple reads aloud as a true sentence: the plan covers the rule, the task produces the deliverable, the component implements the section.

| Term | Kind | Domain → Range | Says |
|---|---|---|---|
| `wl:covers` | ObjectProperty | Plan → Rule (a requirement) | intent: someone undertook to build all of it |
| `wl:ruleKind` | ObjectProperty | Rule → skos:Concept | the rule's kind, from `wlc:RuleKind`: `wlc:requirement`, `wlc:invariant`, `wlc:informative` |
| `wl:defers` | ObjectProperty | Plan → Section | explicit handoff of a section the plan will not build |
| `wl:Deferral` | Class | qualified `wl:defers`: `wl:deferringPlan`, `wl:deferredSection`, `wl:deferredTo` (DesignDoc ∪ Plan) | which document owns the handed-off section |
| `wl:produces` | ObjectProperty | Task → Deliverable | work: this is what makes it exist |
| `wl:implements` | ObjectProperty | Component → Section | evidence: the code meets the section |
| `wl:cutFrom` | ObjectProperty | Artifact → Commit | the commit a release was cut from |
| `wl:status` | ObjectProperty | (DesignDoc ∪ Plan ∪ Section) → skos:Concept | document status |

The qualified `wl:Deferral` node names both its ends and has no `Plan → Deferral` predicate. SHACL requires its plan to also assert the direct `wl:defers` edge. No `wl:Coverage`, `wl:fullyImplements` or `wl:completedWith` exist. `wl:Plan` is a top-level class disjoint from `wl:DesignDoc`. Issue and PullRequest reach a deliverable only through the task they are bound to. These triples are the contract for the document projector (WL-SPEC-79).

