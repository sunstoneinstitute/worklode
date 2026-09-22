# Spec sprawl metrics: old corpus, new corpus, clause graph

Computed by `sprawl3.py` over three corpora. OLD is the 47 inline-exported WL-SPEC documents, unit a leaf section. NEW is the 11 documents of `docs/specs2/`, same unit. GRAPH is those same 11 documents read through `docs/specs2/ttl/`, unit a clause: 325 `wl:Clause` nodes plus 61 `wl:OpenQuestion` nodes, 386 retrieval nodes in all. Every GRAPH number below covers all 386 nodes unless a row says clauses only. OLD and NEW are computed exactly as `sprawl.py` computes them in `sprawl-metrics.md`, so the two reports agree. Tokens are words times 1.3 everywhere, taken from `wl:tokenCount` for GRAPH.

## Summary

- Size: OLD 47 docs, 749 leaves, 269,048 tokens. NEW 11 docs, 337 leaves, 88,357 tokens. GRAPH 11 arrangements, 386 nodes (325 clauses and 61 open questions), 85,474 tokens, median node 188 tokens and 76.2% of nodes inside the 100 to 800 band.
- M1 amendment load: OLD 23 effective and 45 pending markers on 5.7% of leaves, amendment text 0.1% of base text. NEW 3 effective markers on 0.6% of leaves. GRAPH 0 by construction, because an amendment there is a new clause version; the graph reports supersession coverage instead, 524 edges from 247 clauses onto 523 old sections, with 23 section-map rows unresolved.
- M2 topic dispersion: mean docs per topic OLD 28.3, NEW 9.4, GRAPH 10.1 arrangements per topic. The graph reading adds connectivity and it comes out weak: a topic's clauses fall into 48.6 connected components on average, with only 9.1% of the topic's clauses in the largest one, so today's edges leave most clauses of a topic unreachable from each other.
- M3 reading cost: summed over topics, whole-document load OLD 1,756,724 tokens at 3.4x the leaf text, NEW 698,451 at 4.3x. GRAPH loads 227,957 tokens at 1.2x when retrieval is the topic's clauses plus their 1-hop neighbourhood, against 716,891 at 3.8x if it still loaded whole arrangements. The 1.2x is a lower bound: it assumes the agent lands on exactly the tagged clauses, and the 1-hop closure is small because the current edges are sparse (refines links H3 to its H2 preamble only, most references target a whole document). With realistic edges the load sits between 1.2x and 3.8x.
- M4 cross-references: OLD 3.0 per 1k tokens on 45.0% of leaves, NEW 2.4 on 32.0%. GRAPH 2.9 typed edges per 1k tokens (references, constrains, conflictsWith) on 37.0% of nodes, plus 124 refines edges as structure; 76.7% of nodes have no inbound edge of any kind.
- M5 staleness: `lode` invocations not in `commands.txt` OLD 291 (62 distinct), NEW 22 (14 distinct), GRAPH 21 (13 distinct). GRAPH reads the same text as NEW, so the two agree up to the lines no clause covers.
- M6 redundancy: near-duplicate pairs across documents OLD 2 (0.5% of leaves), NEW 0, GRAPH 0 across arrangements and 0 inside one arrangement. The clause unit is finer than a leaf section, so the graph can test pairs inside one document that the NEW pass skips, and it still finds none.
- M7 chain depth: OLD longest amend chain 1 and 4 sections amended by two or more documents. NEW longest chain 1. GRAPH refines depth 2, supersession chain depth 1 (old section to new clause), largest supersession fan-in 13.
- Nodes matching no topic: OLD 22.0%, NEW 25.8%, GRAPH 26.9%.
- NEW and GRAPH are the same text. The difference between them is the retrieval unit and the edges, so M5 and M6 measure the text while M2, M3, M4 and M7 measure the structure laid over it.
- Reading M3: the ratio is how many tokens an agent loads per token it actually needed.

## Size

