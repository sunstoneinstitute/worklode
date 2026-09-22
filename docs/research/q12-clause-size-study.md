# Q12 clause size study

Measured on the 47 old specs (inline exports), the 11 restructured documents, `section-map.tsv`, 28 WL plans and the amendment edges of 13 specs. Tokens are words x 1.3 (tiktoken not installed).

## Summary

1. Amendments are paragraph-sized. The 45 effective folded amendments have a p50 of 61 tokens and a p90 of 95, against target sections of p50 744 tokens. The p50 amendment rewrites 8% of its target, and 38 of 45 rewrite under a quarter of it. No amendment is document-scoped.
2. Amendments target H2 and H3 about equally (27 vs 18 folded, 33 vs 29 by edge). Heading level does not predict where change lands. The changed part is one rule inside the section.
3. The H2 was not stable under the refactor: 68% of kept old H2 rows merged with others into one new section, only 33% moved 1:1, and 43% of old H2s landed under a new H3.
4. Plans cover H3 anchors 40% of the time (86 of 216 edges), so plan authors already reach below the H2.
5. Leaf headings (H3 where present, otherwise the H2) fit a 100 to 800 token window in 83% (old) and 80% (new) of cases. The 1500 upper bound bites on 6% of H2s and no new H3.
6. Hand reading: H2s under about 400 tokens hold one concern, H2s over 1000 hold four to eight, and an H3 holds one concern in five of six cases.

Recommended answer to Q12: (b) with (d) as the guard, tightened. A clause is the lowest heading unit of the source document (the H3 where one exists, otherwise the H2), bounded to 100 to 800 tokens. A leaf over 800 tokens is split at paragraph boundaries into one clause per rule. A leaf under 100 tokens is folded into its neighbour unless it is a standalone rule (a table, a command, a state machine). The upper bound is 800 rather than 1500 because the amendment data shows change lands on one 60 token rule, and a 1500 token clause would reversion 25 times more text than changed.

## 1. Amendments and supersessions (drives the recommendation)

Folded markers (`> **Amended by ...`, `> **Superseded by ...`, `> Pending ...`) in the 47 inline exports.

| Measure | Value |
|---|---|
| Folded markers total | 97 (45 effective, 52 pending) |
| Effective, target is H2 | 27 (60%) |
| Effective, target is H3 or deeper | 18 (40%) |
| Pending, target H2 / H3+ | 29 / 23 |
| Amending text tokens, p10 / p50 / p90 / max | 35 / 61 / 95 / 137 |
| Target section tokens, p50 / p90 | 744 / 1217 |
| Ratio amending / target, p50 / p90 | 0.08 / 0.28 |
| Amendments rewriting under 25% of target | 38 of 45 |
| Amendments at or above 100% of target | 1 |
| Sections carrying more than one amendment | 7 of 34 |

Edges via `lode doc show --json` (`amends` edges on 9 amending docs, `amendedBy` inbound on 9 amended docs): outbound 13 H2 / 7 H3+, inbound by target anchor 33 H2 / 29 H3+, document-scoped 0. `replaces` edges: none in the sample.

Answer to "what is the natural amendment unit": one rule of about 60 tokens, smaller than any heading. Where an amendment rewrote part of an H2 it changed one paragraph out of a 700 to 1200 token section. Example: WL-SPEC-4 sec-1.4 (4098 tokens) received an 89 token amendment; WL-SPEC-29 sec-2 (2169 tokens) a 28 token one.

## 2. Section sizes

| Corpus | Unit | n | p10 | p50 | p90 | max |
|---|---|---|---|---|---|---|
| Old (47) | H2 | 499 | 120 | 306 | 1105 | 6188 |
| Old | H3 | 362 | 81 | 208 | 597 | 4187 |
| New (11) | H2 | 155 | 80 | 390 | 1171 | 2921 |
| New | H3 | 227 | 83 | 182 | 382 | 791 |

| Measure | Old | New |
|---|---|---|
| H2 over 1500 tokens | 29 / 499 (6%) | 10 / 155 (6%) |
| H2 over 800 tokens | 79 / 499 (16%) | 36 / 155 (23%) |
| H3 under 100 tokens | 55 / 362 (15%) | 39 / 227 (17%) |
| H3 over 800 tokens | 19 / 362 | 0 / 227 |
| H3s per H2, p50 / p90 / max | 0 / 3 / 10 | 0 / 5 / 10 |
| H2s with no H3 | 384 / 499 (77%) | 108 / 155 (70%) |
| Leaf units (H3, or H2 without H3) | 746 | 335 |
| Leaf in 100..800 tokens | 622 (83%) | 269 (80%) |
| Leaf under 100 | 83 (11%) | 61 (18%) |
| Leaf over 800 | 41 (5%) | 5 (1%) |
| Leaf p50 / p90 | 221 / 607 | 195 / 463 |
| H2 preamble before first H3, p50 / p90 (H2s with H3s) | 119 / 703 | 50 / 202 |

## 3. Section map (old H2 to new section)

| Measure | Value |
|---|---|
| Rows | 661 (495 old H2, 166 old H3) |
| Dispositions | moved 347, merged 195, dropped-history 56, dropped-other 49, dropped-stale 9, pointer 5 |
| Rows mapped to a new section | 539 |
| One old row to more than one new section (split) | 3 (1%) |
| New sections receiving content | 278 |
| New sections receiving more than one old row (merge) | 100 (36%) |
| Old rows involved in a merge | 364 (68%) |
| Merge fan-in p50 / p90 / max | 1 / 4 / 13 |
| Old rows moved 1:1 into an otherwise empty section | 177 (33%) |
| Old H2 rows landing at new H2 / new H3 / named bucket | 219 / 160 / 13 |

Excluding the Open questions and Sources buckets changes none of these by more than one point.

## 4. Plan `covers` anchors

28 WL plans (the 15 most recent plus every tenth): 216 `covers` edges, 124 to H2 anchors (57%), 86 to H3 or deeper (40%), 6 document-scoped. 19 plans cover at least one H3, 9 cover only H2s.

## 5. Hand judgments

- WL-SPEC-54 §5 Launch profiles (306 tokens, no H3): one concern, four paragraphs that each restate part of the same rule.
- WL-SPEC-29 §7 Approvals (1105 tokens, 3 H3): six concerns (table and state machine, decision tasks, revision binding, flows and lanes, evidence bundle, gates). The preamble alone holds three. Each H3 holds one.
- WL-SPEC-25 §9 Plans are documents (3987 tokens, 3 H3): eight or more concerns (plan vs spec, task declaration grammar, metadata keys, minting transaction, title identity, drift rule, versioning, hierarchy). H3 9.1 alone holds four, so the H3 is not one concern here either.
- 06 Open questions (170 tokens, no H3): ten unrelated items, a bucket. Each bullet is its own clause candidate under 30 tokens.
- 07 §10 IRIs (754 tokens, 4 H3): four concerns matching the four H3s (base, grammar, storage, code location). The grammar table is one clause of 400 tokens.
- 03 §13 Research work (2254 tokens, 8 H3): eight concerns matching eight H3s. Approvals under 13.x duplicates WL-SPEC-29 §7, so one concern spans two documents.
