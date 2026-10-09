# Rules for rules (draft v3)

## Corpus facts

- 1366 rule rows: 944 withdrawn, 422 live (371 requirement, 48 informative, 3 invariant).
- Every live rule is arranged in exactly one document. No live rule has an edge to another rule.
- 36 live rules govern 63 tasks. 288 of 329 non-abandoned feature tasks have no governing rule (WL-1011, draft, holds the follow-up).
- 14 live rules have an empty body. 146 refer to something by position (`§N`, `section N`, `sec-N`).
- Body length in words: median 136, p90 361, max 1752.

## Model

### Rules and arrangement

A **rule** is a row with a body of its own. A **document** is a template: its
body is arrangement prose and markup, with placeholders (`{{ rule 42 }}`) where
a rule's text renders. Rendering a document fills the placeholders from the
rules' current (or pinned) versions. Headings, overviews, motivation, non-goals,
deferral pointers, plan indexes and the glossary table are template text, never
rules. A document can still be read from end to end, but only the placeholders
are rows.

This replaces today's arrangement rows (`arranged_in`): the template is the
arrangement.

### Rule kinds

| Kind | What it is | How a reviewer checks it |
|---|---|---|
| `requirement` | A behaviour, shape or process a change can comply with or violate. | Directly against the change. |
| `invariant` | A property that holds in every state, with the checks that enforce it. | Against the state, not the change. |
| `catalogue` | A set of entries with one shape, where every entry answers the same question. | Membership: the change adds, removes or alters an entry. |
| `definition` | One term, one meaning, in one scope. | Whether the term is used as defined. |
| `principle` | A design stance that other rules refine. Not checkable on its own. | Through its refiners only. Replaces `informative`. |

`rationale` is not a kind. A rule's body may end with a short rationale
paragraph; it never stands alone.

**Invariant.** An invariant controls state, not changes: "every document's
status lives in one place", "one human actor per person", "a published anchor
stays put", "no agent lowers the gate on its own work". It is violated by a
state the system reaches, whatever change got it there, so it is enforced by
checks at the writes that could break it (a 409 on actor collision, a refused
revise that drops an anchor, a refused self-approval). Those checks are the
invariant's enforcement, not separate decisions, so they stay in the
invariant's body (D9). Today's corpus has 3 rows tagged invariant and 9 that
read as one: WL-REQ-1247 (status has one owner), WL-REQ-46 (one actor per
person), WL-REQ-201 (anchor permanence), WL-RULE-1321 (the honesty rule),
WL-REQ-106 (the commit horizon), WL-RULE-1306 (canonical URLs keep resolving),
WL-REQ-1289 (timestamps are UTC). WL-RULE-1344 is five invariants in one row
and splits into five.

**Catalogue, not enumeration.** An enumeration is a closed set of values (the
task states). A catalogue is a set of entries that each carry fields (a metric
with its labels and meaning, a command with its flags, an error with its
condition and message). Most of our lists are the second kind, and an
enumeration is a catalogue whose entries have one field, so one kind covers
both. "Entry" is one metric, one command, one error. "Shape" is the set of
fields every entry has. A table whose entries have different shapes, or that
answer different questions (WL-REQ-30: five concerns in one table), is not a
catalogue; it is several requirements, or one requirement with an illustrative
table.

**Definition and code.** A definition rule says what a term means. The code
shape that carries the concept (the `internal/model` struct, its fields and
wire names) is a requirement that presupposes the definition. A definition may
carry the concept IRI from `ns/concept.ttl`, which already names the model
objects, so the term, its meaning and its code shape are three linked things,
not one row. A definition's URL is `/project/<name>/term/<slug>` (also by id).

### Edges

Every edge is stored once and queryable in both directions (`owl:inverseOf`
in `ns/ontology.ttl`).

| Edge | Inverse | Meaning | In context closure |
|---|---|---|---|
| `A refines B` | `refinedBy` | A narrows B. B is a principle or a wider rule. | Yes |
| `A needs B` | `neededBy` | A cannot be applied without a fact or term B states. A does not narrow B. | Yes |
| `A references B` | `referencedBy` | B helps a reader but is not needed to apply A. | No |
| `A conflictsWith B` | symmetric | A and B state the same fact with different values. A finding, not a design. | No |
| `constrains`, `amends`, `supersedes` | as today | Unchanged. | No |

