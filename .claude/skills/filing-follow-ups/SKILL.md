---
name: filing-follow-ups
description: Use when about to defer something noticed mid-change — appending an item to docs/follow-ups.md, saying "I'll note that as a follow-up" / "out of scope, filing it for later", or listing known gaps at the end of a task.
---

# Filing follow-ups

`docs/follow-ups.md` is the frozen v1 backlog. Its own header says these items
belong in the tracker "once an instance is running" — it is running, and this
repo is the tracker. **Never append a new item to that file.** Striking an item
you just fixed, or rewriting one in place, is still fine.

Everything you were about to defer ends in exactly one of two places.

## 1. Do it now, if it is genuinely incidental

Do it now when **all four** hold:

- it lives in a file this change already touches
- it needs no decision — no schema, no new spec section, no naming call, no
  option you would otherwise ask the user about
- the fix fits inside the diff you are already writing without changing what
  that diff is about
- the checks you are already running would catch it if you got it wrong

Then fix it, and say in your report that you did.

Being *able* to do it now is not the test. A widened diff the user did not ask
for is a worse outcome than a filed task — if any of the four fails, go to
step 2 even when the fix looks easy.

## 2. Otherwise, file a lode task

```bash
lode task add --title "<the defect, not the area>" --kind bug --priority high \
  --follow-up-to "$(lode status --json | jq -r .task.id)" \
  --body-file - <<'EOF'
What is wrong, where it lives (file:line, spec §), what was ruled out, and why
it was deferred rather than fixed here.
EOF
```

Outside a task worktree, drop the `--follow-up-to` line entirely — `lode
status --json` prints prose there, not JSON, so the substitution yields
garbage rather than failing. Write the body to the standard of an existing `docs/follow-ups.md`
entry: dense, self-contained, readable by someone who was not in this session.

| Judgment | Flag |
|---|---|
| exposure or silent wrongness | `--priority critical` |
| cheap correctness fix, small diff | `--priority high` |
| dogfooding friction, or an enabler | `--priority medium` |
| chore, doc/spec hygiene | `--priority low` |
| waiting on another decision or spec | add `--draft` |
| wrongness / hygiene / doc / new capability | `--kind bug` / `chore` / `spec` / `feature` |

Report the id `lode task add` prints.

## When lode will not take it

`unauthorized` or a refused connection means this shell is not logged in. **Do
not fall back to the file.** Print the exact `lode task add` command in your
report so it can be run after `lode login`, and say plainly that the item is
unfiled.

## Red flags

| Thought | Reality |
|---|---|
| "I'll drop it in follow-ups so it isn't lost" | Nothing watches that file. A task is the thing that gets picked up. |
| "It's one line, not worth a task" | Then it passes step 1 — fix it. |
| "It's related, so it's in scope" | Related is not incidental. Run the four tests. |
| "I'll collect these and file them at the end" | Each one is a fix or a task, decided when you notice it. |
| "lode is down, the file is right there" | Print the command instead. A silent append reads as filed and isn't. |
| "The file already has items like this" | Those predate the tracker. New ones do not go there. |
