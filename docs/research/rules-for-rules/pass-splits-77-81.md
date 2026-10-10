# Corpus pass: splits in WL-SPEC-77 to 81

WL-1036, WL-PLAN-149 Task 5. Applies tests 3 and 4 of WL-SPEC-77 sec-4c to the rules arranged in WL-SPEC-77 to 81, from the rows in `v2result0.jsonl` to `v2result3.jsonl`, with the conventions of `pass-splits-72-76.md`. A `too_big` rule becomes one section per decision group: the original anchor keeps the first group and each new group gets a letter-suffixed anchor (WL-SPEC-77 sec-4). A group under an already-lettered anchor takes the doubled-letter form (sec-4a gains sec-4aa). A `too_small` rule's text moves into its parent; its anchor stays, with the body "Merged into WL-REQ-n.". Rules with verdict `template` are not split. WL-REQ-165 and WL-REQ-170, the closure hubs, were split first.

Each spec has an open candidate revision with the new body. None is accepted: the owner reviews and runs `lode doc revise <spec> --accept`. A revision mints its new rules only when it lands, so lineage, kind changes, merges and re-pointed `covers` and `governedBy` edges are listed below as commands to run after each accept. Superseded plans and abandoned tasks are not re-pointed.

`rr` resolves a spec anchor to the rule arranged there:

```bash
rr() { lode rule list --doc "$1" --json | jq -r --arg d "$1" --arg a "$2" \
  '.[] | select(any(.arranged_in[]; .doc_ref == $d and .anchor == $a)) | .ref'; }
```

## Summary

| Spec | Before | After accept | Rules split | Kept | `too_small` merged | Revision | Post-accept commands |
|---|---|---|---|---|---|---|---|
| WL-SPEC-77 | 37 | 92 | 19 | 0 | 0 | opened | 187 |
| WL-SPEC-78 | 53 | 73 | 11 | 0 | 0 | opened | 34 |
| WL-SPEC-79 | 29 | 47 | 12 | 0 | 0 | opened | 52 |
| WL-SPEC-80 | 47 | 64 | 14 | 0 | 1 | opened | 37 |
| WL-SPEC-81 | 26 | 42 | 12 | 0 | 0 | opened | 37 |
| Total | 192 | 318 | 68 | 0 | 1 | 5 of 5 | 347 |

"After accept" counts the new sections and subtracts the merged rule, which leaves the count when `lode rule supersede` withdraws it. No revision was refused for an already-open candidate. WL-REQ-1367 and WL-REQ-1368 (WL-SPEC-77 sec-4c, sec-4d) have no row and are untouched; WL-PLAN-149 covers only those two, so it needs no re-point.

Specs over about forty rules after the pass, listed as split candidates for the owner and not split here: WL-SPEC-77 (92), WL-SPEC-78 (73), WL-SPEC-79 (47), WL-SPEC-80 (64), WL-SPEC-81 (42).

`lode rule list --json` returns empty `covered_by` and `governed_tasks` for every rule. The edges here were read with `lode rule show <ref> --json`, which returns them.

## WL-SPEC-77 Documents

Rules: 37 before, 92 after accept (55 new sections, 0 merged). Revision: opened on doc 560, body written, not accepted. Over the forty-rule target: split candidate for the owner.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-165 | sec-4 | sec-4 Sections and anchors; sec-4e Anchor depth; sec-4f What one section class expresses; sec-4g Every anchored section is a design rule; sec-4h Reading rules; sec-4i Rule edges, manual and derived; sec-4j Rule owner, tags and kind; sec-4k Rule kinds; sec-4l An invariant governs every task; sec-4m Amendment; sec-4n Rule lineage; sec-4o The refactor: `lode rule supersede`; sec-4p Plans cover whole requirements (sec-4a to sec-4d are taken) |
| WL-REQ-170 | sec-9 | sec-9 Editorial lifecycle; sec-9a Derived supersession; sec-9b Owner and reviewers; sec-9c Acceptance is a deliberate human act; sec-9d Submit; sec-9e Revising an accepted spec; sec-9f Edit and compare-and-swap; sec-9g Grooming; sec-9h Plan lifecycle |
| WL-REQ-163 | sec-2 | sec-2 Document kinds; sec-2a Document classes in the ontology; sec-2b Documents are not closeable; sec-2c The cardinality test; sec-2d The authoring task closes on submission |
| WL-REQ-164 | sec-3 | sec-3 The store; sec-3a One version bump per node kind; sec-3b Candidate edges; sec-3c Plan ordering is one row |
| WL-REQ-1355 | sec-4a | sec-4a Editing one rule; sec-4aa A rule's canonical page |
| WL-REQ-1356 | sec-4b | sec-4b Governing links; sec-4ba Link version and pinning; sec-4bb Governing links under supersession |
| WL-REQ-166 | sec-5 | sec-5 Versioning; sec-5a The DCAT projection and atomic publication; sec-5b Section staleness without section versions |
| WL-REQ-168 | sec-7 | sec-7 Stored bodies carry no header; sec-7b The title; sec-7c Header keys; sec-7d References carry the section; sec-7e The shorthand (sec-7a is taken) |
| WL-REQ-1357 | sec-7a | sec-7a Resolving a reference; sec-7aa A spec number keeps its meaning |
| WL-REQ-1288 | sec-8.1 | sec-8.1 Edge ownership; sec-8.1a Stored edge types; sec-8.1b Edge terms in `ns/` and the graph projection |
| WL-REQ-171 | sec-10 | sec-10 The escalation ladder; sec-10a In-place amendment is allowed when nothing refers to the section; sec-10b Substantive is decided in two parts; sec-10c Notes; sec-10d Tier routing |
| WL-REQ-174 | sec-11.2 | sec-11.2 Acceptance publishes the tasks; sec-11.2a A plan's task set; sec-11.2b Who owns what in a plan |
| WL-REQ-176 | sec-12 | sec-12 Task kinds; sec-12a A decision's data |
| WL-REQ-177 | sec-13 | sec-13 Implementation coverage; sec-13a The repo-implements deriver; sec-13b Coverage queries; sec-13c Authorship; sec-13d Project and Milestone |
| WL-REQ-180 | sec-16 | sec-16 Importing a corpus: `lode doc import`; sec-16a Dangling references are reported |
| WL-REQ-182 | sec-18 | sec-18 Surfaces; sec-18a Permissions |
| WL-REQ-1295 | sec-19.1 | sec-19.1 Normative text lives only in rules; sec-19.1a Spec headings and the spec write gate |
| WL-REQ-1298 | sec-19.4 | sec-19.4 A rule changes through its own versions; sec-19.4a Accepting a rule version |
| WL-REQ-1299 | sec-19.5 | sec-19.5 A spec body is rendered, not stored; sec-19.5a The editable form |

Anchor choice under an already-lettered anchor: WL-SPEC-77 sec-4 says an insert between `2.1a` and `2.1b` is `2.1aa`, so a group split out of sec-4a is sec-4aa, out of sec-4b sec-4ba and sec-4bb, and out of sec-7a sec-7aa. Each sits directly after its parent. The new groups of WL-REQ-165 take the next free letters, sec-4e to sec-4p, and sit after sec-4d. The groups of WL-REQ-168 take sec-7b to sec-7e and sit after sec-7aa. sec-16a sits after sec-16.1. WL-REQ-1367 (sec-4c) and WL-REQ-1368 (sec-4d) are untouched.