The second context edge is `needs`. Candidates considered, with the test "is
the meaning exact, and is the inverse a word":

| Name | Inverse | Exact? |
|---|---|---|
| `needs` | `neededBy` | Yes. The closure is what the rule needs. Your own phrase for it was "needed for context". |
| `assumes` | `assumedBy` | Nearly. In English "assume" carries "without proof", which is not the point; the fact is proven, it lives elsewhere. |
| `presupposes` | `presupposedBy` | Yes, and the inverse is unreadable. |
| `reliesOn` | `reliedOnBy` | Yes; inverse is awkward. |
| `uses` | `usedBy` | Too wide; a rule "uses" a command too. |
| `buildsOn` | `builtOnBy` | Reads as refines. |

Decided: `refines` and `needs`.

The **context closure** of A is A plus everything reachable over `refines`
and `needs`. An agent asking for a rule gets the closure. A document template
arranges rules without their closure; a document that wants context visible
arranges it.

### Facts and ownership

A **fact** is a value a rule states as true: a name, a default, a shape, a
state list, a behaviour. Every fact has one owner: the rule whose subject it
is. The header rule owns the header's value; the page rule that sends the
header `needs` the header rule and does not restate the value. When two rules
state one fact with different values, the owner's value stands and the pair
gets a `conflictsWith` edge until the other is fixed. The C2 pass records
these; they are findings about the spec, and the v1 run already found one
(WL-REQ-1307 `X-Requested-With: lode-web` against WL-REQ-1338 `lode-cockpit`).

### Glossary

Every project has a glossary document; the instance has one shared glossary.
Both are templates over `definition` rules, one term each. Term resolution
inside a project closure: the project's definition first, then the instance's.
A project definition that narrows an instance term `refines` it. A project
definition that reuses a term with a different meaning is allowed; the project
copy wins inside the project and the conflict is visible as two definitions of
one label with no edge between them.

## Criteria

### C1 It decides something

A rule is one of the five kinds. Template text is anything else: heading
shells, overviews, motivation, non-goals, "deferred" and "designed, not built"
pointers, plan indexes. A verification checklist that restates another rule's
behaviour as tests is template text in that rule's document, or a `references`
edge target if it has its own row. A design that is written out but not built
is still a rule if it decides something; "not built" is status, not kind. An
open question is template text. A heading whose body disagrees with it is a
finding, not a criterion. A parent that defines several terms is one
`definition` per term plus the parent's own decision, if it has one.

### C2 It is self-contained over its context closure

Reading the rule plus its closure, and nothing else, gives every fact and term
needed to apply it.

- No reference by position. Needed context is a `refines` or `needs` edge.
  Optional context is `references`.
- Every term the rule uses is a `definition` in its closure, or defined in the
  rule itself.
- A fact lives in its owner. A second rule that needs it `needs` the owner; it
  does not restate it.

### C3 It is not too big

A rule is a **decision group**: decisions that share one subject and one
check.

1. One subject. The decisions govern the same thing: one command, one
   catalogue, one state machine, one route, one invariant.
2. One check. A reviewer asks the same question of every part. A catalogue
   passes by construction; an invariant's enforcement checks are one check
   (does the state hold).

Split where the subject or the check changes. A section that covers storage,
API and CLI of one feature is three subjects and three checks: three rules,
each needing the model rule. A surface block is a subject when it states a
fact of its own (a route's body, a flag's default); a surface sentence that
only names where the rule shows is not. Two catalogues in one body are two
rules. A one-off repair procedure beside an invariant is a requirement that
`needs` the invariant.

Length is not a test. A one-subject, one-check rule over 300 words is a prompt
to look for a hidden second subject, not a verdict.

### C4 It is not too small

A rule is too small when it cannot be applied without restating its parent and
it is the parent's only refiner. Merge it into the parent.

Connectivity report: a rule with exactly one context edge whose target has
exactly one refiner. It is a report, not a verdict, until edges exist.

### C5 Arrangement does not pull context

A template arranges rules, never closures. A rendered document shows the rules
it arranges and nothing else.

## Decisions

