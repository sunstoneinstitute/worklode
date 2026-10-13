---
name: tidy-tasks
description: Repoint open tasks from withdrawn rules to their successors and convert § section citations in task titles and bodies to rule refs, fanned out over subagents
argument-hint: "[--project P] [--dry-run]"
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(python3 *)
---

Invocation arguments: $ARGUMENTS

Open tasks drift from the rules they cite. A spec revision withdraws rules
and links each one to its successor with a `supersedes` edge, but the
tasks' `governs` edges and bodies still name the old rules. Bodies also
cite spec sections by number ("DP-SPEC-12 §12"), and those numbers change
when a spec is rearranged. This skill fixes both, so every open task names
current rules by ref.

## 1. Build the worklist

```bash
python3 ${CLAUDE_SKILL_DIR}/find.py [--project P] > <scratchpad>/work.json
```

For each open task that needs work, it prints:

- `stale_edges`: withdrawn rules that govern the task, each mapped to its
  accepted successors. Successor chains are followed to the end.
- `stale_body_refs`: withdrawn rule refs in the title or body, mapped the
  same way. Ranges such as `DP-REQ-159..163` are expanded.
- `section_citations`: snippets around each `§` citation.

The script reads `lode rule show` for every rule because `lode rule list`
can leave out `governed_tasks`. An empty successor list means the rule was
withdrawn with no replacement. Report those tasks instead of guessing a
successor.

Show the user the counts and the stale-rule tasks. If `--dry-run` was
given, stop here.

## 2. Fan out

Write the per-task instructions below to `<scratchpad>/tidy-instructions.md`.
Then dispatch subagents with `model: "sonnet"`, each with about 15 tasks.
Put tasks that share a pattern in the same batch, e.g. all the "Decompose
<spec> §N" tasks. Tell each agent to touch only its own tasks. Run the
batches in parallel.

> For each task id you are given:
>
> 1. Read it: `lode task show <id> --json`.
> 2. **Stale edge** (old → successors): `lode task govern <id> --by <new>`
>    for each successor that applies, then `lode task ungovern <id> --by <old>`.
>    Do not use `--pin`. If one rule was split into several, link only the
>    successors the task's work touches, and report which ones you dropped.
> 3. **Stale body ref**: replace it with its successor ref(s). Do the same
>    judgment as in step 2. If the task names the rule on a "Governing:"
>    line and has no edge to it yet, add one with `lode task govern`.
> 4. **§ citation**: look up the ref with `lode show '<SPEC>#sec-<N>'`. The
>    ref is in the heading, e.g. `## 12. ... (DP-REQ-58) {#sec-12}`. A bare
>    "§N" refers to the spec named nearby. A range lists every section's
>    ref. Rewrite the sentence so it still reads naturally: "Governing:
>    DP-SPEC-12 §12" becomes "Governing: DP-REQ-58", and "Decompose DP-SPEC-1
>    §15 into plans" becomes "Decompose DP-REQ-238 (DP-SPEC-1) into plans".
>    Add a `governs` edge for refs on a "Governing:" line. A task body can
>    predate a renumbering, so lettered anchors such as §9a or §5.1a often
>    no longer exist. If `lode show` fails, list the spec's rules with
>    `lode rule list --doc <SPEC>`, pick the one whose topic matches the
>    sentence, and report that choice. A section with no rule ref of its
>    own: cite the nearest rule that carries the content, and report it.
>    A section of a replaced spec resolves to a withdrawn rule. Check each
>    ref with `lode rule show`, and if it is withdrawn, use its successor
>    as in step 2.
> 5. Leave alone: ADR decision numbers, plain doc refs (`DP-SPEC-8`), and
>    citations of paths or docs that no longer exist when you cannot find a
>    confident match in `lode search`. Report each one you leave.
> 6. If the current rule text changed something the body relies on (a
>    name, a path, a behaviour, whether the work is built yet), fix that
>    sentence. Otherwise keep the edit to the citation itself.
> 7. Write the body to `<scratchpad>/body-<id>.md` and apply it with
>    `lode task edit <id> --body-file <file>`. Add `--title "<new>"` if the
>    title changed.
> 8. Check the result with `lode task show <id>`: no stale refs, no "rule
>    now vN" drift on rewritten edges, and no `§` left except ones you are
>    reporting.
>
> Report for each task: old → new, the edges added and removed, and what
> you left alone and why. Keep it short.

## 3. Verify and report

Run `find.py` again. Every task left in it should be one the agents
reported as left alone on purpose. Give the user:

- the counts of edges repointed, refs replaced and citations converted;
- each judgment call, such as a dropped successor or a guessed section
  match;
- what was left alone, so they can decide what to do with it.
