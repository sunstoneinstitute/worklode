
```bash
lode task add --kind decision --title "Approve The Grid's scope"
lode decision add WL-643 --key x-distribution --group exclude --question "Exclude distribution networks?" --type yes_no
lode task edit WL-643 --body-file deck.md
```

`lode decision list` is the plain view and `lode decision resolve` answers from the CLI. Non-goals: a presentation build tool (Slidev cannot render untrusted markdown safely; Quarto computes nothing a deck needs), decks that create questions, reproducing Figma or Slides output, `mode` on design documents, and live shared state across viewers.

