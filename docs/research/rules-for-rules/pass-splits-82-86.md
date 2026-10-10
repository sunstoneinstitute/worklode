# Corpus pass: splits in WL-SPEC-82 to 86

WL-1037, WL-PLAN-149 Task 6. Applies tests 3 and 4 of WL-SPEC-77 sec-4c to the rules arranged in WL-SPEC-82 to 86, from the rows in `v2result0.jsonl` to `v2result3.jsonl`, with the conventions of `pass-splits-72-76.md`. A `too_big` rule becomes one section per decision group: the original anchor keeps the first group and each new group gets a letter-suffixed anchor (WL-SPEC-77 sec-4). Rules with verdict `template` are not split. No spec in this range has a `too_small` row.

WL-SPEC-82, 83, 84 and 85 each have an open candidate revision with the new body. None is accepted: the owner reviews and runs `lode doc revise <spec> --accept`. A revision mints its new rules only when it lands, so lineage, kind changes and re-pointed `covers` and `governedBy` edges are listed below as commands to run after each accept. WL-SPEC-86 is a draft, which `lode doc revise` refuses, so it was edited in place and its lineage and kinds were run. Superseded plans and abandoned tasks are not re-pointed.

`rr` resolves a spec anchor to the rule arranged there:

```bash
rr() { lode rule list --doc "$1" --json | jq -r --arg d "$1" --arg a "$2" \
  '.[] | select(any(.arranged_in[]; .doc_ref == $d and .anchor == $a)) | .ref'; }
```

## Summary

| Spec | Before | After accept | Rules split | Kept | `too_small` merged | Revision | Post-accept commands |
|---|---|---|---|---|---|---|---|
| WL-SPEC-82 | 23 | 40 | 10 | 0 | 0 | opened | 29 |
| WL-SPEC-83 | 11 | 14 | 1 | 0 | 0 | opened | 7 |
| WL-SPEC-84 | 21 | 30 | 6 | 0 | 0 | opened | 30 |
| WL-SPEC-85 | 10 | 19 | 5 | 0 | 0 | opened | 32 |
| WL-SPEC-86 | 4 | 10 | 2 | 0 | 0 | draft, edited in place | 0 (10 run) |
| Total | 69 | 113 | 24 | 0 | 0 | 4 opened, 1 draft | 98 |

No revision was refused for an already-open candidate. The WL-SPEC-83 candidate of 2026-10-09 had landed as version 2 before this pass.

No spec is over about forty rules after the pass. WL-SPEC-82 sits at 40.

`lode rule list --json` returns empty `covered_by` and `governed_tasks`. The edges here were read with `lode rule show <ref> --json`.

WL-1013, the `X-Requested-With` conflict, is governed by three split rules. Each spec's commands move it to the group that holds the header: WL-SPEC-82 sec-3a, WL-SPEC-83 sec-6a, and WL-SPEC-85 sec-5, which keeps WL-REQ-1338.

## WL-SPEC-82 Cockpit

Rules: 23 before, 40 after accept (17 new sections, 0 merged). Revision: opened on doc 565, body written, not accepted. At the forty-rule target.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-329 | sec-1 | sec-1 Evidence-backed status display; sec-1a Three disclosure layers |
| WL-REQ-331 | sec-2.1 | sec-2.1 Top bar destinations and routes; sec-2.1a Top bar at narrow widths |
| WL-REQ-332 | sec-2.2 | sec-2.2 Project sidebar; sec-2.2a Task page; sec-2.2b Rule page; sec-2.2c Document page |
| WL-REQ-334 | sec-3 | sec-3 Session gating and permissions; sec-3a Write origin gates; sec-3b Server-side page assembly and import boundary; sec-3c Styling and generated artifacts |
| WL-REQ-335 | sec-4 | sec-4 Accessibility target; sec-4a Narrow-viewport and contrast rules |
| WL-REQ-341 | sec-6 | sec-6 The fact-selected panel set; sec-6a Panel assembly and the shared planning outcome |
| WL-REQ-343 | sec-8 | sec-8 The Crew page; sec-8a Agents shown as delegates |
| WL-REQ-344 | sec-9 | sec-9 Review lanes; sec-9a The Deliverables page; sec-9b The reviews queue; sec-9c The approval page; sec-9d Which work needs review |
| WL-REQ-346 | sec-11 | sec-11 Home project cards; sec-11a The Morning Brief |
| WL-REQ-347 | sec-12 | sec-12 The inbox and its items; sec-12a Inbox ordering |

