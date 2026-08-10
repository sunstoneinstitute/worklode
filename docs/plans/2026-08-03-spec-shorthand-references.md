---
status: draft
covers:
  - docs/specs/014-design-documents-as-graph-objects.md#sec-11.3
  - docs/specs/026-design-doc-queries.md#sec-4.2
  - docs/specs/010-per-project-task-keys.md#sec-2
---
# Spec shorthand references — `WL-SPEC-23`

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `<PROJECTKEY>-SPEC|ADR-<n>[#sec-N]` a parseable, resolvable reference form — in Go for the future `lode doc` commands, and in `secfmt.py` for the commit hook that is the only gate docs-only PRs actually pass through. Reserve `SPEC`/`ADR` as project keys so the middle token can never be a project.

**Architecture:** The grammar exists twice, because the hook is Python and cannot depend on a build (026 §0). `testdata/shorthand.yaml` is the single source of truth for the grammar — input, expected parse, expected canonical form, expected error — and both test suites drive off it, so a divergence is a test failure rather than a corpus that means two things. Resolution is three tiers keyed on the project key (026 §4.2): the current project resolves offline against `docs/specs/NNN-*.md` and a miss is a defect; a foreign key with no backbone is reported unresolved and does not fail. Tier 2 is not built here — it reads 025's `docs` rows, which do not exist.

**Tech Stack:** Go 1.25+, Python 3 stdlib only (`secfmt.py` has no third-party imports and must keep none — it runs in a fresh checkout before anything is installed), golang-migrate, pgx.

**Read first:**
- `docs/specs/014-design-documents-as-graph-objects.md` §11.1, §11.3 — the grammar and why `<TYPE>` exists
- `docs/specs/026-design-doc-queries.md` §4, §4.2 — resolution tiers, the degradation rule, `project_key`, kind inference
- `docs/specs/010-per-project-task-keys.md` §2 — the key CHECK being amended
- `internal/designdoc/frontmatter.go` — `Frontmatter`, `RefList`, `AnchorMap`: every field a reference can appear in
- `scripts/secfmt.py:221-278` — `anchor_alternation`, `retarget_own_keys`, `update_refs`: how it already rewrites frontmatter textually, which is the pattern task 3 follows
- `internal/api/admin.go:26-28` — `projectKeyRe`, the mirror of the CHECK constraint

**Conventions:**
- `go test ./internal/...`; store and API tests need Postgres with pgvector (`TEST_POSTGRES_DSN` overrides the DSN).
- `./scripts/secfmt.py -l` must stay clean; `./scripts/check-migrations.sh --no-fix` after task 5.
- Commit after every task, imperative mood, no trailers.

**Non-goals:** `lode doc` commands (026 §7, not yet implemented — task 2 leaves an API for them and no caller). Tier 2 backbone resolution. Full frontmatter reference-integrity checking (026 §4) — task 4 touches only shorthand-shaped values and leaves path references exactly as it finds them.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/designdoc/testdata/shorthand.yaml` (new) | The shared grammar fixture — both suites read it |
| `internal/designdoc/shorthand.go` (new) | `Shorthand` — parse, normalise, `String()` |
| `internal/designdoc/shorthand_test.go` (new) | Fixture-driven grammar tests |
| `internal/designdoc/resolve.go` (new) | Tier selection, corpus lookup by number, kind check |
| `internal/designdoc/resolve_test.go` (new) | Tier behaviour against a fixture corpus |
| `scripts/secfmt.py` | Same grammar; tier-1 resolution; canonical-form rewrite; `--strict-refs` |
| `scripts/secfmt_shorthand_test.py` (new) | The Python half of the fixture contract |
| `deploy/base/migrations/0010_reserved_project_keys.{up,down}.sql` (new) | `SPEC`/`ADR` excluded from the key CHECK |
| `deploy/base/kustomization.yaml` | List the new migration |
| `internal/api/admin.go` | `projectKeyRe` rejects the reserved keys |
| `.worklode/config.toml` | Gains `project_key = "WL"` |
| `docs/authoring-design-docs.md` | Already documents the shorthand; task 6 adds the `project_key` line |

---

## Task 1: The shared grammar fixture

**Files:**
- Create: `internal/designdoc/testdata/shorthand.yaml`

This task writes no code. The fixture is the contract tasks 2 and 3 both implement against, so it is authored once, first, and neither implementation may edit it to make itself pass.

- [ ] **Step 1: Write the fixture**

Create `internal/designdoc/testdata/shorthand.yaml`. Each case is `input`, plus either `parse` (key, type, number, fragment) and `canonical`, or `error` naming the rejection. Cover exactly these:

| Input | Expectation |
|---|---|
| `WL-SPEC-23` | parses: key `WL`, type `SPEC`, number 23, no fragment; canonical `WL-SPEC-23` |
| `WL-SPEC-023` | same parse; canonical `WL-SPEC-23` — padding is normalised away |
| `WL-SPEC-14#sec-2.1` | parses with fragment `sec-2.1`; canonical round-trips the fragment |
| `WL-ADR-7` | parses, type `ADR` |
| `CMS-SPEC-4` | parses — a foreign key is well-formed, resolution is a separate concern |
| `A1B2C3D4E5-SPEC-1` | parses — 10 chars is the `^[A-Z][A-Z0-9]{1,9}$` maximum |
| `WL-23` | error: no type token — this is a task id |
| `wl-spec-23` | error: lowercase |
| `WL-PLAN-3` | error: unknown type; the message must say plans have no shorthand (014 §11.3) |
| `WL-SPEC-` | error: no number |
| `WL-SPEC-2a` | error: non-numeric |
| `A1B2C3D4E5F-SPEC-1` | error: key exceeds 10 chars |
| `SPEC-SPEC-1` | error: reserved key |
| `004-execution-backbone.md` | error: not a shorthand — a path must fall through untouched, not be half-parsed |

