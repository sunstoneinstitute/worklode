# Corpus pass: splits in WL-SPEC-72 to 76

WL-1035, WL-PLAN-149 Task 4. Applies tests 3 and 4 of WL-SPEC-77 sec-4c to the rules arranged in WL-SPEC-72 to 76, from the rows in `v2result0.jsonl` to `v2result3.jsonl`. A `too_big` rule becomes one section per decision group: the original anchor keeps the first group and each new group gets a letter-suffixed depth-3 anchor (WL-SPEC-77 sec-4). A `too_small` rule's text moves into its parent; its anchor stays, with a one-line body, because an accepted anchor is never removed. Rules that WL-1034 made `principle` with verdict `template` are not split.

Each spec has an open candidate revision with the new body. None is accepted: the owner reviews and runs `lode doc revise <spec> --accept`. A revision mints its new rules only when it lands, so lineage, kind changes, merges and re-pointed `covers` and `governedBy` edges are listed below as commands to run after each accept. Superseded plans and abandoned tasks are not re-pointed.

`rr` resolves a spec anchor to the rule arranged there:

```bash
rr() { lode rule list --doc "$1" --json | jq -r --arg d "$1" --arg a "$2" \
  '.[] | select(any(.arranged_in[]; .doc_ref == $d and .anchor == $a)) | .ref'; }
```

## Summary

| Spec | Before | After accept | Rules split | Kept | `too_small` merged | Revision | Post-accept commands |
|---|---|---|---|---|---|---|---|
| WL-SPEC-72 | 12 | 17 | 2 | 0 | 0 | opened | 8 |
| WL-SPEC-73 | 36 | 50 | 11 | 0 | 1 | opened | 33 |
| WL-SPEC-74 | 34 | 49 | 10 | 0 | 0 | opened | 45 |
| WL-SPEC-75 | 54 | 83 | 17 | 0 | 2 | opened | 94 |
| WL-SPEC-76 | 33 | 47 | 8 | 1 | 0 | opened | 29 |
| Total | 169 | 246 | 48 | 1 | 3 | 5 of 5 | 209 |

"After accept" counts the new sections and subtracts the merged rules, which leave the count when `lode rule supersede` withdraws them. No revision was refused for an already-open candidate.

Specs over about forty rules after the pass, listed as split candidates for the owner and not split here: WL-SPEC-73 (50), WL-SPEC-74 (49), WL-SPEC-75 (83), WL-SPEC-76 (47).

## WL-SPEC-72 The design authority gate

Rules: 12 before, 17 after accept (5 new sections, 0 merged). Revision: opened on doc 555, body written, not accepted.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-6 | sec-3 | sec-3 Guarded paths: the `[gate]` table; sec-3a Worklode's guarded paths; sec-3b `lode gate check`; sec-3c The gate report in `lode doctor` |
| WL-REQ-7 | sec-4 | sec-4 Declaration: the `Spec:` trailer; sec-4a The `Worklode-Task:` trailer; sec-4b The governing link the reconciler writes |

The plan paragraph of WL-REQ-7, which restated condition 1 of WL-REQ-8, now cites WL-REQ-8 and stays in sec-4.

### Kept

