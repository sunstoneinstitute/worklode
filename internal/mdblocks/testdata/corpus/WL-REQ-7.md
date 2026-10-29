
The trailer key is `Spec:`, or one of its aliases `Rule:`, `Requirement:` and
`Governed by:`. A key matches case-insensitively, with any whitespace inside it
or before its colon, and only at the start of a line. Its value names a rule: `Spec: WL-REQ-<n>`, or
`Spec: WL-REQ-<n> amended` when the pull request ships a new version of it.
Every rule ref infix parses (`WL-REQ-<n>`, `WL-RULE-<n>`). A section value
(`WL-SPEC-75 sec-5`, `WL-SPEC-75#sec-5`) is refused, because section numbers
change with a spec's arrangement and a rule ref does not. The refusal names the
rule ref the section resolves to at the document's current version, by the
same test and the same fallback as the citation checks of WL-REQ-1791.

A task minted from a plan already knows its rules: the plan covers rules, and
acceptance governs the task by them (WL-REQ-172). The CI check does not read
plans, so planned work that touches a guarded path carries a trailer too
(WL-REQ-8).

Unplanned work declares itself with one trailer on the pull request body or a
commit message. The pull request body is read first, then commit messages
newest first, and the first `Spec:` line found is the declaration:

```
Spec: WL-REQ-<n>                # satisfies this rule as written
Spec: WL-REQ-<n> amended        # this rule was amended for this change
Spec: none refactor             # one word from the "how" list of WL-REQ-4
```

The `none` reasons are a closed list: `fix`, `refactor`, `perf`, `copy`,
`tests`, `build`, `config`. A cited ref may carry `amended` or one reason word
after it. A `fix` cites the rule it restores: `Spec: WL-REQ-<n> fix`.
A `none fix` with no rule is refused, because a fix with nothing to cite is
a "what" change by the citation test of WL-REQ-2.
