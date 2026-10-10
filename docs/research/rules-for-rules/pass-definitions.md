# Corpus pass: definitions

WL-1038, WL-PLAN-149 Task 7, WL-SPEC-77 sec-4d and sec-19.7.

Terms come from every `missing_terms` entry in `v2result0.jsonl` .. `v2result3.jsonl` (177 distinct terms, 149 rules; the plan's "65 rules, about 30 terms" is the v1 count) plus the 29 terms in the WL-SPEC-73 sec-2 glossary table that no row lists. Each definition was drafted from the spec that arranges most of the using rules, or from the glossary table where the table lists it, checked against the current spec text.

Glossary: **WL-SPEC-87** (slug `glossary`), draft, submitted for the owner's review. 173 `definition` rules, one per anchored section. Nothing was accepted.

"Rules that use it" names the rules as they stood when the rows were taken. The split passes (`pass-splits-*.md`) moved text into new anchors pending accept, so a use may now sit in a rule split out of the one named. A ref whose prefix changed in the kinds pass is shown with its row ref.

## Counts

- Terms collected: 206
- Definitions minted: 173
- Existing definitions cited: 6
- Merged into another term: 3
- Not minted, code or schema identifier: 24
- Concept IRIs set: 2 (Rally, Stale)

## Minted

| Term | Glossary rule | Source spec | Rules that use it |
|---|---|---|---|
| AcceptedDeviation | WL-RULE-1375 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Acting lead (used as "acting-lead") | WL-RULE-1376 | WL-SPEC-75 | WL-REQ-123 |
| Activity card | WL-RULE-1377 | WL-SPEC-80 | WL-REQ-1237 |
| Admin cluster | WL-RULE-1378 | WL-SPEC-73 | WL-REQ-21 |
| Admin listener | WL-RULE-1379 | WL-SPEC-73 | WL-REQ-32 |
| Amend | WL-RULE-1380 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Analysis commit | WL-RULE-1381 | WL-SPEC-75 | WL-REQ-124 |
| Approval | WL-RULE-1382 | WL-SPEC-73 | WL-REQ-344 |
| Approval flow (used as "approval_flow") | WL-RULE-1383 | WL-SPEC-75 | WL-REQ-77 |
| Artifact | WL-RULE-1384 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Auto-merge | WL-RULE-1385 | WL-SPEC-84 | WL-REQ-1325 |
| Backbone | WL-RULE-1386 | WL-SPEC-73 | WL-REQ-236 |
| Bare note | WL-RULE-1387 | WL-SPEC-84 | WL-REQ-1319 |
| Bootstrap admin | WL-RULE-1388 | WL-SPEC-74 | WL-RULE-46 (row WL-REQ-46) |
| Branch rules observation | WL-RULE-1389 | WL-SPEC-72 | WL-REQ-6 |
| Brief | WL-RULE-1390 | WL-SPEC-76 | WL-REQ-131, WL-REQ-133, WL-REQ-134 |
| Build | WL-RULE-1391 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Carryover | WL-RULE-1392 | WL-SPEC-75 | WL-REQ-119 |
| CI trust check | WL-RULE-1393 | WL-SPEC-72 | WL-REQ-8 |
| Claim | WL-RULE-1394 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| ClickStack | WL-RULE-1395 | WL-SPEC-80 | WL-REQ-1236 |
| ClickStack collector | WL-RULE-1396 | WL-SPEC-73 | WL-REQ-24 |
| ClusterSecretStore | WL-RULE-1397 | WL-SPEC-73 | WL-REQ-23 |
| CNPG | WL-RULE-1398 | WL-SPEC-75 | WL-REQ-127 |
| Commit | WL-RULE-1399 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| commit-msg hook | WL-RULE-1400 | WL-SPEC-72 | WL-REQ-7 |
| Component | WL-RULE-1401 | WL-SPEC-73 | WL-REQ-236 |
| Concern | WL-RULE-1402 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Context header | WL-RULE-1403 | WL-SPEC-79 | WL-REQ-257 |
| Corpus index | WL-RULE-1404 | WL-SPEC-79 | WL-REQ-254 |
| Corpus number | WL-RULE-1405 | WL-SPEC-78 | WL-REQ-193, WL-REQ-117 |
| Covers | WL-RULE-1406 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Crew | WL-RULE-1407 | WL-SPEC-73 | WL-REQ-118 |
| crit | WL-RULE-1408 | WL-SPEC-84 | WL-RULE-1315 (row WL-REQ-1315), WL-REQ-1316 |
| Crit gate | WL-RULE-1409 | WL-SPEC-79 | WL-REQ-244 |
| CURIE | WL-RULE-1410 | WL-SPEC-79 | WL-REQ-248 |
| Decision deck (used as "deck") | WL-RULE-1411 | WL-SPEC-78 | WL-RULE-210 (row WL-REQ-210) |
| Decision rail | WL-RULE-1412 | WL-SPEC-82 | WL-REQ-336 |
| Deck component (used as "component") | WL-RULE-1413 | WL-SPEC-78 | WL-REQ-213 |
| Defers | WL-RULE-1414 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Deployment | WL-RULE-1415 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Design authority gate | WL-RULE-1416 | WL-SPEC-72 | WL-REQ-87 |
| DesignDoc | WL-RULE-1417 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Discharge | WL-RULE-1418 | WL-SPEC-78 | WL-REQ-187 |
| Dispatch mechanism | WL-RULE-1419 | WL-SPEC-74 | WL-REQ-47 |
| Document status | WL-RULE-1420 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Domain event | WL-RULE-1421 | WL-SPEC-76 | WL-REQ-154 |
| Dossier | WL-RULE-1422 | WL-SPEC-75 | WL-REQ-124 |
| Drift | WL-RULE-1423 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Edge | WL-RULE-1424 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Edge Agent | WL-RULE-1425 | WL-SPEC-73 | WL-REQ-33, WL-REQ-34, WL-REQ-35, WL-REQ-36, WL-REQ-279, WL-REQ-280, WL-REQ-290, WL-REQ-291, WL-REQ-1234, WL-REQ-295 |
| Editor | WL-RULE-1426 | WL-SPEC-75 | WL-REQ-123 |
| Editorial Evaluation | WL-RULE-1427 | WL-SPEC-75 | WL-REQ-125 |
| Effect | WL-RULE-1428 | WL-SPEC-79 | WL-RULE-121 (row WL-REQ-121) |
| Entity | WL-RULE-1429 | WL-SPEC-81 | WL-REQ-305 |
| Environment | WL-RULE-1430 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Escalate | WL-RULE-1431 | WL-SPEC-84 | WL-REQ-1318 |
| Event bus offset (used as "eventbus offset") | WL-RULE-1432 | WL-SPEC-76 | WL-REQ-156 |
| Event log | WL-RULE-1433 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Evidence | WL-RULE-1434 | WL-SPEC-75 | WL-RULE-121 (row WL-REQ-121) |
| Evidence category | WL-RULE-1435 | WL-SPEC-82 | WL-REQ-329 |
| Flux | WL-RULE-1436 | WL-SPEC-75 | WL-REQ-113 |
| Focus | WL-RULE-1437 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Frontier | WL-RULE-1438 | WL-SPEC-81 | WL-REQ-326 |
| Gate 1 | WL-RULE-1439 | WL-SPEC-75 | WL-REQ-125 |
| Gate 2 | WL-RULE-1440 | WL-SPEC-75 | WL-REQ-125 |
| Governing document (used as "governing doc") | WL-RULE-1441 | WL-SPEC-74 | WL-REQ-70 |
| Graph Store Protocol (used as "GSP") | WL-RULE-1442 | WL-SPEC-79 | WL-REQ-251 |
| graph-server | WL-RULE-1443 | WL-SPEC-79 | WL-REQ-236, WL-REQ-250 |
| Harness | WL-RULE-1444 | WL-SPEC-80 | WL-REQ-264, WL-REQ-274 |
| Horizon | WL-RULE-1445 | WL-SPEC-75 | WL-REQ-77 |
| hzdev | WL-RULE-1446 | WL-SPEC-73 | WL-REQ-21 |
| Impact review | WL-RULE-1447 | WL-SPEC-75 | WL-REQ-124 |
| In force | WL-RULE-1448 | WL-SPEC-78 | WL-REQ-195 |
| Inbox | WL-RULE-1449 | WL-SPEC-80 | WL-REQ-299 |
| Intent | WL-RULE-1450 | WL-SPEC-78 | WL-REQ-209 |
| Issue | WL-RULE-1451 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Keychain | WL-RULE-1452 | WL-SPEC-74 | WL-REQ-61 |
| Keycloak claim | WL-RULE-1453 | WL-SPEC-74 | WL-RULE-45 (row WL-REQ-45) |
| Keystore | WL-RULE-1454 | WL-SPEC-74 | WL-REQ-71 |
| Knowledge graph | WL-RULE-1455 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Last-deploy branch | WL-RULE-1456 | WL-SPEC-75 | WL-REQ-113 |
| Lease | WL-RULE-1457 | WL-SPEC-76 | WL-REQ-136 |
| Lease TTL | WL-RULE-1458 | WL-SPEC-74 | WL-REQ-48 |
| Lease-free path | WL-RULE-1459 | WL-SPEC-75 | WL-REQ-83 |
| Litmus test (used as "litmus-test") | WL-RULE-1460 | WL-SPEC-75 | WL-REQ-125 |
| lode-worker | WL-RULE-1461 | WL-SPEC-81 | WL-REQ-1365 |
| Loopback | WL-RULE-1462 | WL-SPEC-74 | WL-REQ-56 |
| Manual close | WL-RULE-1463 | WL-SPEC-76 | WL-REQ-135 |
| Matryoshka | WL-RULE-1464 | WL-SPEC-79 | WL-REQ-255 |
| Max pooling (used as "max-pooled") | WL-RULE-1465 | WL-SPEC-79 | WL-REQ-259 |
| MCP relay | WL-RULE-1466 | WL-SPEC-73 | WL-REQ-30 |
| Mechanical file | WL-RULE-1467 | WL-SPEC-84 | WL-RULE-1320 (row WL-REQ-1320) |
| Meeting-administrator permission | WL-RULE-1468 | WL-SPEC-78 | WL-REQ-220 |
| Merge probe | WL-RULE-1469 | WL-SPEC-76 | WL-REQ-135 |
| Milestone | WL-RULE-1470 | WL-SPEC-73 | WL-REQ-209, WL-REQ-118 |
| model_prices | WL-RULE-1471 | WL-SPEC-73 | WL-REQ-39 |
| Morning Brief | WL-RULE-1472 | WL-SPEC-82 | WL-REQ-126 |
| Motherlode | WL-RULE-1473 | WL-SPEC-81 | WL-RULE-322 (row WL-REQ-322) |
| MSW | WL-RULE-1474 | WL-SPEC-83 | WL-REQ-1309 |
| Named graph | WL-RULE-1475 | WL-SPEC-79 | WL-REQ-251 |
| Native skill registry | WL-RULE-1476 | WL-SPEC-81 | WL-RULE-317 (row WL-REQ-317) |
| Natural key | WL-RULE-1477 | WL-SPEC-79 | WL-REQ-247 |
| needs-decomposition | WL-RULE-1478 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| OIDC | WL-RULE-1479 | WL-SPEC-74 | WL-REQ-51 |
| op:// reference | WL-RULE-1480 | WL-SPEC-74 | WL-REQ-67 |
| otel-gateway | WL-RULE-1481 | WL-SPEC-73 | WL-REQ-24, WL-REQ-1236 |
| Overlay | WL-RULE-1482 | WL-SPEC-73 | WL-REQ-21, WL-REQ-22, WL-REQ-23 |
| OWL RL | WL-RULE-1483 | WL-SPEC-79 | WL-REQ-241 |
| Oxigraph | WL-RULE-1484 | WL-SPEC-79 | WL-REQ-252 |
| Permission | WL-RULE-1485 | WL-SPEC-74 | WL-REQ-49 |
| Pi | WL-RULE-1486 | WL-SPEC-80 | WL-REQ-293 |
| Pickup | WL-RULE-1487 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Pin | WL-RULE-1488 | WL-SPEC-74 | WL-REQ-70 |
| Pitch | WL-RULE-1489 | WL-SPEC-75 | WL-REQ-125 |
| PKCE | WL-RULE-1490 | WL-SPEC-74 | WL-REQ-52 |
| Plan | WL-RULE-1491 | WL-SPEC-73 | WL-REQ-9 |
| Policy hand-off (used as "hand-off") | WL-RULE-1492 | WL-SPEC-86 | WL-REQ-1345 |
| Preset | WL-RULE-1493 | WL-SPEC-86 | WL-REQ-1343 |
| Primary repo | WL-RULE-1494 | WL-SPEC-75 | WL-REQ-114 |
| Prober | WL-RULE-1495 | WL-SPEC-75 | WL-RULE-121 (row WL-REQ-121) |
| Project | WL-RULE-1496 | WL-SPEC-73 | WL-RULE-130 (row WL-REQ-130) |
| Project lead (used as "lead") | WL-RULE-1497 | WL-SPEC-75 | WL-REQ-119 |
| Project owner | WL-RULE-1498 | WL-SPEC-84 | WL-REQ-1325 |
| Protocol mapper | WL-RULE-1499 | WL-SPEC-74 | WL-REQ-52 |
| PROV-O (used as "PROV") | WL-RULE-1500 | WL-SPEC-79 | WL-REQ-238 |
| Pull request | WL-RULE-1501 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Rally | WL-RULE-1502 | WL-SPEC-75 | WL-REQ-87 |
| Rally band | WL-RULE-1503 | WL-SPEC-85 | WL-REQ-1336 |
| Recovery ladder | WL-RULE-1504 | WL-SPEC-84 | WL-REQ-1316, WL-REQ-1317 |
| Reifier | WL-RULE-1505 | WL-SPEC-79 | WL-REQ-239 |
| Repo discovery (used as "discovery") | WL-RULE-1506 | WL-SPEC-75 | WL-REQ-85 |
| Repo mapping | WL-RULE-1507 | WL-SPEC-76 | WL-REQ-132, WL-REQ-85 |
| Resolver | WL-RULE-1508 | WL-SPEC-75 | WL-REQ-84, WL-REQ-85, WL-RULE-112 (row WL-REQ-112) |
| Review lane | WL-RULE-1509 | WL-SPEC-82 | WL-REQ-344 |
| Reviewed through now | WL-RULE-1510 | WL-SPEC-82 | WL-REQ-126 |
| Round | WL-RULE-1511 | WL-SPEC-84 | WL-RULE-1315 (row WL-REQ-1315), WL-REQ-1318 |
| Rule | WL-RULE-1512 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Run board | WL-RULE-1513 | WL-SPEC-85 | WL-REQ-1334 |
| Runtime event | WL-RULE-1514 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Sandbox | WL-RULE-1515 | WL-SPEC-73 | WL-RULE-25 (row WL-REQ-25) |
| Science Lead | WL-RULE-1516 | WL-SPEC-75 | WL-REQ-123 |
| Secrets ceremony (used as "ceremony") | WL-RULE-1517 | WL-SPEC-74 | WL-REQ-67 |
| Section | WL-RULE-1518 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| SHACL | WL-RULE-1519 | WL-SPEC-78 | WL-REQ-208 |
| Skill | WL-RULE-1520 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| skillOverrides | WL-RULE-1521 | WL-SPEC-81 | WL-REQ-325 |
| Slide | WL-RULE-1522 | WL-SPEC-78 | WL-REQ-212 |
| Smart zone | WL-RULE-1523 | WL-SPEC-75 | WL-REQ-102 |
| SPA | WL-RULE-1524 | WL-SPEC-83 | WL-RULE-1302 |
| Spec | WL-RULE-1525 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Spec reconciler (used as "spec-reconciler") | WL-RULE-1526 | WL-SPEC-72 | WL-REQ-7 |
| spec-gate | WL-RULE-1527 | WL-SPEC-72 | WL-REQ-6 |
| Stale | WL-RULE-1528 | WL-SPEC-77 | WL-REQ-9 |
| Subject | WL-RULE-1529 | WL-SPEC-74 | WL-REQ-49 |
| Sunstone stage | WL-RULE-1530 | WL-SPEC-75 | WL-REQ-119 |
| Sunstone Way | WL-RULE-1531 | WL-SPEC-75 | WL-REQ-118, WL-REQ-120 |
| Supersede | WL-RULE-1532 | WL-SPEC-73 | none (WL-SPEC-73 glossary table) |
| Sweeper | WL-RULE-1533 | WL-SPEC-76 | WL-REQ-136, WL-REQ-94, WL-REQ-265 |
| Task | WL-RULE-1534 | WL-SPEC-73 | WL-RULE-130 (row WL-REQ-130) |
| Task activity | WL-RULE-1535 | WL-SPEC-80 | WL-REQ-1234 |
| Task-mutation permission | WL-RULE-1536 | WL-SPEC-75 | WL-REQ-128 |
| Task-scoped token | WL-RULE-1537 | WL-SPEC-73 | WL-REQ-30 |
| Templ cockpit | WL-RULE-1538 | WL-SPEC-83 | WL-RULE-1302 |
| Templated entry | WL-RULE-1539 | WL-SPEC-74 | WL-REQ-69 |
| Umbrella project | WL-RULE-1540 | WL-SPEC-75 | WL-REQ-119 |
| Unmapped sender | WL-RULE-1541 | WL-SPEC-80 | WL-REQ-296 |
| Verdict | WL-RULE-1542 | WL-SPEC-84 | WL-RULE-1315 (row WL-REQ-1315) |
| Watcher | WL-RULE-1543 | WL-SPEC-76 | WL-REQ-159 |
| Watcher rule | WL-RULE-1544 | WL-SPEC-76 | WL-REQ-151 |
| Wire shape | WL-RULE-1545 | WL-SPEC-81 | WL-REQ-309 |
| WorklodeBot | WL-RULE-1546 | WL-SPEC-74 | WL-REQ-63 |
| Worktree | WL-RULE-1547 | WL-SPEC-76 | WL-REQ-136 |

## Cited, not copied

| Term | Defined by | Rules that use it |
|---|---|---|
| Actor | WL-RULE-45 | none (WL-SPEC-73 glossary table) |
| deliverable | WL-RULE-121 | WL-RULE-130 (row WL-REQ-130), WL-REQ-118 |
| focused | WL-RULE-1320 | WL-RULE-1320 (row WL-REQ-1320) |
| service actor | WL-RULE-45 | WL-RULE-153 (row WL-REQ-153) |
| skim | WL-RULE-1320 | WL-RULE-1320 (row WL-REQ-1320), WL-RULE-1321 |
| task state machine | WL-RULE-141 | WL-RULE-141 (row WL-REQ-141) |

## Merged into another term

| Term | Defined as | Rules that use it |
|---|---|---|
| curie | CURIE | WL-REQ-108 |
| design rule | Rule | WL-REQ-165 |
| gate | design authority gate | WL-REQ-2 |

## Not minted: code or schema identifiers

The rule that introduces each name specifies it. The owner decides whether any of these needs a definition.

| Term | Source spec | Rules that use it |
|---|---|---|
| `applied_at` | WL-SPEC-80 | WL-REQ-297 |
| `artifact_evidence` | WL-SPEC-76 | WL-REQ-137 |
| `BackgroundCtx` | WL-SPEC-76 | WL-REQ-158 |
| `conferenceRecords` | WL-SPEC-78 | WL-RULE-218 (row WL-REQ-218) |
| `content_hash` | WL-SPEC-79 | WL-REQ-260 |
| `covered_sections` | WL-SPEC-78 | WL-REQ-185 |
| `doc_reviewers` | WL-SPEC-84 | WL-REQ-1322 |
| `done_state` | WL-SPEC-76 | WL-REQ-152 |
| `finishLogin` | WL-SPEC-74 | WL-REQ-54 |
| `gate source` | WL-SPEC-75 | WL-REQ-110 |
| `governedBy` | WL-SPEC-72 | WL-REQ-7 |
| `main_commits` | WL-SPEC-75 | WL-RULE-86 (row WL-REQ-86) |
| `permSearchRead` | WL-SPEC-79 | WL-REQ-261 |
| `permWebRead` | WL-SPEC-82 | WL-REQ-334 |
| `ResolveDelivery` | WL-SPEC-76 | WL-REQ-150 |
| `response_type` | WL-SPEC-78 | WL-REQ-214 |
| `router.asset` | WL-SPEC-83 | WL-REQ-1307 |
| `seeded_by` | WL-SPEC-75 | WL-REQ-122 |
| `state_log` | WL-SPEC-76 | WL-REQ-146 |
| `SubscriberLock` | WL-SPEC-75 | WL-REQ-107 |
| `task_commits` | WL-SPEC-75 | WL-REQ-84 |
| `triage_state` | WL-SPEC-80 | WL-REQ-299 |
| `wlc:` | WL-SPEC-79 | WL-REQ-237 |
| `wlid` | WL-SPEC-75 | WL-REQ-105 |

## Drafting notes for review

Definitions whose source is thin, or where the corpus uses the term in two senses.

- AcceptedDeviation (WL-RULE-1375): Table says 'Effect / AcceptedDeviation'; Effect (a deliverable with no artifact) is a different term.
- Amend (WL-RULE-1380): Table combines Amend / supersede and says documents are involved; the relation is between rules only.
- Approval (WL-RULE-1382): Taken from the WL-SPEC-73 glossary table; table text is 'A recorded decision that a document or deliverable is accepted, with who and when.'
- CI trust check (WL-RULE-1393): WL-SPEC-72 uses the term without explaining it; definition is minimal. Check the CI workflow for the exact rule.
- Claim (WL-RULE-1394): Table row is still accurate; added the state change and the claimability conditions' summary from WL-SPEC-75 sec-7.
- Concern (WL-RULE-1402): Table says 'narrows pickup to a category of problem'; concern is a closed enum on the task, used by focus.
- Crew (WL-RULE-1407): WL-SPEC-73 table says 'people currently working a project'; WL-SPEC-75 sec-13.5 defines participants with roles and a lead. Drafted from WL-SPEC-75.
- Crit gate (WL-RULE-1409): Spec uses the phrase without defining it beyond 'crit-reviewed'; check wording with owner.
- Decision deck (WL-RULE-1411): Not built yet (WL-971).
- Deck component (WL-RULE-1413): Same spelling as Component (graph, WL-SPEC-79); qualified.
- DesignDoc (WL-RULE-1417): WL-SPEC-79 still lists wl:ADR as a subclass; the adr kind is being retired.
- Document status (WL-RULE-1420): Table lists 'stale' for all documents; current spec limits it to plans.
- Edge (WL-RULE-1424): Table lists only the four task edges; document and rule edges now exist (WL-SPEC-77 sec-8).
- Effect (WL-RULE-1428): WL-SPEC-73 glossary table row 'Effect / AcceptedDeviation' describes a tolerated deviation and is out of date; WL-SPEC-79 sec-8 defines Effect as a Deliverable subclass.
- Entity (WL-RULE-1429): WL-RULE-218 'Entities' covers only meetings, minutes and notes, so it is not cited.
- Evidence (WL-RULE-1434): Also used for an analysis submission's revision-bound 'evidence bundle' (sec-13.6).
- Focus (WL-RULE-1437): Table says focus narrows pickup to an area (project, component); current meaning is a project-level ordered concern list that only ranks.
- Frontier (WL-RULE-1438): Two meanings: grilling design-tree frontier (WL-REQ-326) and task frontier / deployed frontier elsewhere. Heading is qualified-free here; coordinator may want 'Grilling frontier'.
- Governing document (WL-RULE-1441): Spec text defines governing rules (WL-SPEC-75) but not the phrase 'governing doc'.
- Graph Store Protocol (WL-RULE-1442): Term is GSP; heading spelled out.
- Horizon (WL-RULE-1445): Separate from the commit horizon of the event log (WL-SPEC-75 sec-9.3); WL-REQ-77 uses the project meaning.
- hzdev (WL-RULE-1446): Hetzner expansion of the hz prefix inferred from WL-SPEC-80's "each Hetzner cluster".
- Issue (WL-RULE-1451): Table says Issues are projected and mirrored to a task; WL-SPEC-79 says declared, not projected.
- Keychain (WL-RULE-1452): The keystore for task secrets (separate term) also uses the macOS keychain.
- Litmus test (WL-RULE-1460): WL-SPEC-75 only names it; its criteria live in the sunstone-core:litmus-test skill. Definition is thin; coordinator may drop or revise.
- Max pooling (WL-RULE-1465): Term is the adjective max-pooled; heading uses the noun.
- Milestone (WL-RULE-1470): WL-REQ-209 calls a milestone 'a date'; WL-SPEC-75 and the glossary table describe a grouping with ordering.
- Pickup (WL-RULE-1487): Table row was 'Ranking and pickup'; term here is Pickup as listed.
- Pin (WL-RULE-1488): WL-SPEC-81 also uses 'pin' for skills and 'pinned to' for test constraints; this covers the task/doc declaration sense.
- Plan (WL-RULE-1491): Taken from the WL-SPEC-73 glossary table. WL-REQ-9 also frames a plan as the reconciliation between a spec and the code when it was written.
- Policy hand-off (WL-RULE-1492): WL-SPEC-86 sec-3 says three hand-offs but names two.
- Project owner (WL-RULE-1498): Spec 84 does not define the term; check WL-SPEC-74 for the owner role before accepting.
- PROV-O (WL-RULE-1500): Term is the short form PROV; heading uses PROV-O as in the spec.
- Pull request (WL-RULE-1501): Same note as Issue.
- Repo discovery (WL-RULE-1506): Collides with Discovery, the first Sunstone Way stage; WL-REQ-85 uses the repo meaning.
- Rule (WL-RULE-1512): Table says 'A requirement with a stable WL-REQ-n id'; out of date after rule kinds (WL-SPEC-77 sec-4). informative is retired.
- Runtime event (WL-RULE-1514): Class declared but not projected (no natural key).
- Sandbox (WL-RULE-1515): Also the id of the default agent actor (WL-SPEC-74), a different thing named after this one.
- Supersede (WL-RULE-1532): Split from the table's Amend / supersede row.
- Task-mutation permission (WL-RULE-1536): Go constant is permTaskWrite.
- Umbrella project (WL-RULE-1540): WL-SPEC-73 uses 'umbrella' loosely in its Project row.
- Watcher (WL-RULE-1543): `lode-watch` is a separate executable (pod informer) with a different role; the rule WL-REQ-159 uses the subscriber meaning.
- Rally (WL-RULE-1502): the body says members are `dependsOn` edges; the `wlc:rally` concept definition says `blocks` edges. One of the two needs updating.

## Pending

1. Owner review and accept of WL-SPEC-87.
2. The WL-SPEC-73 sec-2 glossary table duplicates the glossary and is out of date in several rows (Rule, Focus, Concern, Edge, Amend / supersede, Document status, Effect / AcceptedDeviation, Crew). Remove it in a WL-SPEC-73 revision after the owner accepts the open candidate revision there. Not edited here.
3. `needs` edges from each using rule to its definition: Task 8 (WL-1039).