D1 C3 is the decision-group test.
D2 Documents are templates with rule placeholders. Arrangement rows go away.
   Rule kinds: requirement, invariant, catalogue, definition, principle.
D3 Definitions are one-term rules; a project glossary and an instance glossary
   are templates over them.
D4 C4's connectivity signal is a report until edges exist.
D5 Order: C1 pass, then C3, then edges, then C4 and C5.
D6 `needs` is the context edge beside `refines`; every edge gets an
   `owl:inverseOf` property.
D7 C3 has no lifecycle test. Superseding part of a rule is a new version.
D8 Catalogue: one entry shape, every entry answers the same question.
D9 An invariant's enforcement checks are part of the invariant.
D10 One owner per fact; disagreements become `conflictsWith` edges and are
    reported as spec findings.
D11 Template text inside rule bodies (57 rules) is moved out by a subagent as
    a one-time migration after the rule pass.
D12 The C1 pass mints a `definition` rule per undefined term before edges are
    written. Term URL: `/project/<name>/term/<slug>`.
D13 Rule count is not a target. Targets: no rule over one decision group,
    p90 context closure under 2000 words, zero conflict pairs, zero undefined
    terms.
D14 C4's primary signal is restatement of the parent; the connectivity report
    is secondary.
D15 The H7 answers are in C1 and C3.
D16 The confirmed conflict pairs are filed as decision tasks now.
D17 The human unit is the document, not the rule. A person keeps track of
    fifteen specs and a glossary; a rule is what a review, a task and an agent
    address. A document template over about 40 rules is readable end to end;
    past that the spec is carrying more than one subject and should split.
    Today WL-SPEC-77 (86 after the pass) and WL-SPEC-75 (78) are over it.

## Test history

| | v0 | v1 | v2 |
|---|---|---|---|
| ok | 180 | 213 | 212 |
| too_big | 172 | 141 | 145 |
| too_small | 10 | 7 | 5 |
| template | 60 | 61 | 60 |
| split parts | 683 | 384 | 399 |
| projected rules | 863 | 597 | 611 |
| verdicts unchanged from previous | | 324 | 379 |

v0 to v1: replaced "could a task satisfy A and leave B" with the decision-group
test (F1). v1 to v2: dropped the lifecycle test, which alone produced the
sub-100-word splits (G1); sharpened catalogue (G3); invariants keep their
checks (G7).

Earlier findings still open: F5 (146 positional references, one wrong),
F6/G6 (65 rules use an undefined term, Edge Agent in 8), G5 (96 restatements,
one conflict).

## v2 test (422 live rules, four Sonnet agents)

Raw rows: `v2result0-3.jsonl`. Kinds assigned to the 362 rows that are
rules: requirement 252, catalogue 74, definition 13, principle 13, invariant
10. The other 60 rows are template text and belong to their document, not to
any rule kind.

### Per-spec breakdown

"after" = ok rules + split parts. Templates and merged rules drop out;
definitions minted under D12 are not counted.

| Spec | live | ok | too_big | too_small | template | after |
|---|---|---|---|---|---|---|
| WL-SPEC-72 | 12 | 6 | 2 | 0 | 4 | 13 |
| WL-SPEC-73 | 36 | 17 | 11 | 1 | 7 | 43 |
| WL-SPEC-74 | 34 | 20 | 11 | 0 | 3 | 47 |
| WL-SPEC-75 | 54 | 28 | 18 | 2 | 6 | 78 |
| WL-SPEC-76 | 33 | 18 | 9 | 0 | 6 | 40 |
| WL-SPEC-77 | 35 | 12 | 19 | 0 | 4 | 86 |
| WL-SPEC-78 | 53 | 34 | 12 | 1 | 6 | 67 |
| WL-SPEC-79 | 29 | 12 | 12 | 0 | 5 | 42 |
| WL-SPEC-80 | 47 | 26 | 14 | 1 | 6 | 58 |
| WL-SPEC-81 | 26 | 12 | 12 | 0 | 2 | 40 |
| WL-SPEC-82 | 22 | 7 | 10 | 0 | 5 | 34 |
| WL-SPEC-83 | 11 | 9 | 1 | 0 | 1 | 13 |
| WL-SPEC-84 | 16 | 6 | 7 | 0 | 3 | 23 |
| WL-SPEC-85 | 10 | 3 | 5 | 0 | 2 | 17 |
| WL-SPEC-86 | 4 | 2 | 2 | 0 | 0 | 10 |
| total | 422 | 212 | 145 | 5 | 60 | 611 |