Text moved and not split: WL-REQ-171's "Stale plans" paragraph restates WL-REQ-170's plan lifecycle with the in-place amendment trigger added, so it moved into sec-9h. WL-REQ-165's "One model at every size. Informative." tail decides nothing; it stays in sec-4 and is a template-text candidate. WL-REQ-182's last paragraph is split: the roles sentence goes to sec-18a, the crit, plugin and cockpit pointers stay in sec-18. Headings renamed on an original anchor to name its group: sec-7, sec-10, sec-12, sec-16 (its old heading "The backbone is the only home" described WL-REQ-1247, not this body) and sec-18. The store keeps each one on its rule through the anchor-only match. In every rewritten body, positional references (`section N`, `§N`, "above", "below") now cite a rule ref or a `WL-SPEC-NN#sec-x` anchor.

### Kept

None.

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
for a in sec-2a sec-2b sec-2c sec-2d; do lode rule link "$(rr WL-SPEC-77 $a)" --derived-from WL-REQ-163; done
for a in sec-3a sec-3b sec-3c; do lode rule link "$(rr WL-SPEC-77 $a)" --derived-from WL-REQ-164; done
lode rule set  WL-REQ-164 --kind catalogue
lode rule link "$(rr WL-SPEC-77 sec-4e)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4f)"  --derived-from WL-REQ-165
lode rule set  "$(rr WL-SPEC-77 sec-4f)"  --kind catalogue
lode rule link "$(rr WL-SPEC-77 sec-4g)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4h)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4i)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4j)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4k)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4l)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4m)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4n)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4o)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4p)"  --derived-from WL-REQ-165
lode rule link "$(rr WL-SPEC-77 sec-4aa)" --derived-from WL-REQ-1355
lode rule link "$(rr WL-SPEC-77 sec-4ba)" --derived-from WL-REQ-1356
lode rule link "$(rr WL-SPEC-77 sec-4bb)" --derived-from WL-REQ-1356
lode rule link "$(rr WL-SPEC-77 sec-5a)"  --derived-from WL-REQ-166
lode rule link "$(rr WL-SPEC-77 sec-5b)"  --derived-from WL-REQ-166
lode rule link "$(rr WL-SPEC-77 sec-7b)"  --derived-from WL-REQ-168
lode rule link "$(rr WL-SPEC-77 sec-7c)"  --derived-from WL-REQ-168
lode rule set  "$(rr WL-SPEC-77 sec-7c)"  --kind catalogue
lode rule link "$(rr WL-SPEC-77 sec-7d)"  --derived-from WL-REQ-168
lode rule link "$(rr WL-SPEC-77 sec-7e)"  --derived-from WL-REQ-168
lode rule link "$(rr WL-SPEC-77 sec-7aa)" --derived-from WL-REQ-1357
lode rule link "$(rr WL-SPEC-77 sec-8.1a)" --derived-from WL-REQ-1288
lode rule set  "$(rr WL-SPEC-77 sec-8.1a)" --kind catalogue
lode rule link "$(rr WL-SPEC-77 sec-8.1b)" --derived-from WL-REQ-1288
for a in sec-9a sec-9b sec-9c sec-9d sec-9e sec-9f sec-9g sec-9h; do lode rule link "$(rr WL-SPEC-77 $a)" --derived-from WL-REQ-170; done
for a in sec-10a sec-10b sec-10c sec-10d; do lode rule link "$(rr WL-SPEC-77 $a)" --derived-from WL-REQ-171; done
lode rule link "$(rr WL-SPEC-77 sec-11.2a)" --derived-from WL-REQ-174
lode rule link "$(rr WL-SPEC-77 sec-11.2b)" --derived-from WL-REQ-174
lode rule set  "$(rr WL-SPEC-77 sec-11.2b)" --kind catalogue
lode rule link "$(rr WL-SPEC-77 sec-12a)"  --derived-from WL-REQ-176
lode rule link "$(rr WL-SPEC-77 sec-13a)"  --derived-from WL-REQ-177
lode rule link "$(rr WL-SPEC-77 sec-13b)"  --derived-from WL-REQ-177
lode rule set  "$(rr WL-SPEC-77 sec-13b)"  --kind catalogue
lode rule link "$(rr WL-SPEC-77 sec-13c)"  --derived-from WL-REQ-177
lode rule link "$(rr WL-SPEC-77 sec-13d)"  --derived-from WL-REQ-177
lode rule link "$(rr WL-SPEC-77 sec-16a)"  --derived-from WL-REQ-180
lode rule link "$(rr WL-SPEC-77 sec-18a)"  --derived-from WL-REQ-182
lode rule set  "$(rr WL-SPEC-77 sec-18a)"  --kind catalogue
lode rule link "$(rr WL-SPEC-77 sec-19.1a)" --derived-from WL-REQ-1295
lode rule link "$(rr WL-SPEC-77 sec-19.4a)" --derived-from WL-REQ-1298
lode rule link "$(rr WL-SPEC-77 sec-19.5a)" --derived-from WL-REQ-1299
```

The four `for` lines expand to 19 commands: 55 `link` and 7 `set` commands in all. WL-REQ-164 keeps sec-3, which is now only the store's table catalogue, so it becomes a `catalogue` too.

### Re-pointed edges (after accept)

Most covering plans reach a split rule only through `supersedes` from a WL-SPEC-25 rule (WL-REQ-650 to WL-REQ-699, WL-REQ-715). Each plan gains the groups that hold its predecessors' text. These plans have no direct edge on the first group, so nothing can be unlinked from them. Superseded plans (WL-PLAN-69, 70, 71) and abandoned tasks (WL-935, WL-936) are skipped.

Unchanged: WL-PLAN-149 (covers sec-4c and sec-4d only, neither split). WL-PLAN-59, 61 and 62 (stale) and WL-PLAN-141 and 142 (draft) cover WL-REQ-182 only through the surface tables WL-REQ-715 and WL-REQ-1194, which are the first group. WL-966 (gate, `adr` retirement in sec-2), WL-1015 (sec-3 table and sec-19.5) and WL-1016 (sec-4a and sec-19.4) are governed only by first groups.

```bash
# WL-PLAN-5 (spent) Tasks 1-2 build the deriver's pin annotation, Tasks 3-4 the coverage and delivered-coverage queries.
lode doc link WL-PLAN-5 --covers WL-SPEC-77#sec-13a
lode doc link WL-PLAN-5 --covers WL-SPEC-77#sec-13b
# WL-PLAN-31 (stale) covers all of WL-SPEC-25 through a whole-document entry, so it gets every group whose text came from a WL-SPEC-25 rule.
for a in sec-2a sec-2b sec-3a sec-4e sec-4f sec-5a sec-5b sec-7c sec-7d sec-7e sec-9b sec-9c sec-9e sec-9g sec-9h sec-10a sec-10b sec-10c sec-10d sec-12a sec-13a sec-13c sec-13d; do lode doc link WL-PLAN-31 --covers WL-SPEC-77#$a; done
# WL-PLAN-57 (accepted) swaps the task kinds (WL-REQ-685, 686) and edits Project in the ontology (WL-REQ-695).
lode doc link WL-PLAN-57 --covers WL-SPEC-77#sec-12a
lode doc link WL-PLAN-57 --covers WL-SPEC-77#sec-13d
# WL-PLAN-58 (accepted) builds the store, the anchor depth (WL-REQ-666), accept and revise, and reviewers (WL-REQ-668 to 671).
lode doc link WL-PLAN-58 --covers WL-SPEC-77#sec-4e
lode doc link WL-PLAN-58 --covers WL-SPEC-77#sec-9b
lode doc link WL-PLAN-58 --covers WL-SPEC-77#sec-9c
lode doc link WL-PLAN-58 --covers WL-SPEC-77#sec-9e
# WL-PLAN-60 (accepted) covers the deriver rule WL-REQ-692.
lode doc link WL-PLAN-60 --covers WL-SPEC-77#sec-13a
# WL-PLAN-66 (accepted) builds the shorthand (WL-REQ-699), not the headerless body.
lode doc link WL-PLAN-66 --covers WL-SPEC-77#sec-7e
# WL-PLAN-96 (accepted) builds doc_versions and its snapshot path (WL-REQ-662).
lode doc link WL-PLAN-96 --covers WL-SPEC-77#sec-3a
# WL-PLAN-106 (stale) records built sections: WL-REQ-650 to 656, 661, 669, 670 and 696 to 699.
for a in sec-2a sec-2b sec-4f sec-5b sec-7c sec-7d sec-7e sec-9e; do lode doc link WL-PLAN-106 --covers WL-SPEC-77#$a; done
# WL-PLAN-107 (accepted) builds the decision data (WL-REQ-686).
lode doc link WL-PLAN-107 --covers WL-SPEC-77#sec-12a
# WL-PLAN-108 (accepted) builds the version graphs, lastRevisedIn and the doc_versions snapshot (WL-REQ-657 to 662).
lode doc link WL-PLAN-108 --covers WL-SPEC-77#sec-3a
lode doc link WL-PLAN-108 --covers WL-SPEC-77#sec-5a
lode doc link WL-PLAN-108 --covers WL-SPEC-77#sec-5b
# WL-PLAN-109 (stale) builds the reviewer gate, the referrer query, notes, the in-place amendment path and the depth limit (WL-REQ-666, 671 to 680).
for a in sec-4e sec-9b sec-9g sec-9h sec-10a sec-10b sec-10c sec-10d; do lode doc link WL-PLAN-109 --covers WL-SPEC-77#$a; done
# WL-PLAN-110 (stale) builds escalation, stale marking, grooming and the kind lists (WL-REQ-672 to 680).
for a in sec-9g sec-9h sec-10a sec-10b sec-10c sec-10d; do lode doc link WL-PLAN-110 --covers WL-SPEC-77#$a; done
# WL-PLAN-145 (accepted) builds the bump functions, candidate edges, blockedBy rows, header-key refusals, the edge table and its ns/ pins, and link/unlink on an accepted spec. It builds none of the status machine.
lode doc link   WL-PLAN-145 --covers WL-SPEC-77#sec-3a
lode doc link   WL-PLAN-145 --covers WL-SPEC-77#sec-3b
lode doc link   WL-PLAN-145 --covers WL-SPEC-77#sec-3c
lode doc link   WL-PLAN-145 --covers WL-SPEC-77#sec-7c
lode doc link   WL-PLAN-145 --covers WL-SPEC-77#sec-8.1a
lode doc link   WL-PLAN-145 --covers WL-SPEC-77#sec-8.1b
lode doc link   WL-PLAN-145 --covers WL-SPEC-77#sec-9f
lode doc unlink WL-PLAN-145 --covers WL-SPEC-77#sec-9
# WL-PLAN-146 (accepted) builds spec headings and the write gate, rule-version acceptance, and the editable form.
lode doc link WL-PLAN-146 --covers WL-SPEC-77#sec-19.1a
lode doc link WL-PLAN-146 --covers WL-SPEC-77#sec-19.4a
lode doc link WL-PLAN-146 --covers WL-SPEC-77#sec-19.5a
# WL-PLAN-145 tasks (plan links, kept): each gains the groups it builds.
lode task govern WL-906 --by "$(rr WL-SPEC-77 sec-8.1b)"
lode task govern WL-907 --by "$(rr WL-SPEC-77 sec-8.1b)"
lode task govern WL-908 --by "$(rr WL-SPEC-77 sec-8.1a)"
lode task govern WL-909 --by "$(rr WL-SPEC-77 sec-3c)"
lode task govern WL-909 --by "$(rr WL-SPEC-77 sec-8.1a)"
lode task govern WL-910 --by "$(rr WL-SPEC-77 sec-8.1b)"
lode task govern WL-911 --by "$(rr WL-SPEC-77 sec-3a)"
lode task govern WL-912 --by "$(rr WL-SPEC-77 sec-3b)"
lode task govern WL-914 --by "$(rr WL-SPEC-77 sec-9f)"
lode task govern WL-915 --by "$(rr WL-SPEC-77 sec-7c)"
# WL-PLAN-146 tasks (plan links, kept).
lode task govern WL-1043 --by "$(rr WL-SPEC-77 sec-19.1a)"
lode task govern WL-1045 --by "$(rr WL-SPEC-77 sec-19.4a)"
lode task govern WL-1046 --by "$(rr WL-SPEC-77 sec-19.1a)"
lode task govern WL-1047 --by "$(rr WL-SPEC-77 sec-19.5a)"
# WL-886: a docs-only design task that never closes is the authoring-task-closes rule.
lode task govern   WL-886 --by "$(rr WL-SPEC-77 sec-2d)"
lode task ungovern WL-886 --by WL-REQ-163
# WL-1024, WL-1025: the implements-edge conflict is in the stored edge table and the coverage-query paragraph.
lode task govern   WL-1024 --by "$(rr WL-SPEC-77 sec-8.1a)"
lode task ungovern WL-1024 --by WL-REQ-1288
lode task govern   WL-1025 --by "$(rr WL-SPEC-77 sec-8.1a)"
lode task govern   WL-1025 --by "$(rr WL-SPEC-77 sec-13b)"
lode task ungovern WL-1025 --by WL-REQ-1288
lode task ungovern WL-1025 --by WL-REQ-177
# WL-REQ-165 tasks: none builds anchor syntax or numbering.
lode task govern   WL-981 --by "$(rr WL-SPEC-77 sec-4k)"   # kind change refused on a covered rule
lode task ungovern WL-981 --by WL-REQ-165
lode task govern   WL-934 --by "$(rr WL-SPEC-77 sec-4k)"   # rule kinds and WL-REQ refs
lode task ungovern WL-934 --by WL-REQ-165
lode task govern   WL-926 --by "$(rr WL-SPEC-77 sec-4k)"   # non-normative rules never a gap
lode task ungovern WL-926 --by WL-REQ-165
lode task govern   WL-930 --by "$(rr WL-SPEC-77 sec-4p)"   # split rules several plans build part of
lode task ungovern WL-930 --by WL-REQ-165
lode task govern   WL-929 --by "$(rr WL-SPEC-77 sec-4p)"   # retire coverage levels
lode task ungovern WL-929 --by WL-REQ-165
lode task govern   WL-928 --by "$(rr WL-SPEC-77 sec-4p)"   # coverage:none plan entries
lode task ungovern WL-928 --by WL-REQ-165
lode task govern   WL-895 --by "$(rr WL-SPEC-77 sec-4p)"   # covers as plan-to-rule edges
lode task ungovern WL-895 --by WL-REQ-165
lode task govern   WL-900 --by "$(rr WL-SPEC-77 sec-4h)"   # rule shown on rendered documents
lode task ungovern WL-900 --by WL-REQ-165
lode task govern   WL-897 --by "$(rr WL-SPEC-77 sec-4o)"   # rules withdrawn without a successor
lode task ungovern WL-897 --by WL-REQ-165
lode task govern   WL-893 --by "$(rr WL-SPEC-77 sec-4m)"   # amends edge and --inline folding
lode task ungovern WL-893 --by WL-REQ-165
lode task govern   WL-894 --by "$(rr WL-SPEC-77 sec-4m)"   # retire document-level amends/replaces
lode task ungovern WL-894 --by WL-REQ-165
lode task govern   WL-896 --by "$(rr WL-SPEC-77 sec-4m)"   # agent surfaces: amendment and covers
lode task govern   WL-896 --by "$(rr WL-SPEC-77 sec-4p)"
lode task ungovern WL-896 --by WL-REQ-165
lode task govern   WL-892 --by "$(rr WL-SPEC-77 sec-4m)"   # spec revision: amendment and supersession on rules
lode task govern   WL-892 --by "$(rr WL-SPEC-77 sec-4n)"
lode task ungovern WL-892 --by WL-REQ-165
lode task govern   WL-890 --by "$(rr WL-SPEC-77 sec-4m)"   # move amendment and supersession to rules
lode task govern   WL-890 --by "$(rr WL-SPEC-77 sec-4n)"
lode task govern   WL-890 --by "$(rr WL-SPEC-77 sec-4o)"
lode task ungovern WL-890 --by WL-REQ-165
```

The `for` lines expand to 45 commands: 71 plan commands and 54 task commands in all, 187 post-accept commands with the lineage block. The `plan` links that WL-PLAN-145 and WL-PLAN-146 tasks hold to first groups are kept, because a plan link is written for every rule the plan covers.

### Findings outside the pass

- F1. WL-PLAN-31 (stale) covers all of WL-SPEC-25 through a whole-document entry. By supersession that gives it every Documents rule, including WL-REQ-170 and WL-REQ-182, which its tasks never build. This over-coverage existed before the split. Narrowing it is a separate change.
- F2. WL-REQ-170 cited WL-SPEC-77 sec-15 for the review-task mint, but sec-15's rule (WL-RULE-179) is withdrawn. sec-9d now cites WL-SPEC-75#sec-9.6. Rules this pass did not touch still use positional refs: sec-4c, sec-4d, sec-14, sec-17, sec-19.3, sec-19.6 and sec-19.7.
- F3. `lode rule list --doc <spec> --json` returns empty `covered_by` and `governed_tasks`. `lode rule show <ref> --json` returns them.
- F4. The opened candidate reports `"edges": null`. WL-SPEC-77 has no outgoing doc edges, so this is expected and needs no action.

## WL-SPEC-78 Design queries, intents, decision decks, meetings, and attachments

Rules: 53 before, 73 after accept (20 new sections, 0 merged). Revision: opened on doc 561, body written, not accepted. Over the forty-rule target: a split candidate for the owner (intents, decks, meetings and blobs, sec-5 to sec-8.9, are separate subjects from the design-doc queries).

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-198 | sec-3.1 | sec-3.1 Forms and defects; sec-3.1a Dangling references |
| WL-REQ-209 | sec-5 | sec-5 Intents; sec-5a The `serves:` key; sec-5b The intent catalogue; sec-5c Intent queries; sec-5d Decision records and ADR retirement |
| WL-REQ-211 | sec-6.1 | sec-6.1 `mode`; sec-6.1a Storing the mode: `body_mode` |
| WL-REQ-212 | sec-6.2 | sec-6.2 Deck body; sec-6.2a The layout catalogue |
| WL-REQ-214 | sec-6.4 | sec-6.4 Decision rows; sec-6.4a Answering a decision row |
| WL-REQ-215 | sec-6.5 | sec-6.5 Rendering, theme, CSP, print (the `Slides` function); sec-6.5a reveal.js and the deck theme; sec-6.5b Decks keep the cockpit CSP; sec-6.5c Printing a deck |
| WL-REQ-216 | sec-6.6 | sec-6.6 Metrics and authoring (deck metrics); sec-6.6a Authoring a deck |
| WL-REQ-223 | sec-7.6 | sec-7.6 Synchronization and resources (Calendar sync); sec-7.6a Meeting resources |
| WL-REQ-224 | sec-7.7 | sec-7.7 Surfaces and events (cockpit surfaces); sec-7.7a Meeting operations in the API and CLI; sec-7.7b Meeting events; sec-7.7c Meetings in the RDF projection |
| WL-REQ-230 | sec-8.5 | sec-8.5 Upload and reference rewriting (upload pipeline and type classes); sec-8.5a Rewriting local image references |
| WL-REQ-231 | sec-8.6 | sec-8.6 Serving hardening and rendering (blob response hardening); sec-8.6a The task body sanitiser; sec-8.6b Page CSP; sec-8.6c Blobs in the CLI and the task brief |

No letter anchors existed in WL-SPEC-78, so every new section takes `a` onward. Original headings are kept on the first group. The "Not built" line of WL-REQ-209 stays in sec-5. The `mode` renderer sentence of WL-REQ-211 moved ahead of the storage text so it stays in sec-6.1. The deck non-goals stay with authoring in sec-6.6a. The out-of-scope paragraph of WL-REQ-224 moved into sec-7.7. In WL-REQ-231, "board and project pages show titles only" moved into sec-8.6a and the task page `img-src`/`media-src` sentence into sec-8.6b. Positional refs in the rewritten bodies now cite WL-REQ-193, WL-REQ-199, WL-REQ-214, WL-REQ-225, WL-REQ-232 or `WL-SPEC-78#sec-8.6a`. The `decisions` layout row cited §6.3 (components); it now cites WL-REQ-214, the decision rows it renders.

