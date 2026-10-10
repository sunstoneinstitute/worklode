# Corpus pass: refines and needs edges

WL-1039, WL-PLAN-149 Task 8. Writes the `refines` and `needs` edges of WL-SPEC-77 sec-4c over the live rules after the splits of Tasks 4 to 6, then measures every live rule's context closure against the sec-4c targets.

## Method

1. Row edges come from the `refines` and `needs` lists in `v2result0.jsonl` to `v2result3.jsonl` (479 pairs). Each ref is resolved by number, since a kind change moved some refs between `WL-REQ-` and `WL-RULE-`. A withdrawn rule maps to its one live successor over `supersedes`. A rule that no longer exists is skipped.
2. Where neither side was split, the pair is written as is. Where either side was split (276 pairs), the source pieces are the rule plus its `wasDerivedFrom` children, and likewise for the target. A reviewer read the piece headings and bodies and chose the pair that holds the dependency, or called it unclear. Four were unclear and are skipped.
3. Glossary edges: every live rule whose body names a WL-SPEC-87 definition heading (whole word, case-insensitive, the same match `lode rule lint` uses) `needs` that definition. A definition never needs itself.
4. A pair the derivation already holds as `references` is skipped. Every edge was written with `lode rule link`.

## Edges written

| Type | Source | Written |
|---|---|---|
| `refines` | rows | 104 |
| `needs` | rows | 337 |
| `needs` | WL-SPEC-87 glossary | 2935 |
| Total | | 3376 |

## Skipped pairs (47)

| Reason | Count |
|---|---|
| held as references | 33 |
| target rule no longer exists | 6 |
| source and target are one rule | 4 |
| split: piece unclear | 4 |

"Target rule no longer exists": WL-REQ-245 and WL-REQ-330 are heading shells that the WL-SPEC-77 sec-19.7 migration turned into spec headings. "Source and target are one rule": the `too_small` merges folded the source into its target.

| From | Type | To | Reason |
|---|---|---|---|
| WL-REQ-42 | `needs` | WL-REQ-41 | source and target are one rule |
| WL-REQ-166 | `needs` | WL-REQ-245 | target rule no longer exists |
| WL-REQ-331 | `refines` | WL-REQ-330 | target rule no longer exists |
| WL-REQ-332 | `refines` | WL-REQ-330 | target rule no longer exists |
| WL-REQ-333 | `refines` | WL-REQ-330 | target rule no longer exists |
| WL-REQ-1246 | `refines` | WL-REQ-330 | target rule no longer exists |
| WL-REQ-1362 | `refines` | WL-REQ-330 | target rule no longer exists |
| WL-REQ-91 | `refines` | WL-REQ-90 | source and target are one rule |
| WL-REQ-93 | `needs` | WL-REQ-88 | source and target are one rule |
| WL-REQ-292 | `needs` | WL-REQ-291 | source and target are one rule |
| WL-REQ-1298 | `needs` | WL-REQ-165 | split: piece unclear: 1298 (rule edits via own versions) has no clear 165 anchor/section piece it depends on |
| WL-REQ-1298 | `needs` | WL-REQ-171 | split: piece unclear: 1298 vs 171 escalation ladder: no piece of the ladder obviously states a fact 1298 needs |
| WL-REQ-123 | `needs` | WL-REQ-50 | split: piece unclear: Crew roles vs Keycloak login link not evident from texts |
| WL-REQ-127 | `needs` | WL-REQ-21 | split: piece unclear: No visible dependency of pool/migrations on instance placement |
| WL-REQ-6 | `needs` | WL-REQ-7 | held as references |
| WL-REQ-1616 | `needs` | WL-REQ-143 | held as references |
| WL-REQ-1620 | `needs` | WL-REQ-144 | held as references |
| WL-REQ-1744 | `needs` | WL-REQ-319 | held as references |
| WL-REQ-326 | `needs` | WL-REQ-327 | held as references |
| WL-REQ-1555 | `needs` | WL-REQ-63 | held as references |
| WL-REQ-1565 | `needs` | WL-REQ-1566 | held as references |
| WL-REQ-1566 | `needs` | WL-REQ-49 | held as references |
| WL-REQ-163 | `needs` | WL-REQ-1665 | held as references |
| WL-REQ-1627 | `needs` | WL-REQ-1673 | held as references |
| WL-REQ-174 | `needs` | WL-REQ-1356 | held as references |
| WL-REQ-1677 | `needs` | WL-REQ-49 | held as references |
| WL-REQ-1770 | `needs` | WL-REQ-1338 | held as references |
| WL-REQ-1337 | `needs` | WL-REQ-1338 | held as references |
| WL-REQ-1785 | `needs` | WL-REQ-1340 | held as references |
| WL-REQ-1570 | `needs` | WL-REQ-1571 | held as references |
| WL-REQ-54 | `needs` | WL-REQ-53 | held as references |
| WL-REQ-54 | `needs` | WL-REQ-56 | held as references |
| WL-REQ-64 | `needs` | WL-REQ-53 | held as references |
| WL-REQ-1590 | `needs` | WL-REQ-1356 | held as references |
| WL-REQ-107 | `needs` | WL-REQ-106 | held as references |
| WL-REQ-109 | `needs` | WL-REQ-124 | held as references |
| WL-REQ-114 | `needs` | WL-REQ-63 | held as references |
| WL-REQ-1596 | `needs` | WL-REQ-85 | held as references |
| WL-REQ-116 | `needs` | WL-REQ-123 | held as references |
| WL-REQ-116 | `needs` | WL-REQ-124 | held as references |
| WL-REQ-126 | `needs` | WL-REQ-107 | held as references |
| WL-REQ-1234 | `needs` | WL-REQ-1726 | held as references |
| WL-REQ-1730 | `needs` | WL-REQ-288 | held as references |
| WL-REQ-1731 | `needs` | WL-RULE-86 | held as references |
| WL-REQ-1732 | `needs` | WL-REQ-1339 | held as references |
| WL-REQ-1323 | `needs` | WL-RULE-1320 | held as references |
| WL-REQ-1779 | `needs` | WL-REQ-1324 | held as references |

