
- What makes an intent enforceable rather than descriptive; every boundary today is a sentence.
- The ranking function behind `human-attention`.
- Whether drift detection stays under `org-wide-visibility` or feeds `human-attention`.
- Whether one intent per spec survives in lightly worked areas.
- Whether a unit of work can be an investigation rather than a ticket.
- `journalism` has no boundary; `familiar-surfaces` has no persona tie-break rule.
- Alt text as an accessibility contract (an `--alt` flag or a lint). Today alt text defaults to the file basename.
- Bucket per environment or prefix per environment (WL-231).
- Whether Hetzner Object Storage honours the presigned `response-*` overrides (WL-206, blocked by WL-802).
- Bulk-creating a task's decision rows from one file.
- An authenticated browser hand-off for a task body (`lode task show --web` opening a logged-in tab through the CLI auth flow).
- Whether `lode doc sections` gains `--with-drafts` and `--show-dropped`, with a footer summarising dropped sections and documents.
- Whether `lode show --inline` folds supersessions as well as amendments: a withdrawn section's rule shows each rule that `supersedes` it, marked `**[superseding WL-RULE-<a> (WL-SPEC-<n>#sec-<x>)]:**`, and with `--section` a withdrawn anchor forward-resolves to its successors.
- Whether consolidation becomes a fixpoint: backfill of the rules a section amends (`**[amended text WL-RULE-<b>]:**`), ordering by the arranging document's `issued` date, body-once back-references, and a `[cycle: A → B → A]` marker reported as a defect.
- Whether `lode show --inline` takes `--with-drafts`, treating edges from draft rules as in force.