### Kept

None.

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-78 sec-3.1a)" --derived-from WL-REQ-198
lode rule link "$(rr WL-SPEC-78 sec-5a)"   --derived-from WL-REQ-209
lode rule link "$(rr WL-SPEC-78 sec-5b)"   --derived-from WL-REQ-209
lode rule set  "$(rr WL-SPEC-78 sec-5b)"   --kind catalogue
lode rule link "$(rr WL-SPEC-78 sec-5c)"   --derived-from WL-REQ-209
lode rule link "$(rr WL-SPEC-78 sec-5d)"   --derived-from WL-REQ-209
lode rule link "$(rr WL-SPEC-78 sec-6.1a)" --derived-from WL-REQ-211
lode rule link "$(rr WL-SPEC-78 sec-6.2a)" --derived-from WL-REQ-212
lode rule set  "$(rr WL-SPEC-78 sec-6.2a)" --kind catalogue
lode rule link "$(rr WL-SPEC-78 sec-6.4a)" --derived-from WL-REQ-214
lode rule link "$(rr WL-SPEC-78 sec-6.5a)" --derived-from WL-REQ-215
lode rule link "$(rr WL-SPEC-78 sec-6.5b)" --derived-from WL-REQ-215
lode rule link "$(rr WL-SPEC-78 sec-6.5c)" --derived-from WL-REQ-215
lode rule link "$(rr WL-SPEC-78 sec-6.6a)" --derived-from WL-REQ-216
lode rule link "$(rr WL-SPEC-78 sec-7.6a)" --derived-from WL-REQ-223
lode rule link "$(rr WL-SPEC-78 sec-7.7a)" --derived-from WL-REQ-224
lode rule set  "$(rr WL-SPEC-78 sec-7.7a)" --kind catalogue
lode rule link "$(rr WL-SPEC-78 sec-7.7b)" --derived-from WL-REQ-224
lode rule link "$(rr WL-SPEC-78 sec-7.7c)" --derived-from WL-REQ-224
lode rule link "$(rr WL-SPEC-78 sec-8.5a)" --derived-from WL-REQ-230
lode rule link "$(rr WL-SPEC-78 sec-8.6a)" --derived-from WL-REQ-231
lode rule link "$(rr WL-SPEC-78 sec-8.6b)" --derived-from WL-REQ-231
lode rule link "$(rr WL-SPEC-78 sec-8.6c)" --derived-from WL-REQ-231
```

### Re-pointed edges (after accept)

No task is governed by a split rule of this spec, and no plan covers WL-REQ-198, 209, 211, 212, 214, 215, 216, 223 or 224. Unchanged: WL-PLAN-46 (accepted, blobs 1/3) builds the streaming upload endpoint and the presigned redirect with its `Content-Disposition`, the first group of WL-REQ-230 and of WL-REQ-231.

```bash
# WL-PLAN-106 (stale) is the retroactive backfill: it claims each covered section fully built, so it covers every group.
lode doc link WL-PLAN-106 --covers WL-SPEC-78#sec-8.5a
lode doc link WL-PLAN-106 --covers WL-SPEC-78#sec-8.6a
lode doc link WL-PLAN-106 --covers WL-SPEC-78#sec-8.6b
lode doc link WL-PLAN-106 --covers WL-SPEC-78#sec-8.6c
# WL-PLAN-47 (accepted, blobs 2/3) builds --body-file rewriting (its Task 7) and blobs in task show and the brief (its Task 8); not the upload pipeline or response hardening.
lode doc link   WL-PLAN-47 --covers WL-SPEC-78#sec-8.5a
lode doc unlink WL-PLAN-47 --covers WL-SPEC-78#sec-8.5
lode doc link   WL-PLAN-47 --covers WL-SPEC-78#sec-8.6c
lode doc unlink WL-PLAN-47 --covers WL-SPEC-78#sec-8.6
# WL-PLAN-48 (accepted, blobs 3/3) builds the goldmark/bluemonday sanitiser and the page CSP (its Tasks 1-2); not blob response hardening.
lode doc link   WL-PLAN-48 --covers WL-SPEC-78#sec-8.6a
lode doc link   WL-PLAN-48 --covers WL-SPEC-78#sec-8.6b
lode doc unlink WL-PLAN-48 --covers WL-SPEC-78#sec-8.6
```

Findings outside this pass:

- WL-PLAN-48 covers WL-REQ-230 but builds neither group: it reuses WL-PLAN-46's upload path for import mirroring and lists video posters (ffmpeg) as a follow-up. Its `covers` edge to sec-8.5 is left as is for the owner.
- Two bodies outside the split cite moved text by position: the sec-6 intro (WL-RULE-210) cites §6.4 for `POST .../decide`, now in sec-6.4a, and the sec-8.3 table (WL-REQ-228) cites §8.5 for reference rewriting, now in sec-8.5a.

## WL-SPEC-79 Knowledge graph and search

Rules: 29 before, 47 after accept (18 new sections, 0 merged). Revision: opened on doc 562, body written, not accepted. Over the forty-rule target: split candidate for the owner.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-237 | sec-2 | sec-2 Vocabulary: reuse and mint; sec-2a The `ns/` source files and generation |
| WL-REQ-238 | sec-3 | sec-3 Classes; sec-3a Disjointness axioms |
| WL-REQ-239 | sec-4 | sec-4 Properties; sec-4a Reused runtime terms; sec-4b Implementation as a query |
| WL-REQ-241 | sec-6 | sec-6 Reasoning tiers; sec-6a SHACL node shapes |
| WL-REQ-247 | sec-10.2 | sec-10.2 Instance grammar; sec-10.2a Named graph IRIs |
| WL-REQ-250 | sec-11 | sec-11 Reading the graph; sec-11a Implementation evidence queries |
| WL-REQ-251 | sec-12 | sec-12 Projection: writing project graphs; sec-12a The project graph's shape; sec-12b Document projection; sec-12c The projection mapping |
| WL-REQ-255 | sec-14.2 | sec-14.2 Embedding space; sec-14.2a Embedding endpoints in deployment |
| WL-REQ-256 | sec-14.3 | sec-14.3 Provider interface; sec-14.3a The embedding space ID |
| WL-REQ-259 | sec-15 | sec-15 Hybrid search: the dense arm; sec-15a The lexical arm; sec-15b Fusion and filters |
| WL-REQ-260 | sec-16 | sec-16 Freshness: the indexer convergence loop; sec-16a Invalidation on a provider change |
| WL-REQ-261 | sec-17 | sec-17 Search surfaces; sec-17a The `search.read` permission; sec-17b Index and search metrics; sec-17c Degraded operation |

No letter anchor existed in this spec, so none was skipped. The SHACL validation-scope sentence (union of project graphs) stays in sec-6 with the OWL/SHACL split: it says how the gate runs, not what a shape requires. The runtime-triples table stays in sec-12c with the mapping table it details. The cockpit search-box pointer stays with the surfaces in sec-17. Positional references in the rewritten bodies now cite WL-REQ-244, WL-REQ-252, WL-REQ-259, WL-SPEC-78#sec-4.6, WL-SPEC-79#sec-3, WL-SPEC-79#sec-6a and WL-SPEC-79#sec-12a. WL-RULE-1290 (sec-19 Verification) has no row in the input and is unchanged.

### Kept

None.

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-79 sec-2a)"    --derived-from WL-REQ-237
lode rule link "$(rr WL-SPEC-79 sec-3a)"    --derived-from WL-REQ-238
lode rule link "$(rr WL-SPEC-79 sec-4a)"    --derived-from WL-REQ-239
lode rule set  "$(rr WL-SPEC-79 sec-4a)"    --kind catalogue
lode rule link "$(rr WL-SPEC-79 sec-4b)"    --derived-from WL-REQ-239
lode rule link "$(rr WL-SPEC-79 sec-6a)"    --derived-from WL-REQ-241
lode rule set  "$(rr WL-SPEC-79 sec-6a)"    --kind catalogue
lode rule link "$(rr WL-SPEC-79 sec-10.2a)" --derived-from WL-REQ-247
lode rule set  "$(rr WL-SPEC-79 sec-10.2a)" --kind catalogue
lode rule link "$(rr WL-SPEC-79 sec-11a)"   --derived-from WL-REQ-250
lode rule link "$(rr WL-SPEC-79 sec-12a)"   --derived-from WL-REQ-251
lode rule link "$(rr WL-SPEC-79 sec-12b)"   --derived-from WL-REQ-251
lode rule link "$(rr WL-SPEC-79 sec-12c)"   --derived-from WL-REQ-251
lode rule set  "$(rr WL-SPEC-79 sec-12c)"   --kind catalogue
lode rule link "$(rr WL-SPEC-79 sec-14.2a)" --derived-from WL-REQ-255
lode rule link "$(rr WL-SPEC-79 sec-14.3a)" --derived-from WL-REQ-256
lode rule link "$(rr WL-SPEC-79 sec-15a)"   --derived-from WL-REQ-259
lode rule link "$(rr WL-SPEC-79 sec-15b)"   --derived-from WL-REQ-259
lode rule link "$(rr WL-SPEC-79 sec-16a)"   --derived-from WL-REQ-260
lode rule link "$(rr WL-SPEC-79 sec-17a)"   --derived-from WL-REQ-261
lode rule link "$(rr WL-SPEC-79 sec-17b)"   --derived-from WL-REQ-261
lode rule set  "$(rr WL-SPEC-79 sec-17b)"   --kind catalogue
lode rule link "$(rr WL-SPEC-79 sec-17c)"   --derived-from WL-REQ-261
```