None.

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-72 sec-3a)" --derived-from WL-REQ-6
lode rule set  "$(rr WL-SPEC-72 sec-3a)" --kind catalogue
lode rule link "$(rr WL-SPEC-72 sec-3b)" --derived-from WL-REQ-6
lode rule link "$(rr WL-SPEC-72 sec-3c)" --derived-from WL-REQ-6
lode rule link "$(rr WL-SPEC-72 sec-4a)" --derived-from WL-REQ-7
lode rule link "$(rr WL-SPEC-72 sec-4b)" --derived-from WL-REQ-7
```

### Re-pointed edges (after accept)

No plan covers WL-REQ-6 or WL-REQ-7.

```bash
# WL-965 adds Worklode's own [gate] table: the guarded-path catalogue, not the trailer.
lode task govern   WL-965 --by "$(rr WL-SPEC-72 sec-3a)"
lode task ungovern WL-965 --by WL-REQ-7
```

## WL-SPEC-73 System and deployment

Rules: 36 before, 50 after accept (15 new sections, 1 merged). Revision: opened on doc 556, body written, not accepted. Over the forty-rule target: split candidate for the owner.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-16 | sec-3.2 | sec-3.2 Import boundaries of the executables; sec-3.2b Where loops and commands live (sec-3.2a is taken by WL-RULE-1349) |
| WL-REQ-19 | sec-3.5 | sec-3.5 Failure behaviour of the executables; sec-3.5a Startup time and size are review evidence |
| WL-REQ-21 | sec-4.1 | sec-4.1 Where each instance runs; sec-4.1a No admin-cluster privilege for Worklode |
| WL-REQ-22 | sec-4.2 | sec-4.2 The cluster-to-environment map; sec-4.2a The instance kind: `LODE_INSTANCE_ENV` |
| WL-REQ-23 | sec-4.3 | sec-4.3 The prod overlay; sec-4.3a `LODE_WEB_OPEN` is never set on a reachable instance |
| WL-REQ-30 | sec-5.5 | sec-5.5 The sandbox session's entry point; sec-5.5a Sandbox hooks are installed at image build; sec-5.5b The sandbox token; sec-5.5c Secrets in a sandbox; sec-5.5d The human-question relay |
| WL-REQ-31 | sec-5.6 | sec-5.6 Seams for backbone-initiated dispatch; sec-5.6a The dispatch executor |
| WL-REQ-32 | sec-6 | sec-6 Metrics conventions; sec-6a Store metrics registration; sec-6b The store, skill-sync, webhook and embedding metric families |
| WL-REQ-34 | sec-7.1 | sec-7.1 Harness exports; sec-7.1a The Edge Agent store and replacement totals |
| WL-REQ-40 | sec-8.2 | sec-8.2 Replacing a session's usage in the store; sec-8.2a The session-usage route |
| WL-REQ-1289 | sec-10 | sec-10 Storing timestamps in UTC; sec-10a Displaying timestamps |

The two `deps_test.go` sentences of WL-REQ-16 are joined in sec-3.2. Doc-level pointers to WL-SPEC-74 in the rewritten bodies now cite WL-REQ-48, WL-REQ-49, WL-REQ-63 and WL-REQ-67.

### Kept

None.

### too_small merged

| Rule | Into | Change |
|---|---|---|
| WL-REQ-42 (sec-8.4 Cockpit) | WL-REQ-41 (sec-8.3) | The cockpit sentence moved to the end of sec-8.3. sec-8.4 keeps its anchor and heading with the body "Merged into WL-REQ-41." |

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-73 sec-3.2b)" --derived-from WL-REQ-16
lode rule link "$(rr WL-SPEC-73 sec-3.5a)" --derived-from WL-REQ-19
lode rule link "$(rr WL-SPEC-73 sec-4.1a)" --derived-from WL-REQ-21
lode rule link "$(rr WL-SPEC-73 sec-4.2a)" --derived-from WL-REQ-22
lode rule link "$(rr WL-SPEC-73 sec-4.3a)" --derived-from WL-REQ-23
lode rule link "$(rr WL-SPEC-73 sec-5.5a)" --derived-from WL-REQ-30
lode rule link "$(rr WL-SPEC-73 sec-5.5b)" --derived-from WL-REQ-30
lode rule link "$(rr WL-SPEC-73 sec-5.5c)" --derived-from WL-REQ-30
lode rule link "$(rr WL-SPEC-73 sec-5.5d)" --derived-from WL-REQ-30
lode rule link "$(rr WL-SPEC-73 sec-5.6a)" --derived-from WL-REQ-31
lode rule link "$(rr WL-SPEC-73 sec-6a)"   --derived-from WL-REQ-32
lode rule link "$(rr WL-SPEC-73 sec-6b)"   --derived-from WL-REQ-32
lode rule set  "$(rr WL-SPEC-73 sec-6b)"   --kind catalogue
lode rule link "$(rr WL-SPEC-73 sec-7.1a)" --derived-from WL-REQ-34
lode rule link "$(rr WL-SPEC-73 sec-8.2a)" --derived-from WL-REQ-40
lode rule link "$(rr WL-SPEC-73 sec-10a)"  --derived-from WL-REQ-1289
echo 'WL-REQ-42 -> WL-REQ-41' | lode rule supersede --map -
```

### Re-pointed edges (after accept)

No task is governed by a split or merged rule of WL-SPEC-73. Superseded plans are skipped.

