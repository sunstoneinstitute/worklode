---
type: llm
weight: 1
---

The answer must say there is no partial coverage: a `covers` entry always
means the whole requirement, and `coverage:`/`fullCoverageWith:` keys are
refused. The fix is to split the requirement into rules each plan covers in
full, linked with `lode rule link <new-rule> --derived-from WL-REQ-500`.

Fail if it proposes a coverage level, a percentage, a `partial` flag, or
listing WL-REQ-500 in both plans.
