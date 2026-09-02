# Spec sections needing planning — audited

Supersedes the raw `lode doc todo` sweep from 2026-08-28: that pass listed
every `unplanned`/`partial` section across all 33 specs (518 sections) with
no covering plan document. Before minting tasks, each of those sections was
checked against the actual codebase (12 parallel Explore agents) to separate
"no plan exists" from "no plan exists, and it's also not built" — most turned
out to already be implemented without ever going through a formal plan.

**`lode doc todo`/`lode doc list --needs-planning` now report the audited
state directly** — WL-PLAN-106 ("Retroactive coverage backfill", `lode show
WL-PLAN-106`) records every already-shipped or purely-procedural section as
`coverage: full`/`none`, so the two commands agree with the table below
instead of still flagging 518 sections. Its body explains the mechanics
(canonical corpus paths, the `WL-SPEC-N` shorthand gap) for anyone extending
it. It was backbone-only from the start, which is now true of every document
(055).

**16 specs had genuine, unplanned gaps.** A `design`-kind planning task was
minted for each (WL-387 through WL-402); WL-398 (spec 046) blocks on WL-397
(spec 045), since the rule engine depends on the workflow model landing
first.

| Task | Spec | Real gap |
|---|---|---|
| WL-387 | 001-identity-and-authentication | GitHub account-linking routes, `lode auth`, `github_user_tokens` rework, `minted_by` |
| WL-388 | 006-knowledge-graph | SHACL/owlrl CI gate, Deliverable/Effect RDF projection, RDF-1.2 pinned-version, unwired IRI grammar, `observed/repo-implements` deriver, prod graph-server |
| WL-389 | 007-drift-and-overview | scheduled deriver runs, unimplemented-section query, `lode drift --docs` / web spec-status view |
| WL-390 | 025-documents-in-the-backbone | reviewer/approval gate, fixer/escalation-ladder subsystem, `decision` task kind, doc-lifecycle events, DCAT projection, amends/amendedBy CI check |
| WL-391 | 026-design-doc-queries | `lode decompose` command, decomposition-stamp semantics |
| WL-392 | 037-vendored-design-skills | vendored skills tree, version pinning, assay/grilling skill, `lode doc fetch` |
| WL-393 | 038-worklode-in-a-cloud-sandbox | egress/network-policy baseline |
| WL-394 | 040-corpus-indexing-and-hybrid-search | embeddings config, chunking budgets, dense+lexical+RRF hybrid search |
| WL-395 | 041-pi-agent-integration | entire spec unbuilt |
| WL-396 | 042-secret-templates | entire spec unbuilt |
| WL-397 | 045-per-project-workflows | entire spec unbuilt (blocks WL-398) |
| WL-398 | 046-workflow-rule-engine | entire spec unbuilt (blocked on WL-397) |
| WL-399 | 054-agent-actors | `janitor` actor, `minted_by` column |
| WL-400 | 055-documents-leave-the-tree | corpus cutover itself: duplicate plans, cache path, retiring pre-commit hooks |
| WL-401 | 056-nav-shell-and-cross-project-inbox | entire spec unbuilt |
| WL-402 | 057-definition-of-done | entire spec unbuilt |

## Specs with no genuine gap (skipped)

Everything else in the original 33-spec list turned out DONE, N/A
(procedural sections with no build artifact), or PARTIAL only in ways the
spec itself already marks as deferred/v2: 004, 005, 008 (only gap is the
v2-labeled OTLP receiver), 012, 013, 016, 017, 019, 020, 021 (only gaps are
spec-labeled v2 items), 022, 029, 032 (only gap is a placeholder UI card for
a v2 policy engine), 039 (already has a draft plan), 044, 052, 053 (only gap
is optional compat-shim subcommands).

`lode doc todo <spec>` no longer reports these as `unplanned` — see WL-PLAN-106
above.
