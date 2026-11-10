---
type: llm
weight: 1
---

The answer must say:
1. A webhook drives `merged`: the merge commit landing on the default branch
   (a GitHub `push` event), not the PR merge event itself.
2. So a delay between the PR merging and the task moving is expected until
   that push is recorded, and the user should check the task timeline
   (`lode task timeline <id>`) before forcing anything.
3. Setting it by hand (`lode task set state merged`) exists but is a fallback,
   and is refused on a parent task with `childOf` children.

Fail if it claims the PR merge event alone sets `merged`, or recommends
setting the state by hand as the first step.