| Corpus | Documents | Units | Tokens | Unit min | Unit median | Unit p90 | Unit max | Units 100 to 800 tokens |
|---|---|---|---|---|---|---|---|---|
| OLD | 47 | 749 leaf sections | 269,048 | 27 | 230 | 614 | 4200 | 84.6% |
| NEW | 11 | 337 leaf sections | 88,357 | 11 | 200 | 469 | 1063 | 81.9% |
| GRAPH | 11 arrangements | 386 nodes | 85,474 | 10 | 188 | 449 | 949 | 76.2% |
| GRAPH, clauses only | 11 arrangements | 325 clauses | 84,100 | 22 | 213 | 474 | 949 | 90.5% |

GRAPH nodes are 325 `wl:Clause` plus 61 `wl:OpenQuestion`. Open questions are one bullet each, so they pull the distribution down; the clauses-only row shows the split rule's own result. The token size rule the split targets is 100 to 800.

## M1 Amendment load and supersession coverage

| Corpus | Effective markers | Pending markers | Units with 1+ | Amend tokens | Amend share of base |
|---|---|---|---|---|---|
| OLD | 23 | 45 | 5.7% | 231 | 0.1% |
| NEW | 3 | 0 | 0.6% | 80 | 0.1% |
| GRAPH | 0 | 0 | 0.0% | 0 | 0.0% |

GRAPH carries no amendment markers by construction: an amendment to a clause is a new version of that clause, so there is no folded marker to count. Supersession coverage is the comparable figure.

| Supersession measure | Value |
|---|---|
| `wl:supersedesSection` edges | 524 |
| Old sections covered | 523 |
| Clauses carrying 1+ old section | 247 |
| Clauses superseding exactly 1 | 142 |
| Clauses superseding 2 to 4 | 77 |
| Clauses superseding 5 or more | 28 |
| Largest fan-in on one clause | 13 |
| Old sections split over 2+ clauses | 1 |
| Section-map rows unresolved (residue) | 23 |
| Dispositions | moved 342, merged 180, pointer 2 |

## M2 Topic dispersion

| Topic | OLD docs | NEW docs | GRAPH arrangements | GRAPH clauses | GRAPH components | GRAPH largest component |
|---|---|---|---|---|---|---|
| data-model | 42 | 11 | 11 | 95 | 74 | 14.7% |
| api | 34 | 11 | 11 | 61 | 52 | 8.2% |
| cli | 18 | 8 | 11 | 61 | 47 | 9.8% |
| ui | 16 | 7 | 8 | 27 | 24 | 7.4% |
| identity-authz | 28 | 10 | 11 | 81 | 56 | 6.2% |
| deployment-ops | 23 | 6 | 6 | 19 | 19 | 5.3% |
| background-jobs | 26 | 11 | 11 | 60 | 49 | 6.7% |
| agent-harness | 30 | 11 | 11 | 85 | 58 | 14.1% |
| design-process | 38 | 10 | 11 | 94 | 58 | 9.6% |

Mean docs per topic: OLD 28.3, NEW 9.4. Mean arrangements per topic on GRAPH: 10.1. Mean components per topic: 48.6. Mean largest-component share: 9.1%. Token-weighted dispersion share: OLD 82.7%, NEW 74.7%, GRAPH 76.0%.

Fewer components for the same clause count would mean the topic hangs together, because a reader who lands on one clause could walk to the rest of the topic along refines, references, constrains and conflictsWith edges. The current graph is far from that. Most topic clauses are isolated points, because the only dense edge type is `wl:refines`, which links an H3 clause to its H2 preamble and to nothing else, and because many `wl:references` edges point at a whole arrangement rather than at a clause.

## M3 Reading cost per topic