```bash
# WL-PLAN-7 (accepted) builds the six executables: import boundaries, loops in lode-server, failure behaviour, and records startup and size.
lode doc link WL-PLAN-7 --covers WL-SPEC-73#sec-3.2b
lode doc link WL-PLAN-7 --covers WL-SPEC-73#sec-3.5a
# WL-PLAN-106 (stale) is the retroactive backfill: it claims each covered section fully built, so it covers every group of those rules.
lode doc link WL-PLAN-106 --covers WL-SPEC-73#sec-3.2b
lode doc link WL-PLAN-106 --covers WL-SPEC-73#sec-3.5a
lode doc link WL-PLAN-106 --covers WL-SPEC-73#sec-5.5a
lode doc link WL-PLAN-106 --covers WL-SPEC-73#sec-5.5b
lode doc link WL-PLAN-106 --covers WL-SPEC-73#sec-5.5c
lode doc link WL-PLAN-106 --covers WL-SPEC-73#sec-5.5d
lode doc link WL-PLAN-106 --covers WL-SPEC-73#sec-6a
lode doc link WL-PLAN-106 --covers WL-SPEC-73#sec-6b
lode doc link WL-PLAN-106 --covers WL-SPEC-73#sec-8.2a
# WL-PLAN-106 already covers WL-REQ-41, the merge target of WL-REQ-42: nothing to add.
# WL-PLAN-4 (draft) builds the admin overlay with CI checks for LODE_INSTANCE_ENV and the absence of LODE_WEB_OPEN. It leaves the GitHub App ceiling alone, so it does not cover sec-4.1a.
lode doc link WL-PLAN-4 --covers WL-SPEC-73#sec-4.2a
lode doc link WL-PLAN-4 --covers WL-SPEC-73#sec-4.3a
# WL-PLAN-1 (draft) builds hooks at image build and the LODE_SERVER/LODE_TOKEN-only session. By its own coverage notes it builds nothing for the entry point, and it builds no secrets or relay work.
lode doc link   WL-PLAN-1 --covers WL-SPEC-73#sec-5.5a
lode doc link   WL-PLAN-1 --covers WL-SPEC-73#sec-5.5b
lode doc unlink WL-PLAN-1 --covers WL-SPEC-73#sec-5.5
# WL-PLAN-131 (accepted) configures the harness exports. The Edge Agent store is EA-82's work, which the plan says it does not duplicate: it keeps WL-REQ-34 and gains nothing.
```

## WL-SPEC-74 Identity, actors and secrets

Rules: 34 before, 49 after accept (15 new sections, 0 merged). Revision: opened on doc 557, body written, not accepted. Over the target of about forty: a split candidate for the owner (the secrets part, sec-10 to sec-10.8, is a second subject).

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-47 | sec-1.2 | sec-1.2 When to mint an agent actor; sec-1.2a The agent actor roster; sec-1.2b Attribution under a human's token |
| WL-REQ-48 | sec-2 | sec-2 Credentials: `wl_` tokens; sec-2a Task-scoped token limits and revocation |
| WL-REQ-49 | sec-3 | sec-3 Authorization: grants and `Decide`; sec-3a The route guard table and open routes |
| WL-REQ-54 | sec-5 | sec-5 Web sessions: login gating and routes; sec-5a The session cookie |
| WL-REQ-63 | sec-9.1 | sec-9.1 The GitHub App settings; sec-9.1a Bounds on a compromised server; sec-9.1b GitHub App configuration variables |
| WL-REQ-64 | sec-9.2 | sec-9.2 Link flow at login; sec-9.2a Link state in `lode actor show` |
| WL-REQ-65 | sec-9.3 | sec-9.3 The `github_user_tokens` table; sec-9.3a Refreshing a stored GitHub token |
| WL-REQ-69 | sec-10.2 | sec-10.2 The catalog item and its projection; sec-10.2a `catalog.toml` entry format; sec-10.2b Template syntax and rendering; sec-10.2c Serving and validating the catalog |
| WL-REQ-71 | sec-10.4 | sec-10.4 Claim-time ceremony; sec-10.4a Keystore and manifest layout |
| WL-REQ-72 | sec-10.5 | sec-10.5 `lode secret exec` and the environment scrub; sec-10.5a Rendering a templated entry at exec; sec-10.5b The `lode secret` commands |

The `lode login` row of the WL-REQ-64 command table was a surface line only; it is now one sentence in sec-9.2. The re-materialization paragraph of WL-REQ-71 moved ahead of the keystore and manifest text so it stays with the ceremony. Positional refs in every rewritten body now cite WL-REQ-48, WL-REQ-49, WL-REQ-53, WL-REQ-54, WL-REQ-55, WL-REQ-56, WL-REQ-1292, WL-REQ-334 or a `WL-SPEC-74#sec-…` anchor.

### Kept