Split sizes: 87 rules into 2, 29 into 3, 19 into 4, 8 into 5, WL-REQ-170 into
9, WL-REQ-165 into 13. The ok set by kind: requirement 135, catalogue 48,
principle 13, definition 9, invariant 7.

### Findings

H1 **The criteria have converged and the count has not moved.** v1 to v2
changed 43 verdicts and the projection went from 597 to 611. Dropping the
lifecycle test did not reduce too_big (141 to 145); the subject test alone
still splits 87 rules in two. The criteria are stable. The number is the
corpus's, not the criteria's: this material holds about 600 decision groups.

H2 **Count is the wrong target.** 1367 was the alarm, and 944 of those are
withdrawn history rows. The live corpus is 422 rows of median 149 words. Under
v2 edges, the median context closure is 3 rules and 500 words; the p90 closure
is 15 rules and 6900 words; the worst (anything that needs WL-REQ-165) is 42
rules and 13000 words. That p90 is what an agent pays to apply one rule. Fewer,
bigger rules make the closure the same text with less addressable structure.
Proposed D13 below.

H3 **Closure hubs are the section-sized rules.** In-degree under `needs`:
WL-REQ-165 (15), WL-RULE-1302 (9), WL-REQ-225 (9), WL-REQ-67 (8). Splitting
WL-REQ-165 into its 13 groups lets a rule need "anchor permanence" without
pulling in 1752 words of rule minting and supersession. That is where the
closure p90 drops.

H4 **16 conflict pairs.** The C2 pass found facts stated twice with different
values. Beyond the `X-Requested-With` header: `done_state` validated against
the default workflow (WL-REQ-149) or the governing one (WL-REQ-152); spec body
in `docs.body` (WL-REQ-164) or not (WL-REQ-1299); editing an unarranged rule
refused (WL-REQ-1355) or allowed (WL-REQ-1298); no-legacy-host behaviour
(WL-REQ-1303 vs WL-REQ-1304); subscriber `external_id` format (WL-REQ-105 vs
WL-REQ-110 and WL-REQ-290); multi-repo delivery (WL-REQ-86 vs WL-REQ-114);
cross-project edge types (WL-REQ-87 vs WL-REQ-122); self-approval
(WL-RULE-1321 vs WL-REQ-124); when a task enters `in_review` (WL-REQ-1324 vs
WL-REQ-1325); whether the web client produces review rounds (WL-REQ-1322 vs
WL-REQ-1323); the subject of `implements` (WL-REQ-169, WL-REQ-177 vs
WL-REQ-1288). Each is a spec bug the consolidation did not catch. This is the
highest-value output of the three runs.

H5 **Undefined terms are unstable to measure.** v1 found 65 rules using an
undefined term, v2 found 149, with the same index. Edge Agent leads both (8,
10), then brief, sweeper, overlay, resolver, deliverable, repo mapping. The
D12 definition pass should start from the union and let a human cut it.

H6 **Context edge count does not find too_small.** 152 rules have one context
edge; they are leaf requirements that need one model rule. The 5 too_small
verdicts came from restatement (WL-REQ-42 restates WL-REQ-41), not edge count.
C4's signal should be "restates its parent", with the connectivity report
kept as a secondary view.