Notes on the rewrite:

- The `aria-current` sentence of WL-REQ-331 stays in sec-2.1 with the destinations it marks; sec-2.1a holds only the below-880px wrap.
- sec-2.2 keeps the sidebar on every project page, the task page's canonical URL and the `/<ref>` redirect. The bold "Task page.", "Rule page." and "Document page." lead-ins became the headings of sec-2.2a to sec-2.2c.
- sec-6a carries its own "Not built" line, since WL-REQ-341's marker covered both groups. "A fifth panel is a change to this section" now reads "this rule".
- Headings renamed on an original anchor to name its group: sec-1, sec-2.1, sec-3, sec-4, sec-6, sec-8, sec-9, sec-11, sec-12. The store keeps each on its rule through the anchor-only match.
- Positional refs in the rewritten bodies now cite rule refs: §2.5 is WL-REQ-1362, §5.1 WL-REQ-337, §5.3 WL-REQ-339, WL-SPEC-85 §5 WL-REQ-1338, WL-SPEC-83 §9 WL-REQ-1310, WL-SPEC-78 §1.7 WL-REQ-191. Rules outside the split were not touched and still carry section-number references (WL-REQ-333 cites §12, WL-REQ-340 reads "Merged into §9.").

### Kept

None. Every row's groups held on reading.

### too_small merged

None.

### Lineage and kinds (after accept)

No new group is a catalogue, so no kind changes.

```bash
lode rule link "$(rr WL-SPEC-82 sec-1a)" --derived-from WL-REQ-329
lode rule link "$(rr WL-SPEC-82 sec-2.1a)" --derived-from WL-REQ-331
for a in sec-2.2a sec-2.2b sec-2.2c; do lode rule link "$(rr WL-SPEC-82 $a)" --derived-from WL-REQ-332; done
for a in sec-3a sec-3b sec-3c; do lode rule link "$(rr WL-SPEC-82 $a)" --derived-from WL-REQ-334; done
lode rule link "$(rr WL-SPEC-82 sec-4a)" --derived-from WL-REQ-335
lode rule link "$(rr WL-SPEC-82 sec-6a)" --derived-from WL-REQ-341
lode rule link "$(rr WL-SPEC-82 sec-8a)" --derived-from WL-REQ-343
for a in sec-9a sec-9b sec-9c sec-9d; do lode rule link "$(rr WL-SPEC-82 $a)" --derived-from WL-REQ-344; done
lode rule link "$(rr WL-SPEC-82 sec-11a)" --derived-from WL-REQ-346
lode rule link "$(rr WL-SPEC-82 sec-12a)" --derived-from WL-REQ-347
```

### Re-pointed edges (after accept)

Every covering plan reaches a split rule only through `supersedes` from a WL-SPEC-32 or WL-SPEC-56 rule (WL-REQ-777, 783 to 789, 997 to 1002). Each plan gains the groups that hold what it builds. None has a direct edge on the first group, so nothing is unlinked. No covering plan is superseded and no governed task is abandoned.

Unchanged: WL-PLAN-81, 83 and 85 cover WL-REQ-335 for the narrow-width behaviour of the surface each builds, which is the first group's named-workflow sentence. WL-PLAN-85 builds the Home cards (sec-11) and defers the brief. WL-PLAN-87 defers the "on behalf of" labelling, so it keeps sec-8 only. WL-1026 (planning-outcome names) sits in the sec-6 Documents row. WL-697, 699, 701, 703 and 704 are Crew page and roster work, sec-8.

