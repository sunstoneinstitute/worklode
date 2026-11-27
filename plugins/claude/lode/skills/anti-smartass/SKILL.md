---
name: anti-smartass
description: Use when writing or editing any markdown a human reads or reviews — specs, plans, rule bodies (lode rule add, lode rule edit), task bodies, pull request descriptions, docs, READMEs, AGENTS.md files — plain language, precise meaning, structure instead of walls of text. Triggers on any prose authoring or editing task, not just design docs.
---

## Writing Style: Plain Language, Precise Meaning

Specs, plans, rule and task bodies, pull request descriptions, docs, READMEs, AGENTS.md files
etc. are read by agents and humans of varying skill levels. If possible, write these so it does not require deep specialization
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

## Structure: No Walls of Text

A body that covers more than one subject, or runs past a short paragraph, needs structure a
reader can scan. This applies to every markdown body a human reads or reviews: spec sections,
rule bodies, plans, task bodies, pull request descriptions and docs.

- **Open with what the body covers.** One or two sentences name the subject and its parts
  before any detail.
- **One subject per block.** Give each subject its own labelled block. In a rule or task body,
  use a bold label line (`**Merge**`), not a heading: the spec renders the rule under its own
  heading, and headings inside the body compete with the spec's sections. Standalone docs use
  headings.
- **Sequences become numbered lists.** Ordered steps, precedence and fallback order ("uses the
  first of …") are numbered.
- **Parallel items become bullets.** Options, error codes with their causes, and conditions
  that each stand alone.
- **Case analysis becomes a table.** Text shaped "if X … if Y … otherwise" reads better as one
  row per case. Say whether the rows are checked in order.
- **Keep the reason beside the rule.** Splitting a paragraph must not separate a rule from its
  "because".
- **Refer by name, not position.** Write "see Delete", not "step 4", because blocks get
  reordered.
- **Restructuring changes layout, not meaning.** When restructuring an existing body, keep
  every fact, qualifier, ref and code span.

If structure alone cannot make a body readable because it covers many subjects, say so and
propose a split (for a rule, one rule per subject). Do not split without asking: splitting a
rule moves which rule governs existing tasks.

Quick test: *could a smart colleague from a different discipline follow this without a glossary,
can a reviewer find the part they care about without reading all of it, and does it still say
exactly what it said before?* If yes to all three, the balance is right.
