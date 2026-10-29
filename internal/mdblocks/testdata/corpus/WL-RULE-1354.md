
| Capability | Built by |
|---|---|
| `lode doc fetch <ref>... \| --all`, the conditional refetch and `worklode_doc_fetch_total` (§8.5); verb `fetch` joins the test's allowlist | WL-PLAN-120 |
| The `review` entity group (`add`, `list`, `show`, view `guide`, `set guide`, `set verdict`, `exec`, `comment`, `resolve`, `approve`, `submit`, `wait`, `import`) and `task set reviewers`, defined in WL-SPEC-84; verbs `comment`, `approve`, `wait` join the test's allowlist | WL-PLAN-132 to WL-PLAN-135 |
| `project workflow` (view) and `project set workflow --file <f> [--dry-run]`, defined in WL-SPEC-76 | WL-PLAN-8 |
| `project set default-deliverables` and `task edit --deliverable/--deliverable-inherit/--deliverable-none/--workflow`, defined in WL-SPEC-76 | WL-PLAN-121 |
| `graph drift --docs` (stale and orphaned implementation claims) and `graph gaps --unimplemented` (unimplemented sections), queries defined in WL-SPEC-79 | WL-PLAN-122 |
| The five remix research documents and their `UPSTREAM.md` transformation prompts (§8.2) | WL-PLAN-91 |
| Vendored skills in `plugins/claude/lode/skills/` (`assay`, `planning`, `writing-for-agents`, `tdd`, `debugging`, `handoff`, `domain-modeling`), `UPSTREAM-<plugin>.md`, `LICENSES.md`, `scripts/check-vendored-skills.py` (§8.1, §8.2) | WL-PLAN-119 |
| `lode install` writing `skillOverrides` suppression into `.claude/settings.local.json` (§8.3) | WL-PLAN-119 |
| `assay` grilling rounds over `lode show` and the doc cache, reviewed in `crit` (§8.4) | WL-PLAN-91, WL-PLAN-119 |