```bash
# WL-PLAN-73 (accepted) Task 6 fills the three disclosure layers.
lode doc link WL-PLAN-73 --covers WL-SPEC-82#sec-1a
# WL-PLAN-112 (accepted) builds the below-880px More control and the inbox bucket order with det-v1 ranking.
lode doc link WL-PLAN-112 --covers WL-SPEC-82#sec-2.1a
lode doc link WL-PLAN-112 --covers WL-SPEC-82#sec-12a
# WL-PLAN-74 (accepted) is the templ, HTMX and Tailwind migration: shared assemble* functions and the generated-artifact toolchain.
lode doc link WL-PLAN-74 --covers WL-SPEC-82#sec-3b
lode doc link WL-PLAN-74 --covers WL-SPEC-82#sec-3c
# WL-PLAN-104 (accepted) shows an agent as a delegate on work, never an owner.
lode doc link WL-PLAN-104 --covers WL-SPEC-82#sec-8a
# WL-PLAN-97 (accepted) Task 7 groups the Deliverables page by milestone.
lode doc link WL-PLAN-97 --covers WL-SPEC-82#sec-9a
# WL-PLAN-99 (accepted) teaches the /reviews queue every entity kind.
lode doc link WL-PLAN-99 --covers WL-SPEC-82#sec-9b
# WL-PLAN-100 (accepted) builds GET /approvals/{id} with the impact note and the exception act.
lode doc link WL-PLAN-100 --covers WL-SPEC-82#sec-9c
# WL-PLAN-103 (accepted) builds the Morning Brief and "Reviewed through now".
lode doc link WL-PLAN-103 --covers WL-SPEC-82#sec-11a

# WL-1013 resolves the X-Requested-With value, which now sits in the write origin gates.
lode task govern   WL-1013 --by "$(rr WL-SPEC-82 sec-3a)"
lode task ungovern WL-1013 --by WL-REQ-334
```

Findings outside this pass:

- WL-REQ-340 (sec-5.4) reads "Merged into §9.", a positional ref. After accept the definition of done sits in sec-9a; the body should cite that rule.
- WL-REQ-343's row notes an overlap with WL-REQ-47 on agents never joining the Crew. sec-8a states only the delegate display; the roster fact stays with WL-REQ-47.

## WL-SPEC-83 The web app: a React SPA on the Go server

Rules: 11 before, 14 after accept (3 new sections, 0 merged). Revision: opened on doc 570, body written, not accepted. The candidate from 2026-10-09 that the brief expected had already landed as version 2: `lode doc show` reported no open revision, and `lode doc revise` opened a new one without refusal.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-1307 | sec-6 | sec-6 API access from the SPA; sec-6a Checks on a cookie-authorized write; sec-6b Session-only routes; sec-6c View preferences stay in the browser |

The paragraph on porting templ-only routes to `/api/v1` moved up into sec-6, since it says which routes the SPA reaches on `/api/v1`. Positional refs now cite rules: WL-SPEC-85 §5 is WL-REQ-1338, WL-SPEC-75 §13.5 is WL-REQ-123 (the web-session approving sentence moves to sec-13.5a when the WL-SPEC-75 candidate lands), and §4 is WL-REQ-1305.

### Kept

None.

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
for a in sec-6a sec-6b sec-6c; do lode rule link "$(rr WL-SPEC-83 $a)" --derived-from WL-REQ-1307; done
```

### Re-pointed edges (after accept)

```bash
# WL-PLAN-147 (draft) builds cookie auth on /api/v1 (Task 8), the write checks (Task 9) and session-only routes (Task 8). It does not touch view preferences.
lode doc link WL-PLAN-147 --covers WL-SPEC-83#sec-6a
lode doc link WL-PLAN-147 --covers WL-SPEC-83#sec-6b
# WL-1013 resolves the X-Requested-With header value: the write-checks group.
lode task govern   WL-1013 --by "$(rr WL-SPEC-83 sec-6a)"
lode task ungovern WL-1013 --by WL-REQ-1307
```

## WL-SPEC-84 Review

Rules: 21 before, 30 after accept (9 new sections, 0 merged). Revision: opened on doc 574, body written, not accepted.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-1316 | sec-1.1 | sec-1.1 What Worklode owns and crit as an interim client; sec-1.1a crit's `Comment` shape is the anchoring contract |
| WL-REQ-1318 | sec-3 | sec-3 Thread intents; sec-3a Question threads (placed before sec-3.1) |
| WL-REQ-1323 | sec-6 | sec-6 Review API routes; sec-6a `lode review` commands and clients; sec-6b The crit bridge |
| WL-REQ-1324 | sec-7 | sec-7 Gate effects of a review; sec-7a Verdict validity and round invalidation; sec-7b Review task minting |
| WL-REQ-1325 | sec-8 | sec-8 Who opens a change review; sec-8a Reviewer assignment and merge hold |
| WL-REQ-1327 | sec-10 | sec-10 Review metrics; sec-10a Review events; sec-10b Review ontology terms and generated code |

Notes on the rewrite:

- The **Clients** paragraph of WL-REQ-1323 went to sec-6a with the command table, since it says which verbs each client uses. The row's three groups did not place it.
- sec-7 keeps its lead sentence ("The gates stay where they are; the review supplies evidence.") with the document and change bullets.
- sec-10b says "one `wl:Event` subclass per review event" where the old text said "per event", since the event list is now its own section.
- Positional references in the rewritten bodies now cite rules: the `PUT .../guide` row cites WL-RULE-1320 (was §4), and sec-8a cites WL-REQ-1324 (was §7). The `#sec-4.3` in the crit bridge is an example anchor and stays. Rules outside the split, including sec-3.1 and sec-not-built, still carry section-number references.

