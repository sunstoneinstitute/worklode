
The `post-merge` and `post-commit` hooks (WL-REQ-272) report a merge on the default branch. Both are bound because `git merge` fires the first while a squash merge or a merge-resolving commit fires only the second.

Merge reporting is a probe. The client cannot reverse a branch to an id (that is the server's job), so candidates come from the backbone (open tasks of this repo's project, each with its rendered branch) and the client answers "is this branch's work in HEAD now, and was it not in the previous commit?". Ancestry answers a true merge or fast-forward; a patch-id walk answers a squash. Rebase is out of scope. The "not in the previous commit" half is required because `lode work next` branches at the default tip, so every idle branch is already an ancestor of HEAD.

A merge whose branch is already gone falls back to the webhook. A report is never guessed from a missing branch.