### Re-pointed edges (after accept)

Unchanged: WL-PLAN-32, 33, 34 and 92 (stale) build the graph read paths of sec-11 only; WL-PLAN-117 (draft) builds the CI tiers of sec-6 and authors no shapes; WL-880 (a rule-ref URL shortcut bug) stays on sec-10.2. WL-PLAN-37 (superseded) is skipped. No plan covers WL-REQ-256, so sec-14.3a gains no plan.

```bash
# WL-PLAN-42 (accepted) checks the runtime classes with their disjointness axiom, the runtime properties and reused PROV terms, and the runtime SHACL shapes, and writes the observed/deploy row-to-triple functions. It builds no reasoning tier and no projector.
lode doc link   WL-PLAN-42 --covers WL-SPEC-79#sec-3a
lode doc link   WL-PLAN-42 --covers WL-SPEC-79#sec-4a
lode doc link   WL-PLAN-42 --covers WL-SPEC-79#sec-6a
lode doc unlink WL-PLAN-42 --covers WL-SPEC-79#sec-6
lode doc link   WL-PLAN-42 --covers WL-SPEC-79#sec-12c
lode doc unlink WL-PLAN-42 --covers WL-SPEC-79#sec-12
# WL-PLAN-35 (accepted) builds subject-complete task triples, the deterministic document a whole-graph PUT takes, and the projection mapping. Its wl:priority/wl:concern work is sec-4 itself.
lode doc link WL-PLAN-35 --covers WL-SPEC-79#sec-12a
lode doc link WL-PLAN-35 --covers WL-SPEC-79#sec-12c
# WL-PLAN-36 (accepted) builds the projector and writes each side of a cross-project edge into its own project graph.
lode doc link WL-PLAN-36 --covers WL-SPEC-79#sec-12a
# WL-PLAN-5 (spent) builds the repo-implements deriver (the wl:implements mapping row) and the delivered-coverage query, not the projector. Its wl:pinnedVersion work is sec-4 itself.
lode doc link   WL-PLAN-5 --covers WL-SPEC-79#sec-4b
lode doc link   WL-PLAN-5 --covers WL-SPEC-79#sec-12c
lode doc unlink WL-PLAN-5 --covers WL-SPEC-79#sec-12
# WL-PLAN-106 (stale) is the retroactive backfill: it claims each covered section fully built, so it covers every group of those rules.
lode doc link WL-PLAN-106 --covers WL-SPEC-79#sec-2a
lode doc link WL-PLAN-106 --covers WL-SPEC-79#sec-3a
lode doc link WL-PLAN-106 --covers WL-SPEC-79#sec-4a
lode doc link WL-PLAN-106 --covers WL-SPEC-79#sec-4b
lode doc link WL-PLAN-106 --covers WL-SPEC-79#sec-11a
lode doc link WL-PLAN-106 --covers WL-SPEC-79#sec-12a
lode doc link WL-PLAN-106 --covers WL-SPEC-79#sec-12b
lode doc link WL-PLAN-106 --covers WL-SPEC-79#sec-12c
# WL-PLAN-122 (draft) builds the spec-status query and lists the evidence-query results through lode graph gaps and drift.
lode doc link WL-PLAN-122 --covers WL-SPEC-79#sec-11a
# WL-PLAN-2 (accepted) builds the whole corpus index and search: sidecar config, both arms and fusion, invalidation (Task 5), the route, permission, metrics and lexical-only degradation.
lode doc link WL-PLAN-2 --covers WL-SPEC-79#sec-14.2a
lode doc link WL-PLAN-2 --covers WL-SPEC-79#sec-15a
lode doc link WL-PLAN-2 --covers WL-SPEC-79#sec-15b
lode doc link WL-PLAN-2 --covers WL-SPEC-79#sec-16a
lode doc link WL-PLAN-2 --covers WL-SPEC-79#sec-17a
lode doc link WL-PLAN-2 --covers WL-SPEC-79#sec-17b
lode doc link WL-PLAN-2 --covers WL-SPEC-79#sec-17c
# WL-881 adds the rule subject kind to the search surfaces and the rule label value to the subject_kind metrics.
lode task govern WL-881 --by "$(rr WL-SPEC-79 sec-17b)"
```