## Closures

From `lode show <rule> --closure --json` over all 828 live rules.

| Measure | Before | After | Target |
|---|---|---|---|
| Closure size, median (rules) | 1 | 18 | none |
| Closure size, p90 (rules) | 1 | 39 | none |
| Closure words, median | 75 | 632 | none |
| Closure words, p90 | 203 | 1890 | under 2000 |

72 rules have a closure over 2000 words.

Ten largest closures:

| Rule | Heading | Spec | Rules | Words |
|---|---|---|---|---|
| WL-REQ-293 | Pi agent integration | WL-SPEC-80 | 73 | 5689 |
| WL-REQ-291 | Hook wiring | WL-SPEC-80 | 66 | 5240 |
| WL-REQ-283 | `lode task brief <id> --json` | WL-SPEC-80 | 67 | 4400 |
| WL-REQ-271 | Moving a worktree | WL-SPEC-80 | 65 | 4333 |
| WL-REQ-285 | Skills | WL-SPEC-80 | 64 | 4283 |
| WL-REQ-265 | Worktree-bound lease lifecycle | WL-SPEC-80 | 64 | 4269 |
| WL-REQ-284 | Slash commands | WL-SPEC-80 | 63 | 4223 |
| WL-RULE-121 | Deliverables | WL-SPEC-75 | 64 | 4047 |
| WL-REQ-101 | `claim --next` and `--strict-focus` | WL-SPEC-75 | 61 | 4038 |
| WL-REQ-290 | Agent-session store functions | WL-SPEC-80 | 57 | 3905 |

The largest closures sit in WL-SPEC-80 (worktrees, hooks, the brief) and WL-SPEC-75 (tasks). They pull in most of the task, lease and session model plus its glossary terms.

## Rules with exactly one context edge (125)

Test 4 report, not a verdict: each is a candidate to check for restatement of its one target. From `lode rule lint`.

