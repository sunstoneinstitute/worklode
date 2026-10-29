
All enums are controlled vocabularies mirroring a schema CHECK constraint. None is free text.

| Scheme | Members |
|---|---|
| `wlc:DesignDocStatus` | `draft`, `accepted`, `stale`, `superseded`, `withdrawn`, `executed`; order as data in `wlc:DesignDocStatusOrder` (`skos:OrderedCollection`) |
| `wlc:RuleKind` | `requirement`, `invariant`, `informative` |
| `wlc:EdgeOrigin` | `declared`, `inferred` |
| `wlc:TaskKind` | `feature`, `bug`, `chore`, `design`, `review`, `spike`, `decision`, `rally` |
| `wlc:DocKind` | `spec`, `plan` |
| `wlc:ModelLayer` | `intent`, `execution`, `runtime` |
| `wlc:ArtifactKind` | `docker_image`, `pypi`, `git_tag`, `binary` |
| `wlc:DeploymentStatus` | `pending`, `reconciling`, `deployed`, `failed` |
| `wlc:DeployTargetKind` | `flux_kustomization` (HelmRelease events file here too), `pypi_target`, `manual` |
| `wlc:RuntimeEventKind` | `crashloop`, `oom`, `flux_failure`, `flux_recovery` |

There is no `implemented` status; implementation is a per-section query (§11). There is no `under review` status; a document under review is a draft with an open review task. Transition rules live with the authoring skill. No task kind names a container; containment is inferred from `child_of`. `wlc:rally` is the one structural kind: a hand-assembled goal with no work of its own, never claimable, no children, blocks nothing. `wlc:pypi_target` is spelled apart from `wlc:pypi` because target kind and artifact kind are different concepts. Every minted term carries a `wl:layer` tag. Each concept in `wlc:TaskKind`, `wlc:RuleKind` and `wlc:DocKind` carries `skos:altLabel` entries for the words people say for that kind.