### Findings outside the pass

- `lode rule list --doc <spec> --json` returns empty `covered_by` and `governed_tasks` for every rule; `lode show <rule> --json` returns them.
- Bodies not rewritten here still carry positional references: sec-5 (`§11`), sec-7 (`§8`, `§12`), sec-10.4 (`§10.2`), sec-not-built (`§6`, `§8`, `§10.2`, `§11`), and withdrawn sec-18 (`§14.5`).
- sec-12b (document projection) has no live plan besides the stale backfill WL-PLAN-106.

## WL-SPEC-80 Agent harness and sessions

Rules: 47 before, 64 after accept (18 new sections, 1 merged). Revision: opened on doc 563, body written, not accepted. Over the forty-rule target: a split candidate for the owner (task activity, sec-8.6 to sec-8.9a, and reconciliation and inbox import, sec-10 to sec-11.4, are separate subjects).

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-270 | sec-3.4 | sec-3.4 The path guard; sec-3.4a Task id resolution from a worktree |
| WL-REQ-272 | sec-4 | sec-4 Hooks (event catalogue and guards); sec-4a Merge reporting probe |
| WL-REQ-273 | sec-4.1 | sec-4.1 The `lode-hook` executable and chaining; sec-4.1a Git hook install and coexistence; sec-4.1b Hook timeout and failure policy; sec-4.1c Recorded agent identity |
| WL-REQ-275 | sec-5.1 | sec-5.1 `lode install` (flags); sec-5.1a Config coexistence |
| WL-REQ-278 | sec-5.4 | sec-5.4 Hook delivery per harness; sec-5.4a Brief output per harness |
| WL-REQ-279 | sec-5.5 | sec-5.5 Telemetry: token metrics through Edge Agent; sec-5.5a Log export to the Worklode server; sec-5.5b No content telemetry |
| WL-REQ-281 | sec-6 | sec-6 Status line; sec-6a Status line install slot |
| WL-REQ-290 | sec-8.3 | sec-8.3 Agent-session store functions; sec-8.3a Agent-session HTTP endpoints |
| WL-REQ-1234 | sec-8.6 | sec-8.6 Task activity: the OTLP log ingest route; sec-8.6a Task activity: attribution |
| WL-REQ-1235 | sec-8.7 | sec-8.7 Task activity: storage; sec-8.7a Task activity: retention |
| WL-REQ-1237 | sec-8.9 | sec-8.9 Task activity: the Activity card; sec-8.9a Task activity: the event stream |
| WL-REQ-293 | sec-9 | sec-9 Pi agent integration (package and install); sec-9a Pi extension runtime |
| WL-REQ-297 | sec-10.3 | sec-10.3 `lode task reconcile`: flags and report; sec-10.3a Reconcile engine 1: replay stored events; sec-10.3b Reconcile engine 2: poll GitHub |
| WL-REQ-302 | sec-11.3 | sec-11.3 Staging flags on `lode inbox promote`; sec-11.3a `lode inbox link` |