None.

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-74 sec-1.2a)" --derived-from WL-REQ-47
lode rule set  "$(rr WL-SPEC-74 sec-1.2a)" --kind catalogue
lode rule link "$(rr WL-SPEC-74 sec-1.2b)" --derived-from WL-REQ-47
lode rule link "$(rr WL-SPEC-74 sec-2a)" --derived-from WL-REQ-48
lode rule link "$(rr WL-SPEC-74 sec-3a)" --derived-from WL-REQ-49
lode rule link "$(rr WL-SPEC-74 sec-5a)" --derived-from WL-REQ-54
lode rule link "$(rr WL-SPEC-74 sec-9.1a)" --derived-from WL-REQ-63
lode rule link "$(rr WL-SPEC-74 sec-9.1b)" --derived-from WL-REQ-63
lode rule set  "$(rr WL-SPEC-74 sec-9.1b)" --kind catalogue
lode rule link "$(rr WL-SPEC-74 sec-9.2a)" --derived-from WL-REQ-64
lode rule link "$(rr WL-SPEC-74 sec-9.3a)" --derived-from WL-REQ-65
lode rule link "$(rr WL-SPEC-74 sec-10.2a)" --derived-from WL-REQ-69
lode rule link "$(rr WL-SPEC-74 sec-10.2b)" --derived-from WL-REQ-69
lode rule link "$(rr WL-SPEC-74 sec-10.2c)" --derived-from WL-REQ-69
lode rule link "$(rr WL-SPEC-74 sec-10.4a)" --derived-from WL-REQ-71
lode rule link "$(rr WL-SPEC-74 sec-10.5a)" --derived-from WL-REQ-72
lode rule link "$(rr WL-SPEC-74 sec-10.5b)" --derived-from WL-REQ-72
lode rule set  "$(rr WL-SPEC-74 sec-10.5b)" --kind catalogue
```

### Re-pointed edges (after accept)

No task is governed by a split rule of this spec. Superseded plans are skipped. Unchanged: WL-PLAN-116 (`tokens.minted_by`, the token description in sec-2) and WL-PLAN-89 (`LODE_WEB_OPEN` gating, sec-5) build only the first group of their rule.

```bash
# WL-PLAN-106 (stale) records built sections; it covered WL-REQ-47, 48, 54, 69, 71 and 72 whole, so it covers every group.
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-1.2a
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-1.2b
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-2a
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-5a
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-10.2a
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-10.2b
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-10.2c
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-10.4a
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-10.5a
lode doc link WL-PLAN-106 --covers WL-SPEC-74#sec-10.5b
# WL-PLAN-126 (draft) adds a `janitor` actor: a roster entry, argued from the minting test it keeps.
lode doc link WL-PLAN-126 --covers WL-SPEC-74#sec-1.2a
# WL-PLAN-143 (draft) narrows the installation and adds no-bypass rulesets: settings plus the compromise bounds.
lode doc link WL-PLAN-143 --covers WL-SPEC-74#sec-9.1a
# WL-PLAN-52 (stale) builds the token table and store.UserToken refresh, and the web link flow; not `lode actor show`.
lode doc link WL-PLAN-52 --covers WL-SPEC-74#sec-9.3a
# WL-PLAN-53 (stale) builds `lode login` linking and `lode actor show`.
lode doc link WL-PLAN-53 --covers WL-SPEC-74#sec-9.2a
# WL-PLAN-10 (accepted) secret templates: parser, template rendering, serving, manifest, exec rendering, status.
lode doc link WL-PLAN-10 --covers WL-SPEC-74#sec-10.2a
lode doc link WL-PLAN-10 --covers WL-SPEC-74#sec-10.2b
lode doc link WL-PLAN-10 --covers WL-SPEC-74#sec-10.2c
lode doc link WL-PLAN-10 --covers WL-SPEC-74#sec-10.4a
lode doc link WL-PLAN-10 --covers WL-SPEC-74#sec-10.5a
lode doc link WL-PLAN-10 --covers WL-SPEC-74#sec-10.5b
# WL-PLAN-43 (accepted, secrets 1/3) builds the catalog format and endpoint and the event; no exec.
lode doc link   WL-PLAN-43 --covers WL-SPEC-74#sec-10.2a
lode doc link   WL-PLAN-43 --covers WL-SPEC-74#sec-10.2c
lode doc unlink WL-PLAN-43 --covers WL-SPEC-74#sec-10.5
# WL-PLAN-44 (accepted, secrets 2/3) builds keystore, manifest, env file, exec and the command tree; not the catalog item.
lode doc link   WL-PLAN-44 --covers WL-SPEC-74#sec-10.4a
lode doc link   WL-PLAN-44 --covers WL-SPEC-74#sec-10.5b
lode doc unlink WL-PLAN-44 --covers WL-SPEC-74#sec-10.2
# WL-PLAN-45 (accepted, secrets 3/3) builds the ceremony, purge on release and catalog deployment; not exec.
lode doc unlink WL-PLAN-45 --covers WL-SPEC-74#sec-10.5
```

Finding outside this pass: WL-PLAN-126 adds `janitor` with no grants difference, which the minting test in sec-1.2 refuses.

## WL-SPEC-75 Tasks and execution

Rules: 54 before, 83 after accept and supersede (31 new sections, 2 merged). Revision: opened on doc 558, body written, not accepted. At 83 rules the spec is well over the forty-rule target: a split candidate for the owner.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-77 | sec-1 | sec-1 Project keys and task ids; sec-1a Branch and worktree names; sec-1b The `commit-msg` trailer hook; sec-1c Research project attributes |
| WL-REQ-84 | sec-3.2 | sec-3.2 Transitions and the `Transition` guard; sec-3.2a `lode task set state`; sec-3.2b Reopen clears landed commits; sec-3.2c Delivery leaves the lease open |
| WL-REQ-87 | sec-4 | sec-4 Edge types and the `task_edges` table; sec-4a Edge API and CLI; sec-4b Governing-rule links; sec-4c Governing-link API and CLI |
| WL-REQ-107 | sec-9.4 | sec-9.4 Subscriber offsets; sec-9.4a One consumer per stream |
| WL-REQ-109 | sec-9.6 | sec-9.6 The `doc-lifecycle` subscriber; sec-9.6b Planning cost attribution (placed after sec-9.6a, the next free letter) |
| WL-REQ-113 | sec-10.1 | sec-10.1 Fact tables; sec-10.1a Deploy and release frontiers; sec-10.1b Environment names |
| WL-REQ-114 | sec-10.2 | sec-10.2 Webhook handlers; sec-10.2a The delivery resolver; sec-10.2b GitHub App permissions and the Flux alert |
| WL-REQ-116 | sec-11 | sec-11 Decision tasks; sec-11a Decision rows |
| WL-REQ-117 | sec-12 | sec-12 Deleting tasks and documents: the tombstone; sec-12a What moves with a delete; sec-12b Delete justification by instance environment; sec-12c What a tombstone hides |
| WL-REQ-119 | sec-13.1 | sec-13.1 The investigation is a project; sec-13.1a The Sunstone stage |
| WL-REQ-120 | sec-13.2 | sec-13.2 Milestones and deliverables; sec-13.2a The default story template |
| WL-REQ-122 | sec-13.4 | sec-13.4 Identifiers per entity kind; sec-13.4a Cross-project references |
| WL-REQ-123 | sec-13.5 | sec-13.5 Assignee and contributors; sec-13.5a The Crew: roles, lead and deputy |
| WL-REQ-124 | sec-13.6 | sec-13.6 Approvals table and states; sec-13.6a Self-approval; sec-13.6b Approval flows; sec-13.6c Approval gates |
| WL-REQ-125 | sec-13.7 | sec-13.7 Idea capture at intake; sec-13.7a The dossier and editorial decisions; sec-13.7b Promotion |
| WL-REQ-126 | sec-13.8 | sec-13.8 Outbound consequences and crew spaces; sec-13.8a Inbound ingest sources |
| WL-REQ-127 | sec-14 | sec-14 Postgres driver, pool and migrations; sec-14a Isolation and replica safety; sec-14b Typed store errors |

Notes on the rewrite:

- WL-REQ-116 cited section 13.7 for approvals. It now cites WL-REQ-124, the approvals rule. Its closing sentence on approvals moved from the rows to the decision task, sec-11.
- WL-REQ-117's trailing surface paragraph (routes, CLI, metric) stays in sec-12 with the tombstone columns.
- The Keycloak identity paragraph of WL-REQ-123 went to sec-13.5a, the Crew, since it explains why approving and crew gates read stored groups.
- The "designed, not built" Google Chat sentence of WL-REQ-126 stays in sec-13.8 as it was. It is template text and already listed under sec-not-built.
- Every positional reference inside the rewritten rules is now a rule ref. Rules outside the split were not touched and still carry section-number references.

### Kept

None. Every row's groups held on reading.

### too_small merged

| Rule | Parent | Change in the revision |
|---|---|---|
| WL-REQ-91 Roll-up (sec-5.3) | WL-REQ-90 (sec-5.2) | the `ResolveHierarchy` paragraph moved into sec-5.2; sec-5.3 keeps its heading and anchor with the body "Merged into WL-REQ-90." |
| WL-REQ-93 API and brief (sec-5.5) | WL-REQ-88 (sec-5) | the surface paragraph moved into sec-5 as its `Surface:` line; sec-5.5 keeps its heading and anchor with the body "Merged into WL-REQ-88." |

Neither merged rule has a covering plan or a governed task.

### Lineage, kinds and merges (after accept)

```bash
for a in sec-1a sec-1b sec-1c; do lode rule link "$(rr WL-SPEC-75 $a)" --derived-from WL-REQ-77; done
for a in sec-3.2a sec-3.2b sec-3.2c; do lode rule link "$(rr WL-SPEC-75 $a)" --derived-from WL-REQ-84; done
for a in sec-4a sec-4b sec-4c; do lode rule link "$(rr WL-SPEC-75 $a)" --derived-from WL-REQ-87; done
lode rule set "$(rr WL-SPEC-75 sec-4a)" --kind catalogue
lode rule set "$(rr WL-SPEC-75 sec-4c)" --kind catalogue
lode rule link "$(rr WL-SPEC-75 sec-9.4a)" --derived-from WL-REQ-107
lode rule link "$(rr WL-SPEC-75 sec-9.6b)" --derived-from WL-REQ-109
for a in sec-10.1a sec-10.1b; do lode rule link "$(rr WL-SPEC-75 $a)" --derived-from WL-REQ-113; done
for a in sec-10.2a sec-10.2b; do lode rule link "$(rr WL-SPEC-75 $a)" --derived-from WL-REQ-114; done
lode rule link "$(rr WL-SPEC-75 sec-11a)" --derived-from WL-REQ-116
for a in sec-12a sec-12b sec-12c; do lode rule link "$(rr WL-SPEC-75 $a)" --derived-from WL-REQ-117; done
lode rule link "$(rr WL-SPEC-75 sec-13.1a)" --derived-from WL-REQ-119
lode rule link "$(rr WL-SPEC-75 sec-13.2a)" --derived-from WL-REQ-120
lode rule link "$(rr WL-SPEC-75 sec-13.4a)" --derived-from WL-REQ-122
lode rule link "$(rr WL-SPEC-75 sec-13.5a)" --derived-from WL-REQ-123
for a in sec-13.6a sec-13.6b sec-13.6c; do lode rule link "$(rr WL-SPEC-75 $a)" --derived-from WL-REQ-124; done
for a in sec-13.7a sec-13.7b; do lode rule link "$(rr WL-SPEC-75 $a)" --derived-from WL-REQ-125; done
lode rule link "$(rr WL-SPEC-75 sec-13.8a)" --derived-from WL-REQ-126
for a in sec-14a sec-14b; do lode rule link "$(rr WL-SPEC-75 $a)" --derived-from WL-REQ-127; done
printf 'WL-REQ-91 -> WL-REQ-90\nWL-REQ-93 -> WL-REQ-88\n' | lode rule supersede --map -
```

### Re-pointed edges (after accept)

Plans that keep only the first group, so need no command: WL-PLAN-66 (key rules, sec-1), WL-PLAN-145 (edge table, sec-4), WL-PLAN-31, WL-PLAN-109 and WL-PLAN-110 (`doc-lifecycle` rules and offsets). Superseded plans are skipped. Tasks that keep only the first group: WL-906 to WL-920 (sec-4, plan-minted from WL-PLAN-145), WL-927 (sec-9.6), WL-573 (sec-13.7), WL-696 (sec-13.5). Abandoned tasks WL-569, WL-571 and WL-695 are skipped.

```bash
# WL-PLAN-106 is the retroactive coverage backfill: it covers every group of what it covered.
for a in sec-1a sec-1b sec-1c sec-4a sec-4b sec-4c sec-12a sec-12b sec-12c sec-14a sec-14b; do
  lode doc link WL-PLAN-106 --covers WL-SPEC-75#$a