- [ ] **Step 2: Verify**

```bash
python3 -c "import sys;print(open('internal/designdoc/testdata/shorthand.yaml').read())"
```

Reading it is the whole check — nothing consumes it yet. Confirm every row above is present and the file parses as YAML by eye.

---

## Task 2: `Shorthand` in Go

**Files:**
- Create: `internal/designdoc/shorthand.go`, `internal/designdoc/shorthand_test.go`

- [ ] **Step 1: Write the failing test**

`shorthand_test.go` reads `testdata/shorthand.yaml` into a table and runs every case through `ParseShorthand`. A case with `error` asserts a non-nil error whose message contains the named substring; a case with `parse` asserts each field and that `String()` equals `canonical`.

Add one test that is not in the fixture, because it guards a collision rather than the grammar:

```go
func TestShorthandIsNotATaskID(t *testing.T) {
	// WL-SPEC-23 must not parse as a task id anywhere, or a document
	// reference could be routed to the task API (014 §11.3).
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`^([A-Z][A-Z0-9]*-\d+)(?:-[a-z0-9-]+)?$`), // worktree.dirRe
		regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`),                // task-id shape
	} {
		if re.MatchString("WL-SPEC-23") {
			t.Fatalf("%v matched WL-SPEC-23", re)
		}
	}
}
```

Run it — it fails to compile. That is the red state.

- [ ] **Step 2: Implement**

`shorthand.go` holds:

```go
// Shorthand is a cross-corpus document reference, 014 §11.3:
//
//	<PROJECTKEY>-SPEC|ADR-<n>[#sec-<anchor>]
type Shorthand struct {
	Key      string // project key, e.g. "WL"
	Kind     string // "spec" or "adr", lowercased to match frontmatter
	Number   int
	Fragment string // "sec-2.1", empty when the whole document is meant
}