Notes on the rewrite:

- No new anchor letter was taken; every new section uses `a` onward.
- The `post-merge`/`post-commit` row stays in the sec-4 hook table; the three merge notes moved to sec-4a.
- The `lode-hook otel-headers` bullet stays with the executable in sec-4.1, since it is a `lode-hook` subcommand.
- The `ProjectCost` sentence of WL-REQ-290 moved to the store paragraph of sec-8.3; the wire and naming paragraph went with the endpoints to sec-8.3a.
- The out-of-scope sentence of WL-REQ-1234 (no `/otlp/v1/metrics`, no traces, no Codex logs) stays with the route in sec-8.6.
- WL-REQ-281's location paragraph stays in sec-6 (the row's note named it a possible third subject; it is part of the rendered line).
- The acceptance paragraph of WL-REQ-293 was split by group: settings-merge acceptance and Go tests in sec-9, runtime acceptance and TypeScript tests in sec-9a. The body's opening "It is integrated" now reads "Pi is integrated", since the sentence naming Pi was removed earlier.
- Positional refs in every rewritten body now cite WL-REQ-265, 270, 272, 273, 276, 278, 279, 288, 290, 291, 293, 297, 1234, 1235, 1236, 1237 or a `WL-SPEC-NN#sec-…` anchor. Rules outside the pass (sec-2, sec-12, sec-not-built) still carry `§` refs.