| Topic | OLD whole docs | OLD ratio | NEW whole docs | NEW ratio | GRAPH clause tokens | GRAPH 1-hop tokens | GRAPH 1-hop ratio | GRAPH whole-arrangement ratio |
|---|---|---|---|---|---|---|---|---|
| data-model | 258,743 | 2.5x | 88,357 | 2.8x | 32,998 | 39,110 | 1.2x | 2.6x |
| api | 225,502 | 4.8x | 88,357 | 4.7x | 19,304 | 25,019 | 1.3x | 4.4x |
| cli | 145,277 | 6.1x | 67,482 | 8.1x | 19,203 | 24,463 | 1.3x | 4.5x |
| ui | 119,675 | 7.0x | 61,996 | 8.9x | 8,868 | 10,688 | 1.2x | 7.8x |
| identity-authz | 198,014 | 3.1x | 82,476 | 3.8x | 23,062 | 27,836 | 1.2x | 3.7x |
| deployment-ops | 180,364 | 6.7x | 51,204 | 8.2x | 6,147 | 6,846 | 1.1x | 8.1x |
| background-jobs | 191,896 | 3.9x | 88,357 | 4.4x | 22,197 | 26,177 | 1.2x | 3.9x |
| agent-harness | 193,446 | 2.6x | 88,357 | 4.2x | 26,390 | 29,943 | 1.1x | 3.2x |
| design-process | 243,807 | 2.2x | 81,865 | 2.8x | 31,530 | 37,875 | 1.2x | 2.7x |
| total | 1,756,724 | 3.4x | 698,451 | 4.3x | 189,699 | 227,957 | 1.2x | 3.8x |

Per node: reading one node with its refines ancestors costs 259 tokens on average, against 221 tokens for the node alone and 8,485 tokens for the whole arrangement it sits in. That is 32.8x less text for the same clause.

## M4 Cross-reference density

| Corpus | References | Per 1k tokens | Units with 1+ outbound | Units with 0 inbound | Max in-degree |
|---|---|---|---|---|---|
| OLD | 796 | 3.0 | 45.0% | n/a | n/a |
| NEW | 215 | 2.4 | 32.0% | n/a | n/a |
| GRAPH | 244 | 2.9 | 37.0% | 76.7% | 11 |

GRAPH counts `wl:references`, `wl:constrains` and `wl:conflictsWith` together, 244 typed edges. `wl:refines` is structure and is counted apart: 124 edges, 1.5 per 1k tokens. Inbound counts every edge type.

Five most referenced clauses by in-degree:

- `03-13` Research work (11 inbound)
- `06-8` Images and attachments on tasks (10 inbound)
- `10-15` The project Progress page (7 inbound)
- `03-7` The claim transaction (7 inbound)
- `02-10` Task-declared secrets (6 inbound)

29 `wl:references` edges point at an old WL-SPEC document or section rather than a clause of the new set. Those are the edges that leave the graph.

## M5 Staleness

| Corpus | Stale `lode` invocations | Distinct | `/lode-` spellings |
|---|---|---|---|
| OLD | 291 | 62 | 41 |
| NEW | 22 | 14 | 3 |
| GRAPH | 21 | 13 | 3 |

GRAPH scans clause text taken from the `wl:sourceLines` ranges of the same 11 files, so this metric is NEW's metric on a different slice of the same bytes. It is reported on the clause unit for completeness.

Most frequent stale forms, OLD: `lode hook` (22), `lode secrets exec` (19), `lode worktree next` (15), `lode reconcile` (14), `lode statusline` (13), `lode serve` (12), `lode worktree resume` (11), `lode drift` (11)

Most frequent stale forms, NEW: `lode review show` (3), `lode review set` (3), `lode review add` (2), `lode review exec` (2), `lode review note` (2), `lode review` (2), `lode plugin ships` (1), `lode plugin with` (1)

Most frequent stale forms, GRAPH: `lode review show` (3), `lode review set` (3), `lode review add` (2), `lode review exec` (2), `lode review note` (2), `lode review` (2), `lode plugin ships` (1), `lode plugin with` (1)

## M6 Redundancy

| Corpus | Near-duplicate pairs across documents | Units with a near-duplicate elsewhere | Pairs inside one document |
|---|---|---|---|
| OLD | 2 | 0.5% | not measured |
| NEW | 0 | 0.0% | not measured |
| GRAPH | 0 | 0.0% | 0 |