done
# WL-PLAN-61 builds the ordered log, including the advisory-lock single consumer.
lode doc link WL-PLAN-61 --covers WL-SPEC-75#sec-9.4a
# WL-PLAN-62 builds the doc-lifecycle subscriber and bills planning cost to the design task.
lode doc link WL-PLAN-62 --covers WL-SPEC-75#sec-9.6b

# WL-1020 is the multi-repo delivery conflict: the primary-repo sentence is in the resolver.
lode task govern WL-1020 --by "$(rr WL-SPEC-75 sec-10.2a)"; lode task ungovern WL-1020 --by WL-REQ-114
# WL-570 builds the derived stage and carryover, not decision tasks or the project mapping.
lode task govern WL-570 --by "$(rr WL-SPEC-75 sec-13.1a)"; lode task ungovern WL-570 --by WL-REQ-116; lode task ungovern WL-570 --by WL-REQ-119
# WL-702 closes a project's Crew: stage lifecycle plus the Crew.
lode task govern WL-702 --by "$(rr WL-SPEC-75 sec-13.1a)"; lode task govern WL-702 --by "$(rr WL-SPEC-75 sec-13.5a)"
lode task ungovern WL-702 --by WL-REQ-119; lode task ungovern WL-702 --by WL-REQ-123
# WL-578 and WL-577 are the stage transition act and the stage card.
for t in WL-578 WL-577; do lode task govern $t --by "$(rr WL-SPEC-75 sec-13.1a)"; lode task ungovern $t --by WL-REQ-119; done
# WL-572 reads lifecycle facts (stage) and the dossier; it keeps WL-REQ-125 for intake candidates.
lode task govern WL-572 --by "$(rr WL-SPEC-75 sec-13.1a)"; lode task govern WL-572 --by "$(rr WL-SPEC-75 sec-13.7a)"; lode task ungovern WL-572 --by WL-REQ-119
# WL-1021 is the cross-project edge conflict: the WL-REQ-122 side is the cross-project references group.
lode task govern WL-1021 --by "$(rr WL-SPEC-75 sec-13.4a)"; lode task ungovern WL-1021 --by WL-REQ-122
# Crew work: identity, invitations, lead handoff, removal guard, e2e.
for t in WL-925 WL-704 WL-701 WL-700 WL-699 WL-698 WL-697 WL-694; do
  lode task govern $t --by "$(rr WL-SPEC-75 sec-13.5a)"; lode task ungovern $t --by WL-REQ-123