func ParseShorthand(s string) (Shorthand, error)
func (s Shorthand) String() string
```

Notes that decide the implementation:

- One anchored regexp, `^([A-Z][A-Z0-9]{1,9})-(SPEC|ADR)-(\d+)(?:#(.+))?$`. Anything not matching returns a sentinel `ErrNotShorthand` so callers can distinguish "this is a path, leave it alone" from "this is a malformed shorthand, report it". The fixture's last row depends on that distinction.
- Reserved keys (`SPEC`, `ADR`) are rejected here as well as in the database, because the parser is what a hook runs.
- `String()` re-renders from the parsed fields, which is what drops the zero padding.

- [ ] **Step 3: Verify**

```bash
go test ./internal/designdoc/ -run Shorthand -v
```

Every fixture row passes, including the two error-message assertions.

---

## Task 3: Tier resolution in Go

**Files:**
- Create: `internal/designdoc/resolve.go`, `internal/designdoc/resolve_test.go`

- [ ] **Step 1: Write the failing test**

Build a fixture corpus under `t.TempDir()`: `docs/specs/004-execution-backbone.md`, `023-keycloak-primary-auth.md`, and `030-an-adr.md` carrying `kind: adr` in its frontmatter. Assert:

| Case | Expected |
|---|---|
| `WL-SPEC-23`, key `WL` | resolves to `docs/specs/023-keycloak-primary-auth.md` |
| `WL-SPEC-023`, key `WL` | resolves to the same file |
| `WL-ADR-30`, key `WL` | resolves — the target declares `kind: adr` |
| `WL-SPEC-30`, key `WL` | defect: kind mismatch, message names both kinds |
| `WL-ADR-23`, key `WL` | defect: kind mismatch |
| `WL-SPEC-999`, key `WL` | defect: no such document |
| `CMS-SPEC-4`, key `WL` | **unresolved**, not a defect |
| `WL-SPEC-23`, key `""` | unresolved — no `project_key` configured, so tier 1 cannot apply |

The distinction the test must pin down is that unresolved and defect are different return values, not two messages on one error. 026 §4.2 makes only one of them affect an exit code.

- [ ] **Step 2: Implement**

```go
// Outcome distinguishes 026 §4.2's tiers at the call site: only Defect
// may affect an exit code.
type Outcome int

const (
	Resolved   Outcome = iota // tier 1 hit
	Unresolved                // tier 3 — foreign key, or no key configured
	Defect                    // tier 1 miss, kind mismatch, malformed
)

func ResolveShorthand(corpusRoot, projectKey string, s Shorthand) (path string, o Outcome, err error)
```

- Lookup globs `docs/specs/` for `%03d-*.md`. Exactly one match resolves; zero is a `Defect`; more than one is a `Defect` naming the candidates, since the corpus is supposed to make that impossible.
- **Three-digit padding is worklode's filename convention, not the grammar's** (014 §11.3). rdf-registry pads ADRs to four. Keep the `%03d` local to this lookup — it must not reach `Shorthand`, whose `Number` is an integer and whose `String()` emits no padding. Tier 2 is where a foreign corpus's layout gets handled, and it is out of scope here.
- Kind comes from `Frontmatter`. There is no `Kind` field yet — add `Kind string \`yaml:"kind,omitempty"\`` to `internal/designdoc/frontmatter.go`. Absent means `spec` for anything under `docs/specs/` (026 §4.2). Placing it in the struct means `Bytes()` round-tripping stays byte-exact, which `designdoc_test.go` already asserts across the real corpus — run that suite, not just the new test.
- `projectKey == ""` yields `Unresolved` for every shorthand. No error: an un-migrated repo degrades.

- [ ] **Step 3: Verify**

```bash
go test ./internal/designdoc/ -v
```

The pre-existing round-trip test over the real `docs/` tree must still pass — the new `Kind` field must not perturb frontmatter rendering.

---

## Task 4: `secfmt.py` — the Python half

**Files:**
- Modify: `scripts/secfmt.py`
- Create: `scripts/secfmt_shorthand_test.py`

- [ ] **Step 1: Write the failing test**

`secfmt_shorthand_test.py` reads the *same* `internal/designdoc/testdata/shorthand.yaml` and drives `secfmt.parse_shorthand` through every row. Path the fixture relative to the script so it works from any cwd.

The file must parse the fixture without PyYAML — `secfmt.py` has no third-party imports and the hook runs before anything is installed. Either keep the fixture to a subset a ~20-line parser handles, or have the test shell out to `python3 -c` with a hand-rolled reader. **If the fixture turns out to need real YAML, stop and escalate rather than adding a dependency** — the constraint is 026 §0 and it is not negotiable.

Add the resolution cases too:

- `requires: WL-SPEC-4` in a doc under a corpus with `004-execution-backbone.md` rewrites to `004-execution-backbone.md`
- `requires: CMS-SPEC-4` is left byte-identical and printed to stderr as unresolved
- exit code is 0 in the second case, non-zero under `--strict-refs`
- running `-w` twice changes nothing the second time

- [ ] **Step 2: Implement**

In `scripts/secfmt.py`:

- `parse_shorthand(s)` — the same regexp as task 2, returning a named tuple or `None`. `None` means "not a shorthand", which is how path references pass through untouched.
- `read_project_key(start)` — walk up for `.worklode/config.toml` (or `.lode/`), return `project_key` or `""`. Plain line matching; do not add a TOML parser.
- `normalise_shorthands(front, project_key, corpus_root)` — operate on the frontmatter block textually, the way `retarget_own_keys` already does. Rewrite a tier-1 shorthand to the resolved basename, preserving any `#sec-N`. Leave tier-3 alone and collect it for stderr.
- Wire into the `-w` path next to the existing normalisation, and add `--strict-refs` to the argument parser, documented in the module docstring alongside the other flags.

Only values that `parse_shorthand` accepts are touched. Anything else is returned as-is — this task is not implementing 026 §4's reference integrity check, and must not start reporting on path references.

- [ ] **Step 3: Verify**

```bash
python3 scripts/secfmt_shorthand_test.py
./scripts/secfmt.py -l && echo "corpus clean"
git diff --stat docs/    # must be empty: no real document should change
```

The last check is the one that matters. The corpus has no within-project shorthand today, so a correct implementation rewrites nothing.

---

## Task 5: Reserve `SPEC` and `ADR` as project keys

**Files:**
- Create: `deploy/base/migrations/0010_reserved_project_keys.up.sql`, `.down.sql`
- Modify: `deploy/base/kustomization.yaml`, `internal/api/admin.go`, `internal/api/admin_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/api/admin_test.go`, `POST /api/v1/projects` with `key: "SPEC"` returns 400 and a message naming the reservation; `key: "ADR"` likewise; `key: "SPECS"` still succeeds, since only the exact tokens are reserved.

- [ ] **Step 2: Implement**

`0010_reserved_project_keys.up.sql`:

```sql
ALTER TABLE projects DROP CONSTRAINT projects_key_format;
ALTER TABLE projects ADD CONSTRAINT projects_key_format
    CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$' AND key NOT IN ('SPEC', 'ADR'));
```

The `.down.sql` restores 0003's original CHECK verbatim. Never edit 0003 (CLAUDE.md).

In `admin.go`, keep `projectKeyRe` as the shape check and add the reserved-set test beside it, so the 400 can say which rule was broken. The existing comment says the regexp mirrors the CHECK constraint — update it to name both halves.

List the migration in `deploy/base/kustomization.yaml`.

- [ ] **Step 3: Verify**

```bash
./scripts/check-migrations.sh --no-fix
go test ./internal/api/ -run Project -v
go test ./internal/store/ -count=1
```

---

## Task 6: Commit `project_key` and close the docs

**Files:**
- Modify: `.worklode/config.toml`, `docs/authoring-design-docs.md`

- [ ] **Step 1: Implement**

Add `project_key = "WL"` to `.worklode/config.toml`. This is what turns tier 1 on for this repo; without it task 4's resolution is inert and every shorthand degrades to unresolved.

In `docs/authoring-design-docs.md`, the shorthand subsection already exists. Add the one thing it does not yet say: tier-1 checking depends on `project_key` in `.worklode/config.toml`, and a repo without it gets `unresolved` on every shorthand.

- [ ] **Step 2: Verify**

```bash
./scripts/secfmt.py -l -w && ./scripts/secindex.py
python3 scripts/secfmt_shorthand_test.py
go test ./internal/designdoc/ ./internal/api/
```

Then confirm the feature works end to end on the real corpus: add `requires: WL-SPEC-4` to a scratch copy of a spec, run `secfmt.py -w` on it, and see it rewritten to `004-execution-backbone.md`. Discard the scratch file.

---

## Done when

1. `internal/designdoc/testdata/shorthand.yaml` drives both a Go and a Python test suite, and neither implementation has a case the other lacks.
2. `WL-SPEC-23` does not match any task-id regexp in the repo.
3. `secfmt.py -w` rewrites a within-project shorthand to its filename, leaves a foreign one untouched, and is idempotent.
4. An unresolvable foreign shorthand prints to stderr and exits 0; `--strict-refs` makes it exit non-zero.
5. `secfmt.py` still imports nothing outside the standard library.
6. A project keyed `SPEC` or `ADR` is rejected by both the CHECK constraint and the API with a 400.
7. `./scripts/secfmt.py -l` is clean and `git diff docs/` shows no unintended document changes.