Same test as the other two corpora: 5-word shingles, Jaccard at or above 0.5. GRAPH pairs clauses in different arrangements for the cross-document column. The clause split is finer than a leaf section, so same-arrangement pairs are reported as well.

## M7 Chain depth

| Corpus | Longest chain | Kind of chain | Sections amended by 2+ documents |
|---|---|---|---|
| OLD | 1 | amend and supersede markers | 4 |
| NEW | 1 | amend and supersede markers | 1 |
| GRAPH | 2 | refines (structure) | 0 |
| GRAPH | 1 | supersession, old section to new clause | 0 |

Largest supersession fan-in on one clause: 13 old sections. OLD's amend-chain measure has no counterpart in the graph yet, because no clause has been amended; the first amendment will start a version chain that this metric can then measure.

## How the metrics translate to a clause graph

- M1: the marker count goes to zero because an amendment becomes a new clause version. Supersession coverage takes its place and says how much of the old corpus each clause now carries.
- M2: the document count stays, and connectivity joins it. Components inside a topic say whether the topic is one reachable region or several islands; today it is islands.
- M3: retrieval stops being a document read. The unit is a clause plus its 1-hop neighbourhood, so the ratio measures the edges rather than the file boundaries.
- M4: prose references become typed edges with a direction. In-degree and zero-inbound share are new readings that plain text could not give.
- M5: unchanged, since the input text is the same. The clause unit only changes which lines are attributed to which node.
- M6: unchanged in method, finer in unit. Two clauses inside one document can now be compared, which a leaf-section pass over the same file would miss.
- M7: two chains replace one. Refines depth measures how deep the structure nests, supersession depth measures how far a clause is from the old corpus.

## What each metric fails to see

- M1 counts the folded markers the export emits (`**[amending ...]**`, `**[superseding ...]**`, `> Pending ...`). Amendments stated in prose inside the amending document are not counted.
- M1 amendment tokens count only the paragraph after each folded marker, so a multi-paragraph amendment is under-counted.
- M1 counts markers, not how much the amendment changed the meaning. A one-line clarification and a full rewrite count the same.
- M2 and M3 depend on keyword lists. A section about the claim transaction that never says `table` is invisible to data-model, and a glossary that mentions everything looks like it is about everything. The two-keyword threshold cuts noise but also drops short sections.
- M3 assumes an agent loads whole documents. With `--section` selectors or search the real cost is between the two columns.
- M4 counts references without knowing which ones are needed. Low density can mean self-contained or under-linked.
- M5 matches the first two words after `lode`. Prose like `lode itself` is filtered by a stop list, but a valid command with a renamed flag passes, and a command used only in an example of old behaviour is counted as stale.
- M6 catches copied text only. Two sections that state the same rule in different words are not seen, and a deliberately repeated definition counts as redundancy.
- M7 reads chain depth from folded markers, so a chain deeper than two shows as two.
- GRAPH M3 assumes retrieval is one clause plus one hop. A real agent may stop at the clause or walk three hops, and the true cost sits somewhere between the 1-hop column and the whole-arrangement column.
- GRAPH topics are the same keyword tags as the other two corpora, inherited from `wl:topic`. They carry the same blind spots and add nothing the text did not already say.
- GRAPH `constrains` and `conflictsWith` edges are hand-added around ten sections only, so M2 connectivity and M4 density are lower bounds. A fuller pass over the same text would raise both.
- GRAPH M3 counts only clause-to-clause hops. A reference that points at a whole arrangement or an old WL-SPEC document adds no tokens to the 1-hop figure, which understates the load when an agent follows one.
- None of the metrics measure whether the text is correct against the code.

## Regenerate

```
python3 <scratch>/sprawl3.py    # writes docs/research/sprawl-metrics-report.md
```

`sprawl3.py` reads the OLD exports and `commands.txt` from the session scratchpad, the NEW documents from `docs/specs2/`, and the graph from `docs/specs2/ttl/*.ttl`. It parses the Turtle with regular expressions, because the files are generated one triple per line for edges and supersession and one block per node for clauses.