H7 **Residual ambiguities, each with a proposed answer.**
(a) A rule holding two catalogues (WL-REQ-1323: routes and commands) is two
rules. (b) A multi-term parent (WL-REQ-130, 202, 225, 339) is n definitions
plus the parent's own decision, if any. (c) A one-off repair procedure
(WL-REQ-46's merge script) is a requirement that `needs` the invariant. (d) A
long surface block is a subject; a surface sentence is not; the cut is whether
it states a fact of its own. (e) Heading/body mismatch (WL-REQ-180, 172, 338)
is a finding, not a criterion.

### 600 rules and the human

600 rows is a lot for a person to hold, and no person will. What a person
navigates is the document: fifteen specs today, each a template of 13 to 86
rules, plus a glossary. That is the same ratio as a codebase: nobody tracks
600 functions, they track 15 modules and search for the function. The rule is
the addressable unit for reviews, tasks, agents and `lode search`; the
document is the unit for reading. D17 sets a size for the document instead,
and the per-spec table above says which specs are over it.

### Spec size in tokens

`lode show --inline` text of each spec. "lode" is the backbone's own estimate
(`planTokens`: runes × 4/7, the one the 32k soft / 64k hard plan budget is
measured with). "cl100k-ish" is chars / 3.6, closer to what a tokenizer
returns on English markdown. The lode estimate is deliberately pessimistic by
about 2×.

| Spec | words | lode | cl100k-ish |
|---|---|---|---|
| WL-SPEC-72 | 2229 | 7696 | 3741 |
| WL-SPEC-73 | 5765 | 21893 | 10642 |
| WL-SPEC-74 | 5883 | 22232 | 10808 |
| WL-SPEC-75 | 10044 | 38709 | 18817 |
| WL-SPEC-76 | 4328 | 16752 | 8144 |
| WL-SPEC-77 | 11458 | 41443 | 20146 |
| WL-SPEC-78 | 8304 | 30697 | 14922 |
| WL-SPEC-79 | 6649 | 26997 | 13124 |
| WL-SPEC-80 | 8304 | 32236 | 15671 |
| WL-SPEC-81 | 5837 | 22534 | 10954 |
| WL-SPEC-82 | 4810 | 17723 | 8616 |
| WL-SPEC-83 | 1295 | 4722 | 2296 |
| WL-SPEC-84 | 2785 | 10386 | 5049 |
| WL-SPEC-85 | 2831 | 10091 | 4906 |
| WL-SPEC-86 | 391 | 1519 | 739 |
| all fifteen | 80913 | 305630 | 148575 |

By the plan yardstick, WL-SPEC-75, WL-SPEC-77 and WL-SPEC-80 are over the
32k soft limit and none is over the hard one. The same three top the per-spec
rule count (78, 86, 58 after the pass), so the token budget and D17's rule
budget pick the same specs to split. The whole corpus is about 150k real
tokens: readable by one agent in one context, not by one agent per task. That
is the number the closure metric is for.

## Follow-ups filed

- WL-1011 (draft): find governing rules for the 288 ungoverned feature tasks;
  first step is a rule kind for `lode search`.
- WL-1012 (draft): deliver rule closures to Claude Code. Static path:
  `.claude/rules/*.md` with `paths:` globs derived from rule coverage,
  rendered by `lode install`. Dynamic path: `lode-hook` on PreToolUse
  `Edit|Write` returning the closure of every rule covering the edited path as
  `additionalContext` (10,000 chars per hook). Invariants go first. Sources in
  the task body.
  Token behaviour, from the docs (memory, hooks, context-window,
  prompt-caching pages): a path-scoped rule is appended to message history
  once, at the first Read/Edit/Write of a matching file, and stays until
  `/compact`, which drops it; it reloads at the next matching read. No fork.
  Hook `additionalContext` is wrapped in a system reminder next to the tool
  result on every firing, with no built-in dedupe, so a hook that fires per
  Edit repeats its text per Edit unless it keeps a per-session flag. Both are
  appends after the cached prefix, so neither invalidates the cache. So: rules
  files cost their text once per session per area touched; hooks cost their
  text per firing but can pick the closure per path and stay within 10k chars.
  Rules files win for a stable set an agent keeps editing; hooks win when the
  rule set is large and each edit needs a sliver, or when a block is needed.
  A hook that writes the rules file on first touch gets both.
- WL-1013 to WL-1026: one decision task per confirmed conflict pair, each
  governed by both rules. A verification pass confirmed 15 of the 16 pairs
  (WL-RULE-1321 vs WL-REQ-124 cover different approvals, dropped); the three
  header rules are one task. Three are weak: `done_state` (both only warn),
  domain `external_id` (105 says "wherever possible"), and `in_review` entry.
  Two conflict with a rule marked "designed, not built" (WL-REQ-1299,
  WL-REQ-1298), so the decision there is which design wins.

Also: `template` is not a rule kind. Template text is the document's; the
test runs used it as a verdict for "not a rule" and the wording is fixed.