### Kept

None. Every row's groups held on reading.

### too_small merged

| Rule | Into | Change |
|---|---|---|
| WL-REQ-292 (sec-8.5 Error handling) | WL-REQ-291 (sec-8.4 Hook wiring) | The error-handling paragraph became the last bullet of sec-8.4. sec-8.5 keeps its anchor and heading with the body "Merged into WL-REQ-291." |

WL-REQ-292 has no governed task. Its only live covering plan, WL-PLAN-106, already covers WL-REQ-291.

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-80 sec-3.4a)"  --derived-from WL-REQ-270
lode rule link "$(rr WL-SPEC-80 sec-4a)"    --derived-from WL-REQ-272
for a in sec-4.1a sec-4.1b sec-4.1c; do lode rule link "$(rr WL-SPEC-80 $a)" --derived-from WL-REQ-273; done
lode rule link "$(rr WL-SPEC-80 sec-5.1a)"  --derived-from WL-REQ-275
lode rule link "$(rr WL-SPEC-80 sec-5.4a)"  --derived-from WL-REQ-278
for a in sec-5.5a sec-5.5b; do lode rule link "$(rr WL-SPEC-80 $a)" --derived-from WL-REQ-279; done
lode rule link "$(rr WL-SPEC-80 sec-6a)"    --derived-from WL-REQ-281
lode rule link "$(rr WL-SPEC-80 sec-8.3a)"  --derived-from WL-REQ-290
lode rule set  "$(rr WL-SPEC-80 sec-8.3a)"  --kind catalogue
lode rule link "$(rr WL-SPEC-80 sec-8.6a)"  --derived-from WL-REQ-1234
lode rule link "$(rr WL-SPEC-80 sec-8.7a)"  --derived-from WL-REQ-1235
lode rule link "$(rr WL-SPEC-80 sec-8.9a)"  --derived-from WL-REQ-1237
lode rule link "$(rr WL-SPEC-80 sec-9a)"    --derived-from WL-REQ-293
for a in sec-10.3a sec-10.3b; do lode rule link "$(rr WL-SPEC-80 $a)" --derived-from WL-REQ-297; done
lode rule link "$(rr WL-SPEC-80 sec-11.3a)" --derived-from WL-REQ-302
printf 'WL-REQ-292 -> WL-REQ-291\n' | lode rule supersede --map -
```

### Re-pointed edges (after accept)

Superseded plans (WL-PLAN-21, 22, 23, 28, 49) are skipped. WL-REQ-1234, 1235 and 1237 have no covering plan. Unchanged:

- WL-PLAN-68 (accepted) covers spec 008 whole, so it holds edges to WL-REQ-272, 273, 275, 278, 279 and 281 as well. It builds branch and worktree naming and threads the layout through the hooks; of the new groups it builds only id resolution (`Layout.ParseDir`), sec-3.4a.
- WL-PLAN-63 (accepted) builds the adapters' event maps, the first group of WL-REQ-278, and no brief envelope.
- WL-PLAN-40 (accepted) builds the reconcile endpoint and CLI flags, the first group of WL-REQ-297.
- WL-1019 (ready) is the external_id conflict between WL-REQ-105 and WL-REQ-290. The external_id is in `TouchAgentSession`, the store-function group that stays at sec-8.3.

```bash
# WL-PLAN-106 (stale) is the retroactive backfill: it records each covered rule fully built, so it covers every group.
for a in sec-3.4a sec-4a sec-4.1a sec-4.1b sec-4.1c sec-5.1a sec-8.3a sec-10.3a sec-10.3b sec-11.3a; do
  lode doc link WL-PLAN-106 --covers WL-SPEC-80#$a
done
# WL-PLAN-68 (accepted) builds Layout.ParseDir, the task id from the worktree segment.
lode doc link WL-PLAN-68 --covers WL-SPEC-80#sec-3.4a
# WL-PLAN-63 (accepted) builds the installer and four adapters that preserve foreign settings and converge on re-run.
lode doc link WL-PLAN-63 --covers WL-SPEC-80#sec-5.1a
# WL-PLAN-65 (accepted) Task 5 sets statusLine only when absent or Worklode's.
lode doc link WL-PLAN-65 --covers WL-SPEC-80#sec-6a
# WL-PLAN-125 (draft) builds the whole Pi package: settings merge and the extension runtime.
lode doc link WL-PLAN-125 --covers WL-SPEC-80#sec-9a
# WL-PLAN-39 (accepted) builds engine 1 and the data model, server-internal; flags and report ship in WL-PLAN-40.
lode doc link   WL-PLAN-39 --covers WL-SPEC-80#sec-10.3a
lode doc unlink WL-PLAN-39 --covers WL-SPEC-80#sec-10.3
# WL-PLAN-41 (accepted) builds engine 2 and wires it into the report; it keeps sec-10.3.
lode doc link WL-PLAN-41 --covers WL-SPEC-80#sec-10.3b
```

Findings outside this pass:

- WL-PLAN-68 covers spec 008 as a whole document, so it claims every WL-SPEC-80 rule derived from it, including telemetry and the status line, which it does not build.
- `lode rule list --doc <spec> --json` returns empty `covered_by` and `governed_tasks`; `lode rule show <ref> --json` returns them.

## WL-SPEC-81 CLI and skills

Rules: 26 before, 42 after accept (16 new sections, 0 merged). Revision: opened on doc 564, body written, not accepted. Just over the forty-rule target; the vendored design skills (sec-8 to sec-8.5b, 13 rules) are the second subject if the owner splits it.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-305 | sec-1 | sec-1 The naming law; sec-1a `set` writes a fact |
| WL-REQ-307 | sec-3 | sec-3 Completions; sec-3a Task id ordering |
| WL-REQ-308 | sec-4 | sec-4 Enforcement: `namerule_test.go`; sec-4a Agent-surface tests and renames |
| WL-REQ-312 | sec-6.2 | sec-6.2 The resolve route; sec-6.2a The project filter on list routes |
| WL-REQ-313 | sec-6.3 | sec-6.3 Client cache; sec-6.3a `lode project resolve` |
| WL-REQ-318 | sec-7.1 | sec-7.1 Skill sources and git sync; sec-7.1a Skill storage schema |
| WL-REQ-320 | sec-7.3 | sec-7.3 Task pins; sec-7.3a The local skill store; sec-7.3b Skills in the task brief; sec-7.3c Skill delivery degradation |
| WL-REQ-321 | sec-7.4 | sec-7.4 Plugin-qualified skill identity; sec-7.4a Resolving a bare skill name |
| WL-REQ-323 | sec-8.1 | sec-8.1 The `upstream` provenance block; sec-8.1a `UPSTREAM.md` transformation prompts; sec-8.1b The vendored-skill drift check |
| WL-REQ-324 | sec-8.2 | sec-8.2 The skill set; sec-8.2a What every remix carries |
| WL-REQ-326 | sec-8.4 | sec-8.4 Grilling sources and destination; sec-8.4a Grilling rounds in crit |
| WL-REQ-327 | sec-8.5 | sec-8.5 `lode doc fetch`; sec-8.5a Conditional refetch; sec-8.5b Degradation of vendored skills |

No letter was taken; WL-SPEC-81 had no letter-suffixed anchors. The entity-role paragraph of WL-REQ-305 (`graph`, `task`, `project health`) is rationale for the law and stays in sec-1, moved ahead of sec-1a. The `LICENSES.md` sentence of WL-REQ-323 is provenance and moved from the drift-check paragraph into sec-8.1. sec-8.5b is the row's "misfiled" group: it is about suppression (WL-REQ-325) and grilling (WL-REQ-326), not `lode doc fetch`; it is split out as written and left for the owner to move. Positional refs in the rewritten bodies now cite WL-REQ-308, WL-REQ-311, WL-REQ-319, WL-REQ-321, WL-REQ-327, `WL-SPEC-75#sec-3`, `WL-SPEC-75#sec-8`, `WL-SPEC-77#sec-9`, `WL-SPEC-77#sec-11`, `WL-SPEC-79#sec-14` and `WL-SPEC-80#sec-7.1`. C1's "(§5)" pointed at "cmd decides, cli renders"; the resolution chain is WL-REQ-311 (sec-6.1), which it now cites.