### Kept

None. Every row's groups held on reading.

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-84 sec-1.1a)" --derived-from WL-REQ-1316
lode rule link "$(rr WL-SPEC-84 sec-3a)"   --derived-from WL-REQ-1318
lode rule link "$(rr WL-SPEC-84 sec-6a)"   --derived-from WL-REQ-1323
lode rule set  "$(rr WL-SPEC-84 sec-6a)"   --kind catalogue
lode rule link "$(rr WL-SPEC-84 sec-6b)"   --derived-from WL-REQ-1323
lode rule link "$(rr WL-SPEC-84 sec-7a)"   --derived-from WL-REQ-1324
lode rule link "$(rr WL-SPEC-84 sec-7b)"   --derived-from WL-REQ-1324
lode rule link "$(rr WL-SPEC-84 sec-8a)"   --derived-from WL-REQ-1325
lode rule link "$(rr WL-SPEC-84 sec-10a)"  --derived-from WL-REQ-1327
lode rule set  "$(rr WL-SPEC-84 sec-10a)"  --kind catalogue
lode rule link "$(rr WL-SPEC-84 sec-10b)"  --derived-from WL-REQ-1327
lode rule set  "$(rr WL-SPEC-84 sec-10b)"  --kind catalogue
```

WL-REQ-1318, WL-REQ-1323 and WL-REQ-1327 stay `catalogue`: each keeps its table (intents, routes, metrics).

### Re-pointed edges (after accept)

All four covering plans are drafts. No plan covers WL-REQ-1325, so sec-8a gains no plan. WL-1022 (when a task enters `in_review`, WL-REQ-1324 against WL-REQ-1325) is unchanged: both sentences it names stay in the first groups, sec-7 and sec-8.

```bash
# WL-PLAN-132 (draft) builds the record over the API: the question rule, the ten verbs, base_ref verdict validity, review-task minting, metrics, events and the ns/ terms. It builds no golden copy and no crit bridge.
lode doc link WL-PLAN-132 --covers WL-SPEC-84#sec-3a
lode doc link WL-PLAN-132 --covers WL-SPEC-84#sec-6a
lode doc link WL-PLAN-132 --covers WL-SPEC-84#sec-7a
lode doc link WL-PLAN-132 --covers WL-SPEC-84#sec-7b
lode doc link WL-PLAN-132 --covers WL-SPEC-84#sec-10a
lode doc link WL-PLAN-132 --covers WL-SPEC-84#sec-10b
# WL-PLAN-133 (draft) ports crit's four-stage ladder, enforces the question rule on change rounds, adds the change arm of the verbs and the reviewers route, moves base_ref on the author round, and mints review tasks with a blocks edge.
lode doc link WL-PLAN-133 --covers WL-SPEC-84#sec-1.1a
lode doc link WL-PLAN-133 --covers WL-SPEC-84#sec-3a
lode doc link WL-PLAN-133 --covers WL-SPEC-84#sec-6a
lode doc link WL-PLAN-133 --covers WL-SPEC-84#sec-7a
lode doc link WL-PLAN-133 --covers WL-SPEC-84#sec-7b
# WL-PLAN-134 (draft) adds the guide routes and `lode review guide` / `set guide`; of the sec-10 groups it moves only the two skim metrics.
lode doc link WL-PLAN-134 --covers WL-SPEC-84#sec-6a
# WL-PLAN-135 (draft) pins the golden Comment contract, builds `lode review exec` and `import`, the bridge, and the read-only web view (a cockpit page, not an /api/v1 route).
lode doc link   WL-PLAN-135 --covers WL-SPEC-84#sec-1.1a
lode doc link   WL-PLAN-135 --covers WL-SPEC-84#sec-6a
lode doc link   WL-PLAN-135 --covers WL-SPEC-84#sec-6b
lode doc unlink WL-PLAN-135 --covers WL-SPEC-84#sec-6