WL-RULE-1 (needs WL-RULE-1525), WL-RULE-10 (needs WL-RULE-1525), WL-REQ-17 (needs WL-REQ-15), WL-REQ-32 (needs WL-RULE-1379), WL-REQ-50 (needs WL-RULE-1518), WL-REQ-56 (needs WL-RULE-1462), WL-REQ-59 (refines WL-REQ-56), WL-RULE-62 (needs WL-RULE-1546), WL-REQ-65 (refines WL-RULE-62), WL-REQ-144 (needs WL-REQ-143), WL-RULE-161 (needs WL-RULE-1525), WL-RULE-181 (needs WL-RULE-1518), WL-REQ-192 (needs WL-RULE-1518), WL-RULE-206 (needs WL-RULE-1491), WL-REQ-219 (refines WL-RULE-218), WL-REQ-227 (refines WL-REQ-225), WL-REQ-229 (refines WL-REQ-225), WL-REQ-230 (refines WL-REQ-225), WL-REQ-231 (refines WL-REQ-225), WL-REQ-249 (needs WL-REQ-247), WL-REQ-255 (needs WL-RULE-1464), WL-RULE-1333 (needs WL-RULE-1511), WL-REQ-1343 (needs WL-RULE-1512), WL-RULE-1344 (needs WL-RULE-1525), WL-REQ-1346 (needs WL-RULE-1430), WL-RULE-1369 (needs WL-RULE-1525), WL-RULE-1371 (needs WL-RULE-1491), WL-REQ-1373 (needs WL-RULE-1457), WL-REQ-1374 (needs WL-RULE-1534), WL-RULE-1376 (needs WL-RULE-1496), WL-RULE-1386 (needs WL-RULE-1433), WL-RULE-1390 (needs WL-RULE-1534), WL-RULE-1397 (needs WL-RULE-1482), WL-RULE-1401 (needs WL-RULE-1455), WL-RULE-1402 (needs WL-RULE-1534), WL-RULE-1406 (needs WL-RULE-1491), WL-RULE-1408 (needs WL-RULE-1511), WL-RULE-1409 (needs WL-RULE-1408), WL-RULE-1411 (needs WL-RULE-1534), WL-RULE-1412 (needs WL-RULE-1496), WL-RULE-1413 (needs WL-RULE-1522), WL-RULE-1416 (needs WL-RULE-1525), WL-RULE-1421 (needs WL-RULE-1529), WL-RULE-1425 (needs WL-RULE-1444), WL-RULE-1430 (needs WL-RULE-1415), WL-RULE-1432 (needs WL-RULE-1433), WL-RULE-1434 (needs WL-RULE-1495), WL-RULE-1438 (needs WL-RULE-1534), WL-RULE-1442 (needs WL-RULE-1443), WL-RULE-1444 (needs WL-RULE-1486), WL-RULE-1447 (needs WL-RULE-1382), WL-RULE-1451 (needs WL-RULE-1455), WL-RULE-1453 (needs WL-RULE-1394), WL-RULE-1460 (needs WL-RULE-1422), WL-RULE-1465 (needs WL-RULE-1529), WL-RULE-1470 (needs WL-RULE-1496), WL-RULE-1473 (needs WL-RULE-1531), WL-RULE-1474 (needs WL-RULE-1524), WL-RULE-1475 (needs WL-RULE-1496), WL-RULE-1478 (needs WL-RULE-1534), WL-RULE-1483 (needs WL-RULE-1512), WL-RULE-1484 (needs WL-RULE-1443), WL-RULE-1489 (needs WL-RULE-1496), WL-RULE-1491 (needs WL-RULE-1534), WL-RULE-1494 (needs WL-RULE-1534), WL-RULE-1495 (needs WL-RULE-1415), WL-RULE-1497 (needs WL-RULE-1496), WL-RULE-1499 (needs WL-RULE-1394), WL-RULE-1500 (needs WL-RULE-1429), WL-RULE-1501 (needs WL-RULE-1455), WL-RULE-1503 (needs WL-RULE-1502), WL-RULE-1505 (needs WL-RULE-1424), WL-RULE-1507 (needs WL-RULE-1496), WL-RULE-1508 (needs WL-RULE-1534), WL-RULE-1512 (needs WL-RULE-1525), WL-RULE-1513 (needs WL-RULE-1496), WL-RULE-1520 (needs WL-RULE-1534), WL-RULE-1521 (needs WL-RULE-1473), WL-RULE-1524 (needs WL-RULE-1538), WL-RULE-1525 (needs WL-RULE-1450), WL-RULE-1529 (needs WL-RULE-1534), WL-RULE-1531 (needs WL-RULE-1386), WL-RULE-1533 (needs WL-RULE-1457), WL-RULE-1538 (needs WL-RULE-1524), WL-RULE-1539 (needs WL-RULE-1454), WL-RULE-1540 (needs WL-RULE-1496), WL-RULE-1541 (needs WL-RULE-1496), WL-RULE-1542 (needs WL-RULE-1511), WL-RULE-1545 (needs WL-RULE-1429), WL-RULE-1547 (needs WL-RULE-1534), WL-REQ-1550 (needs WL-RULE-1501), WL-REQ-1554 (needs WL-RULE-1434), WL-REQ-1555 (needs WL-RULE-1378), WL-REQ-1569 (needs WL-RULE-1534), WL-REQ-1574 (needs WL-RULE-1525), WL-REQ-1578 (needs WL-RULE-1525), WL-REQ-1579 (needs WL-RULE-1539), WL-REQ-1587 (needs WL-RULE-1534), WL-REQ-1597 (needs WL-RULE-1436), WL-REQ-1603 (needs WL-RULE-1496), WL-REQ-1614 (needs WL-RULE-1390), WL-REQ-1618 (needs WL-RULE-1525), WL-REQ-1621 (needs WL-RULE-1543), WL-REQ-1627 (needs WL-RULE-1525), WL-REQ-1629 (needs WL-RULE-1534), WL-REQ-1639 (needs WL-RULE-1512), WL-REQ-1641 (needs WL-RULE-1512), WL-REQ-1648 (needs WL-RULE-1475), WL-REQ-1659 (needs WL-RULE-1525), WL-REQ-1671 (needs WL-RULE-1534), WL-REQ-1689 (needs WL-RULE-1391), WL-REQ-1690 (needs WL-REQ-1699), WL-REQ-1694 (needs WL-RULE-1534), WL-REQ-1695 (needs WL-RULE-1433), WL-REQ-1697 (needs WL-RULE-1534), WL-REQ-1706 (needs WL-RULE-1496), WL-REQ-1718 (needs WL-REQ-1713), WL-REQ-1722 (needs WL-RULE-1386), WL-REQ-1723 (needs WL-RULE-1444), WL-REQ-1728 (needs WL-REQ-275), WL-REQ-1758 (needs WL-RULE-1534), WL-REQ-1771 (needs WL-RULE-1382), WL-REQ-1780 (needs WL-REQ-108), WL-REQ-1781 (needs WL-REQ-178), WL-REQ-1790 (needs WL-REQ-32)