### Kept

None. Every row's groups held on reading.

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-81 sec-1a)"   --derived-from WL-REQ-305
lode rule link "$(rr WL-SPEC-81 sec-3a)"   --derived-from WL-REQ-307
lode rule link "$(rr WL-SPEC-81 sec-4a)"   --derived-from WL-REQ-308
lode rule link "$(rr WL-SPEC-81 sec-6.2a)" --derived-from WL-REQ-312
lode rule link "$(rr WL-SPEC-81 sec-6.3a)" --derived-from WL-REQ-313
lode rule link "$(rr WL-SPEC-81 sec-7.1a)" --derived-from WL-REQ-318
lode rule link "$(rr WL-SPEC-81 sec-7.3a)" --derived-from WL-REQ-320
lode rule link "$(rr WL-SPEC-81 sec-7.3b)" --derived-from WL-REQ-320
lode rule link "$(rr WL-SPEC-81 sec-7.3c)" --derived-from WL-REQ-320
lode rule set  "$(rr WL-SPEC-81 sec-7.3c)" --kind catalogue
lode rule link "$(rr WL-SPEC-81 sec-7.4a)" --derived-from WL-REQ-321
lode rule link "$(rr WL-SPEC-81 sec-8.1a)" --derived-from WL-REQ-323
lode rule link "$(rr WL-SPEC-81 sec-8.1b)" --derived-from WL-REQ-323
lode rule link "$(rr WL-SPEC-81 sec-8.2a)" --derived-from WL-REQ-324
lode rule link "$(rr WL-SPEC-81 sec-8.4a)" --derived-from WL-REQ-326
lode rule link "$(rr WL-SPEC-81 sec-8.5a)" --derived-from WL-REQ-327
lode rule link "$(rr WL-SPEC-81 sec-8.5b)" --derived-from WL-REQ-327
```

### Re-pointed edges (after accept)

No task is governed by a split rule of WL-SPEC-81. Superseded plans (WL-PLAN-21, 22, 27, 28, 38) are skipped. Every covering plan reaches these rules through supersession from WL-SPEC-16, 19, 37 and 61 rules, so there is no `covers` edge on a WL-SPEC-81 anchor to unlink: the commands only add. WL-PLAN-68 (branch and worktree naming) and WL-PLAN-129 (`work`, `graph`, `skill`, `secret` renames) build only the law: unchanged.

```bash
# WL-PLAN-127 (accepted) lands the law and adds `task set state` in place of `task done`.
lode doc link WL-PLAN-127 --covers WL-SPEC-81#sec-1a
# WL-PLAN-128 (accepted) turns the `--set` flags into `doc set reviewers` and `project set <field>`, the general field-write form.
lode doc link WL-PLAN-128 --covers WL-SPEC-81#sec-1a
# WL-PLAN-130 (accepted) moves the id comparator and pins the SQL order (tasks 1-2), and its close-out runs the rename procedure: catalog, Codex mirror, onboarding stamp (task 10).
lode doc link WL-PLAN-130 --covers WL-SPEC-81#sec-3a
lode doc link WL-PLAN-130 --covers WL-SPEC-81#sec-4a
# WL-PLAN-106 (stale) is the retroactive backfill: it claims each covered section fully built, so it covers every group of those rules.
lode doc link WL-PLAN-106 --covers WL-SPEC-81#sec-1a
lode doc link WL-PLAN-106 --covers WL-SPEC-81#sec-6.2a
lode doc link WL-PLAN-106 --covers WL-SPEC-81#sec-6.3a
lode doc link WL-PLAN-106 --covers WL-SPEC-81#sec-7.1a
lode doc link WL-PLAN-106 --covers WL-SPEC-81#sec-7.3a
lode doc link WL-PLAN-106 --covers WL-SPEC-81#sec-7.3b
lode doc link WL-PLAN-106 --covers WL-SPEC-81#sec-7.3c
lode doc link WL-PLAN-106 --covers WL-SPEC-81#sec-7.4a
# WL-PLAN-91 (draft) writes the remix research documents and the UPSTREAM prompts, including the destination contract and crit rounds for assay. It writes no frontmatter block and no drift check, but its WL-REQ-323 coverage is inherited from WL-REQ-795 and cannot be unlinked here.
lode doc link WL-PLAN-91 --covers WL-SPEC-81#sec-8.1a
lode doc link WL-PLAN-91 --covers WL-SPEC-81#sec-8.2a
lode doc link WL-PLAN-91 --covers WL-SPEC-81#sec-8.4a
# WL-PLAN-119 (draft) builds the vendored tree: frontmatter, UPSTREAM files, the drift check, LICENSES.md, the seven skills with crit rounds folded into assay.
lode doc link WL-PLAN-119 --covers WL-SPEC-81#sec-8.1a
lode doc link WL-PLAN-119 --covers WL-SPEC-81#sec-8.1b
lode doc link WL-PLAN-119 --covers WL-SPEC-81#sec-8.2a
lode doc link WL-PLAN-119 --covers WL-SPEC-81#sec-8.4a
# WL-PLAN-120 (draft) builds `lode doc fetch` (task 1) and the conditional refetch with its metric (task 2). It does not cover the old Degradation rule WL-REQ-820, so not sec-8.5b.
lode doc link WL-PLAN-120 --covers WL-SPEC-81#sec-8.5a
```

sec-8.5b has no covering plan after accept.

### Findings outside the pass

- Unsplit rules still carry positional refs: WL-REQ-306 (`§7.5`, `WL-SPEC-77 §19.4`), WL-REQ-314/315 (`§6.4`), WL-RULE-317 (`§7.3`, which now lands on task pins, not the local store in sec-7.3a), WL-RULE-322 (`§8`), and the not-built table (`§8.1` to `§8.5`).
- `lode rule list --doc <ref> --json` returns empty `covered_by` and `governed_tasks` for every rule; `lode rule show <ref> --json` returns them.
- Coverage inherited through supersession cannot be narrowed with `lode doc unlink --covers <new anchor>`; narrowing WL-PLAN-91's claim on WL-REQ-323 means unlinking its WL-SPEC-37 entry (`sec-2.2`), which also carries the prompts group.