# WL-1023 is the conflict over whether the web client produces rounds: the "Web UI ... produces no rounds" sentence is in the Clients paragraph, now sec-6a.
lode task govern   WL-1023 --by "$(rr WL-SPEC-84 sec-6a)"
lode task ungovern WL-1023 --by WL-REQ-1323
```

Finding outside this pass: the four review plans cite spec 059 section numbers (§11 events, §8 verdicts, §12) that no longer match WL-SPEC-84's anchors.

## WL-SPEC-85 The project Progress page

Rules: 10 before, 19 after accept (9 new sections, 0 merged). Revision: opened on doc 575, body written, not accepted.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-1336 | sec-3 | sec-3 Page and row layout; sec-3a Hover text and the task position |
| WL-REQ-1337 | sec-4 | sec-4 Two-step action buttons; sec-4a The action catalogue; sec-4b Rally membership; sec-4c Queue for merge, or Merge |
| WL-REQ-1338 | sec-5 | sec-5 Write route safety; sec-5a The stream is read-only |
| WL-REQ-1339 | sec-6 | sec-6 The live stream and its frame; sec-6a Fragment refresh and swap; sec-6b Motion and layout stability |
| WL-REQ-1341 | sec-8 | sec-8 Assembly; sec-8a Routes and their guards; sec-8b Metrics |

The "no percentage" sentence of WL-REQ-1336 moved from the end of the hover text into sec-3, the layout it constrains. `frame-ancestors 'none'` stays in sec-5: it covers JSON replies as well as fragments. In WL-REQ-1337, "§5 is the CSRF protection" now cites WL-REQ-1338, "(§7)" cites WL-REQ-1340, and the two "below" cells of the action table cite `WL-SPEC-85#sec-4b` and `WL-SPEC-85#sec-4c`. Headings renamed on an original anchor to name its group: sec-3, sec-4, sec-5, sec-6 and sec-8.

### Kept