## Open `conflictsWith` pairs

No `conflictsWith` edge is recorded in the backbone. The 15 confirmed pairs in `conflicts.jsonl` are open as decision tasks, all `ready`:

| Task | Pair | Fact |
|---|---|---|
| WL-1013 | WL-REQ-1307 / WL-REQ-1338 | X-Requested-With header value |
| WL-1013 | WL-REQ-334 / WL-REQ-1307 | X-Requested-With header value |
| WL-1014 | WL-REQ-149 / WL-REQ-152 | workflow the repo done_state must belong to |
| WL-1015 | WL-REQ-164 / WL-REQ-1299 | whether docs.body stores spec text |
| WL-1016 | WL-REQ-1355 / WL-REQ-1298 | rule edit on a rule arranged in no or several docs |
| WL-1017 | WL-REQ-1303 / WL-REQ-1304 | behaviour with no LODE_LEGACY_URL |
| WL-1018 | WL-REQ-105 / WL-REQ-110 | subscriber action external_id format |
| WL-1019 | WL-REQ-105 / WL-REQ-290 | domain event external_id format |
| WL-1020 | WL-REQ-86 / WL-REQ-114 | multi-repo task delivery/closure |
| WL-1021 | WL-REQ-87 / WL-REQ-122 | cross-project edge types |
| WL-1022 | WL-REQ-1324 / WL-REQ-1325 | when a task enters in_review |
| WL-1023 | WL-REQ-1322 / WL-REQ-1323 | whether the web client produces review rounds |
| WL-1024 | WL-REQ-169 / WL-REQ-1288 | subject of implements edge |
| WL-1025 | WL-REQ-177 / WL-REQ-1288 | subject of implements edge |
| WL-1026 | WL-REQ-341 / WL-REQ-187 | planning outcome names |

## Undefined terms

`lode rule lint` reported 3048 undefined-term findings before the pass and 103 after. Every remaining one names a definition outside WL-SPEC-87, which this pass does not link:

| Definition | Heading | Spec | Rules using it |
|---|---|---|---|
| WL-RULE-86 | Closed | WL-SPEC-75 | 36 |
| WL-RULE-121 | Deliverables | WL-SPEC-75 | 30 |
| WL-RULE-45 | Actors | WL-SPEC-74 | 13 |
| WL-RULE-141 | Workflows | WL-SPEC-76 | 11 |
| WL-RULE-218 | Entities | WL-SPEC-78 | 8 |
| WL-RULE-153 | The automation engine | WL-SPEC-76 | 2 |
| WL-RULE-196 | Consolidation | WL-SPEC-78 | 2 |
| WL-RULE-1320 | Guides | WL-SPEC-84 | 1 |

## Specs over forty rules

| Spec | Rules |
|---|---|
| WL-SPEC-25 | 74 |
| WL-SPEC-73 | 49 |
| WL-SPEC-74 | 49 |
| WL-SPEC-75 | 81 |
| WL-SPEC-76 | 45 |
| WL-SPEC-77 | 93 |
| WL-SPEC-78 | 70 |
| WL-SPEC-79 | 46 |
| WL-SPEC-80 | 63 |
| WL-SPEC-81 | 42 |
| WL-SPEC-87 | 173 |

WL-SPEC-87 is the glossary: one definition per term, so its count is not a subject count.

## Targets

| Target (sec-4c) | Result | Met |
|---|---|---|
| p90 closure under 2000 words | 1890 | yes, with little margin |
| Zero `conflictsWith` pairs | 15 confirmed pairs open (WL-1013 to WL-1026) | no |
| Zero undefined terms | 103, all on 8 definitions outside WL-SPEC-87 | no |
| About forty rules per spec | 10 specs over forty, apart from the glossary | no |
| No rule over one decision group | not measured here; Tasks 4 to 6 split the `too_big` rows | not measured |
