---
name: anti-smartass
description: Use when writing or editing specs, plans, docs, READMEs, AGENTS.md files, or other prose in this repo — plain language, precise meaning. Triggers on any prose authoring or editing task, not just design docs.
---

## Writing Style: Plain Language, Precise Meaning

Specs, plans, docs, READMEs, AGENTS.md files etc. in this repo are read by agents and humans
of varying skill levels. If possible, write these so it does not require deep specialization
in the subject matter to follow the document.  **The goal is plain language that keeps
every bit of the original precision — simpler words, not a simpler meaning.**

- **Prefer the everyday word.** Use "use" not "utilize", "start" not "initiate", "about" not
  "regarding", "so" not "in order that". Short, common words unless a longer one is genuinely
  more exact.
- **Keep sentences short and direct.** One idea per sentence. Break up long chains of clauses.
  Imperative voice ("Run the migration", not "The migration should be run").
- **Do not sacrifice precision for simplicity.** Exact meaning wins over easy reading whenever
  the two conflict. Never round off a technical constraint, drop a qualifier that changes the
  meaning, or blur a distinction just to sound simpler. Plain ≠ vague.
- **Keep terms of art — but introduce them.** Real domain terms (`Z-set`, "leapfrog triejoin",
  "materialization") are precise; keep them. On first use, add a few plain words saying what the
  term means or does, so a non-expert isn't stranded. Don't invent folksy substitutes for
  established names.
- **Cut jargon that isn't load-bearing.** Corporate/academic filler ("leverage", "facilitate",
  "utilize", "in the context of", "it is important to note that") adds no meaning — remove it.
  Keep only the technical vocabulary that carries real information.
- **Spell out the first use of an acronym** the reader may not know (e.g. "CAPI (Cluster API)"),
  then use the short form.
- **Explain the "why" in plain terms.** A one-line reason a step exists helps a non-expert far
  more than more precise wording of the step itself.

Quick test: *could a smart colleague from a different discipline follow this without a glossary,
and does it still say exactly what it said before?* If yes to both, the balance is right.