None. Every row's groups held on reading.

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-85 sec-3a)" --derived-from WL-REQ-1336
lode rule set  "$(rr WL-SPEC-85 sec-3a)" --kind catalogue
for a in sec-4a sec-4b sec-4c; do lode rule link "$(rr WL-SPEC-85 $a)" --derived-from WL-REQ-1337; done
lode rule set  "$(rr WL-SPEC-85 sec-4a)" --kind catalogue
lode rule link "$(rr WL-SPEC-85 sec-5a)" --derived-from WL-REQ-1338
for a in sec-6a sec-6b; do lode rule link "$(rr WL-SPEC-85 $a)" --derived-from WL-REQ-1339; done
for a in sec-8a sec-8b; do lode rule link "$(rr WL-SPEC-85 $a)" --derived-from WL-REQ-1341; done
lode rule set  "$(rr WL-SPEC-85 sec-8a)" --kind catalogue
lode rule set  "$(rr WL-SPEC-85 sec-8b)" --kind catalogue
```

The `for` lines expand to 9 `link` commands; 13 commands in all.

### Re-pointed edges (after accept)

The four covering plans, WL-PLAN-136 to 139, are the Progress series and all stale. Unchanged: WL-PLAN-137 keeps sec-3 (the action slot in the row) and sec-5 (the JSON write guard); WL-PLAN-139 keeps sec-5 (rule 9, the merge route's actor); WL-PLAN-136 to 139 keep sec-8 (store reader, derivation, handlers). WL-1013, the `X-Requested-With` conflict, is governed by WL-REQ-1338 and stays there: the header rule is rule 3 of sec-5.

```bash
# WL-PLAN-136 (stale, Progress 1/4) builds the read-only page, the position ladder (Task 3) and the GET page route.
lode doc link WL-PLAN-136 --covers WL-SPEC-85#sec-3a
lode doc link WL-PLAN-136 --covers WL-SPEC-85#sec-8a
# WL-PLAN-137 (stale, Progress 2/4) builds the two-step buttons, Accept, Plan, Review, the rally, the write routes and worklode_progress_writes_total.
lode doc link WL-PLAN-137 --covers WL-SPEC-85#sec-4a
lode doc link WL-PLAN-137 --covers WL-SPEC-85#sec-4b
lode doc link WL-PLAN-137 --covers WL-SPEC-85#sec-8a
lode doc link WL-PLAN-137 --covers WL-SPEC-85#sec-8b
# WL-PLAN-138 (stale, Progress 3/4) builds the read-only stream, fragments, swap, pulse and layout stability, their routes and stream metrics; no write route.
lode doc link   WL-PLAN-138 --covers WL-SPEC-85#sec-5a
lode doc link   WL-PLAN-138 --covers WL-SPEC-85#sec-6a
lode doc link   WL-PLAN-138 --covers WL-SPEC-85#sec-6b
lode doc link   WL-PLAN-138 --covers WL-SPEC-85#sec-8a
lode doc link   WL-PLAN-138 --covers WL-SPEC-85#sec-8b
lode doc unlink WL-PLAN-138 --covers WL-SPEC-85#sec-5
# WL-PLAN-139 (stale, Progress 4/4) adds `queued for merge` to the ladder and the pinned tooltip's merge button, its route and write metric; not the page layout or the two-step button.
lode doc link   WL-PLAN-139 --covers WL-SPEC-85#sec-3a
lode doc link   WL-PLAN-139 --covers WL-SPEC-85#sec-4a
lode doc link   WL-PLAN-139 --covers WL-SPEC-85#sec-4c
lode doc link   WL-PLAN-139 --covers WL-SPEC-85#sec-8a
lode doc link   WL-PLAN-139 --covers WL-SPEC-85#sec-8b
lode doc unlink WL-PLAN-139 --covers WL-SPEC-85#sec-3
lode doc unlink WL-PLAN-139 --covers WL-SPEC-85#sec-4
```

Finding outside this pass: WL-PLAN-138 names the stream metrics `worklode_progress_stream_frames_total{event}` and `worklode_progress_stream_connections`. The code and WL-REQ-1341 use `worklode_progress_stream_frames_sent_total` and `worklode_progress_streams_active`, so the plan text is stale, not the spec.

## WL-SPEC-86 Automation and unattended execution

Rules: 4 before, 10 after (6 new sections, 0 merged). WL-SPEC-86 is a draft, so `lode doc revise` refuses it ("only an accepted document is revised"). The split was written in place with `lode doc edit WL-SPEC-86 --if-version 1`, which minted the new rules at once as drafts. Lineage and kinds were run, so nothing is left for after an accept.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-RULE-1344 | sec-2 | sec-2 No planning before the spec is accepted; sec-2a Spec acceptance is a human decision; sec-2b Plan acceptance is a separate decision; sec-2c Plan acceptance mints its declared tasks; sec-2d Automatic execution stays within saved bounds |
| WL-REQ-1346 | sec-4 | sec-4 The unattended-run confirmation surface; sec-4a The live run view; sec-4b Failure, retry and pause |

The sec-2 heading changed from "Invariants" to its first invariant. Each bullet became a one-sentence section.

### Kept

None.

### too_small merged

None.

### Lineage and kinds (run)

```bash
for r in WL-REQ-1369 WL-REQ-1370 WL-REQ-1371 WL-REQ-1372; do
  lode rule set  $r --kind invariant        # renamed WL-RULE-1369 to WL-RULE-1372
  lode rule link $r --derived-from WL-RULE-1344
done
lode rule link WL-REQ-1373 --derived-from WL-REQ-1346
lode rule link WL-REQ-1374 --derived-from WL-REQ-1346
```

### Re-pointed edges

None. No plan covers and no task is governed by WL-RULE-1344 or WL-REQ-1346.
