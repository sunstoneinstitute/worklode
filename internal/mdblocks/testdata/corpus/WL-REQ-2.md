
A spec describes the "what": behaviour an outside party can observe or rely on.
A plan and the code describe the "how". The gate applies only to changes to the
"what". The test is the citation test:

> Name the spec sentence this change makes true. If that sentence exists today
> and the code violates it, the change is a fix and the spec stands. If the
> sentence you would cite is one you would have to write, the change alters the
> "what" and the spec is amended first, or in the same change.

Whether a sentence exists in the spec is a fact anyone can check. Whether a
planner would have minted the task depends on the model and the prompt, so the
gate never asks it.