done
# WL-703 covers contributors (stays on WL-REQ-123) and crew history.
lode task govern WL-703 --by "$(rr WL-SPEC-75 sec-13.5a)"
# WL-567 and WL-563 read the approvals table and its gate read: keep WL-REQ-124, add the gates.
for t in WL-567 WL-563; do lode task govern $t --by "$(rr WL-SPEC-75 sec-13.6c)"; done
# WL-565 is the CI gate in promote-prod.yml.
lode task govern WL-565 --by "$(rr WL-SPEC-75 sec-13.6c)"; lode task ungovern WL-565 --by WL-REQ-124
# WL-564 is the designation act for a newer evidence revision, part of the flows group.
lode task govern WL-564 --by "$(rr WL-SPEC-75 sec-13.6b)"; lode task ungovern WL-564 --by WL-REQ-124
# WL-579 is the pitch-to-launch e2e: capture, dossier and promotion.
lode task govern WL-579 --by "$(rr WL-SPEC-75 sec-13.7a)"; lode task govern WL-579 --by "$(rr WL-SPEC-75 sec-13.7b)"
# WL-576 is the promotion transaction; WL-575 the gate decisions on the dossier.
lode task govern WL-576 --by "$(rr WL-SPEC-75 sec-13.7b)"; lode task ungovern WL-576 --by WL-REQ-125
lode task govern WL-575 --by "$(rr WL-SPEC-75 sec-13.7a)"; lode task ungovern WL-575 --by WL-REQ-125
```

## WL-SPEC-76 Done, verification and workflows

Rules: 33 before, 47 after accept (14 new sections, 0 merged). Revision: opened on doc 559, body written, not accepted.

### Splits

| Rule | Anchor | Sections after the split |
|---|---|---|
| WL-REQ-133 | sec-1.3 | sec-1.3 Storage of done declarations; sec-1.3a API fields for done declarations; sec-1.3b CLI flags for done declarations |
| WL-REQ-150 | sec-8.9 | sec-8.9 State readers; sec-8.9a Hierarchy ignores workflows |
| WL-REQ-151 | sec-8.10 | sec-8.10 Workflow API and CLI; sec-8.10a Review on workflow change; sec-8.10b LLM-authored workflows |
| WL-REQ-154 | sec-10.1 | sec-10.1 The `wl:TaskTransitioned` event; sec-10.1a Evaluation order and the self-loop guard |
| WL-REQ-155 | sec-10.2 | sec-10.2 Automation shape; sec-10.2a Automation write-time validation |
| WL-REQ-156 | sec-10.3 | sec-10.3 Evaluation pass and firing; sec-10.3a Redelivery |
| WL-REQ-157 | sec-10.4 | sec-10.4 The model request frame; sec-10.4a The model's answer space; sec-10.4b Model failures and containment |
| WL-REQ-158 | sec-10.5 | sec-10.5 Evaluator and executor; sec-10.5a The `internal/llm` client; sec-10.5b The `wl:AutomationFired` event |

The `project.workflows_set` event stays with the PUT in sec-8.10, and the "not admin-gated" rationale moves to sec-8.10a, the review it relies on. The sentence tying automations to the workflow surfaces stays in sec-10.5 and cites WL-REQ-151.

### Kept

| Rule | Reason |
|---|---|
| WL-REQ-145 | The second group, the workflow name format, is one sentence that restates a fact WL-REQ-149 owns ("Names match `[a-z0-9-]{1,40}`"). It is a duplicate to remove from WL-REQ-145, not a subject for a new rule. Left unchanged here; the restatement is a finding for the owner. |

### too_small merged

None.

### Lineage and kinds (after accept)

```bash
lode rule link "$(rr WL-SPEC-76 sec-1.3a)"  --derived-from WL-REQ-133
lode rule set  "$(rr WL-SPEC-76 sec-1.3a)"  --kind catalogue
lode rule link "$(rr WL-SPEC-76 sec-1.3b)"  --derived-from WL-REQ-133
lode rule set  "$(rr WL-SPEC-76 sec-1.3b)"  --kind catalogue
lode rule link "$(rr WL-SPEC-76 sec-8.9a)"  --derived-from WL-REQ-150
lode rule link "$(rr WL-SPEC-76 sec-8.10a)" --derived-from WL-REQ-151
lode rule link "$(rr WL-SPEC-76 sec-8.10b)" --derived-from WL-REQ-151
lode rule link "$(rr WL-SPEC-76 sec-10.1a)" --derived-from WL-REQ-154
lode rule link "$(rr WL-SPEC-76 sec-10.2a)" --derived-from WL-REQ-155
lode rule link "$(rr WL-SPEC-76 sec-10.3a)" --derived-from WL-REQ-156
lode rule link "$(rr WL-SPEC-76 sec-10.4a)" --derived-from WL-REQ-157
lode rule link "$(rr WL-SPEC-76 sec-10.4b)" --derived-from WL-REQ-157
lode rule link "$(rr WL-SPEC-76 sec-10.5a)" --derived-from WL-REQ-158
lode rule link "$(rr WL-SPEC-76 sec-10.5b)" --derived-from WL-REQ-158
```

### Re-pointed edges (after accept)

No task is governed by a split rule of this spec. Every covering plan builds all groups of the rules it covers, so each keeps its edge to the original rule and gains the new groups; nothing is unlinked.

```bash
# WL-PLAN-121 (draft) Tasks 1 and 3 build the columns, the API fields and the CLI flags.
lode doc link WL-PLAN-121 --covers WL-SPEC-76#sec-1.3a
lode doc link WL-PLAN-121 --covers WL-SPEC-76#sec-1.3b
# WL-PLAN-8 (draft) Task 2 keeps hierarchy on core rules, Task 5 builds the review watcher, Task 1 fixes the core and entry table the containment rests on.
lode doc link WL-PLAN-8 --covers WL-SPEC-76#sec-8.9a
lode doc link WL-PLAN-8 --covers WL-SPEC-76#sec-8.10a
lode doc link WL-PLAN-8 --covers WL-SPEC-76#sec-8.10b
# WL-PLAN-106 (stale) records the whole state-reader section as built, containerForbiddenStates included.
lode doc link WL-PLAN-106 --covers WL-SPEC-76#sec-8.9a
# WL-PLAN-11 (draft) builds the whole engine: events (Task 2), evaluator (3), internal/llm (4), executor with dedup and redelivery (5), validation (1).
lode doc link WL-PLAN-11 --covers WL-SPEC-76#sec-10.1a
lode doc link WL-PLAN-11 --covers WL-SPEC-76#sec-10.2a
lode doc link WL-PLAN-11 --covers WL-SPEC-76#sec-10.3a
lode doc link WL-PLAN-11 --covers WL-SPEC-76#sec-10.4a
lode doc link WL-PLAN-11 --covers WL-SPEC-76#sec-10.4b
lode doc link WL-PLAN-11 --covers WL-SPEC-76#sec-10.5a
lode doc link WL-PLAN-11 --covers WL-SPEC-76#sec-10.5b
```

