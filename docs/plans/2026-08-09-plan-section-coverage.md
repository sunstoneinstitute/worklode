---
status: draft
covers:
  - docs/specs/033-plan-section-coverage.md#sec-0
  - docs/specs/033-plan-section-coverage.md#sec-1
  - docs/specs/033-plan-section-coverage.md#sec-2
  - docs/specs/033-plan-section-coverage.md#sec-3
  - docs/specs/033-plan-section-coverage.md#sec-4
  - docs/specs/033-plan-section-coverage.md#sec-4.1
  - docs/specs/033-plan-section-coverage.md#sec-4.2
  - docs/specs/033-plan-section-coverage.md#sec-4.3
  - docs/specs/033-plan-section-coverage.md#sec-5
---
# Plan section coverage implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Make qualified plan-section coverage parseable, corpus-validated, and constrained by the published Worklode ontology.

**Architecture:** Parse the relation in internal/designdoc while retaining the deprecated implements compatibility path. Put cross-document policy in the report-only secmeta.py checker and prove it against temporary, hermetic corpora. Add the graph contract in ns/shapes.ttl, with parsed-RDF structural checks, Turtle syntax validation, and conform/nonconform cases through the installed SHACL CLI.

**Tech Stack:** Go, gopkg.in/yaml.v3, Go tests, Python unittest/subprocess/PyYAML, Turtle, Apache Jena Riot.

## Global Constraints

- Retain plan implements as a deprecated spelling; report it and reject frontmatter carrying it with covers.
- A bare reference is complete coverage. partial/none require object form, and fullCoverageWith is only valid with partial.
- Completion targets must be repo-relative plan paths and must themselves cover the same spec section.
- Preserve secmeta.py’s report-only, corpus-wide pre-commit behaviour; introduce no Python dependency.
- Do not build lode doc coverage, rewrite existing plans, change component implementation coverage, or add graph projection work (033 §6).
- Use the installed SHACL CLI for conform/nonconform behavior; introduce no new runtime dependency.

## File Structure

- Modify: internal/designdoc/frontmatter.go — typed coverage parsing, serialization, and legacy reference projection.
- Modify: internal/designdoc/frontmatter_test.go — red/green parse, malformed YAML, compatibility, and round-trip cases.
- Modify: scripts/secmeta.py — qualified-entry policy and cross-plan closure checks.
- Create: scripts/secmeta_test.py — hermetic unittest corpus tests for 033 §5 and the coverage shape.
- Modify: ns/shapes.ttl — wl:CoverageShape cardinality and consistency constraints.

## Tasks

### Task 1 — Parse and round-trip qualified coverage

~~~yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
~~~

**Files:** Modify internal/designdoc/frontmatter.go and internal/designdoc/frontmatter_test.go.

**Interfaces:** Produce type Coverage with fields Spec string, Coverage string, and FullCoverageWith RefList; type CoverageList []Coverage; and func (f Frontmatter) CoverageEntries() CoverageList. Preserve func (f Frontmatter) CoveredSections() RefList by returning the Spec fields, and retain Implements when Covers is absent.

- [ ] **Step 1: Write failing coverage tests.** Add table-driven tests for a scalar covers value, mixed scalar/object list, retired implements object list, and both keys. Assert a scalar becomes Coverage{Spec: ref, Coverage: "full"} and an object preserves completion plans. Add malformed yaml.v3-supported inputs (spec: [], coverage: {bad: value}, and fullCoverageWith: {plan: x}) expecting decode errors, then mutate parsed coverage and reparse doc.Source() to prove the mapping round-trips.

~~~go
want := CoverageList{{Spec: "docs/specs/033-plan-section-coverage.md#sec-3", Coverage: "partial", FullCoverageWith: RefList{"docs/plans/sibling.md"}}}
if got := doc.Frontmatter.CoverageEntries(); !reflect.DeepEqual(got, want) {
    t.Fatalf("CoverageEntries() = %#v, want %#v", got, want)
}
~~~

- [ ] **Step 2: Run the focused test red.** Run: go test ./internal/designdoc -run 'Test(FrontmatterCoverage|CoveredSections)' -count=1. Expected: mapping cannot decode into the current RefList, or symbols are undefined.

- [ ] **Step 3: Implement the concrete coverage model.** Change Frontmatter.Covers and .Implements to CoverageList. Implement Coverage.UnmarshalYAML(*yaml.Node) error: scalar decodes to {Spec: scalar, Coverage: "full"}; mapping decodes via a typed auxiliary struct; all other node kinds return an error. Implement CoverageList.UnmarshalYAML for scalar-or-sequence. Make CoverageEntries choose Covers over deprecated Implements; make CoveredSections project entry.Spec in declaration order. Preserve raw-header equality/rendering so untouched headers stay verbatim and changed headers encode mapping fields.

~~~go
func (f Frontmatter) CoveredSections() RefList {
    entries := f.CoverageEntries()
    out := make(RefList, len(entries))
    for i, entry := range entries { out[i] = entry.Spec }
    return out
}
~~~

- [ ] **Step 4: Run green tests.** Run: go test ./internal/designdoc -run 'Test(FrontmatterCoverage|CoveredSections)' -count=1 && go test ./internal/designdoc -count=1. Expected: PASS, including existing scalar/list and deprecated-spelling tests.

- [ ] **Step 5: Commit.** Run:

~~~bash
git add internal/designdoc/frontmatter.go internal/designdoc/frontmatter_test.go
git commit -m "feat: parse qualified plan coverage"
~~~

### Task 2 — Validate plan coverage in hermetic corpus tests

~~~yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
~~~

**Files:** Modify scripts/secmeta.py; create scripts/secmeta_test.py.

**Interfaces:** Consume spec, coverage, and optional fullCoverageWith mappings. Produce diagnostics for every 033 §5 rule without changing secmeta.py’s exit semantics. The test helper creates a temporary repository, copies secmeta.py and secfmt.py, writes anchored specs/plans, and runs sys.executable scripts/secmeta.py docs/specs docs/plans with captured output.

- [ ] **Step 1: Write every §5 test before code changes.** Add unittest methods covering missing spec, missing coverage, unknown entry keys, invalid level, fullCoverageWith beside full and none, absent #sec-N, nonexistent completion, retired implements, and both keys. Add multi-document cases proving bare form is rejected only when another accepted plan covers the same section (not a draft sibling); completion references must be docs/plans/... repo-relative paths; and an existing target that covers a different section reports failed closure.

~~~python
result = run_secmeta({"docs/specs/s.md": SPEC,
    "docs/plans/a.md": plan("accepted", "docs/specs/s.md#sec-1"),
    "docs/plans/b.md": plan("accepted", "docs/specs/s.md#sec-1")})
self.assertEqual(result.returncode, 0)
self.assertIn("more than one accepted plan", result.stderr)
~~~

Assert the duplicate bare-form diagnostic on stderr while retaining exit code 0: corpus-wide findings are intentionally unresolved rather than frontmatter errors, because a sibling plan can still be on another branch.

- [ ] **Step 2: Run tests red.** Run: python3 -m unittest scripts/secmeta_test.py -v. Expected: failures for a same-directory completion target and for an indexed plan that exists but covers the wrong section.

- [ ] **Step 3: Implement minimal policy fixes.** In check_coverage_entry, require fullCoverageWith to be a list of strings whose paths begin docs/plans/ and end .md; reject bare filenames, shorthands, mappings, and spec paths before reference resolution. In coverage_of, retain only valid completion paths. In cross_check, report a completion target that exists but is absent from the coverage index, as well as one indexed plan that lacks the exact resolved section. Keep accepted-only bare qualification and NO-SPEC rules.

~~~python
def is_plan_path(ref):
    path, _, _ = ref.partition("#")
    return path.startswith(f"{PLANS}/") and path.endswith(".md")
~~~

- [ ] **Step 4: Run green tests and the production corpus.** Run: python3 -m unittest scripts/secmeta_test.py -v && ./scripts/secmeta.py. Expected: PASS and exit 0.

- [ ] **Step 5: Commit.** Run:

~~~bash
git add scripts/secmeta.py scripts/secmeta_test.py
git commit -m "feat: validate qualified plan coverage"
~~~

### Task 3 — Constrain reified coverage nodes in SHACL

~~~yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
~~~

**Files:** Modify ns/shapes.ttl; modify scripts/secmeta_test.py.

**Interfaces:** Produce wl:CoverageShape a sh:NodeShape targeting wl:Coverage: exactly one wl:coveringPlan, wl:coveredSection, and wl:coverageLevel; level set ( wlc:full wlc:partial wlc:none ); no wl:completedWith for full/none; and an SPARQL direct-edge consistency check for ?plan wl:covers ?section.

- [ ] **Step 1: Write a failing parsed-RDF structural test.** Add CoverageShapeTest that runs riot --output=ntriples ns/shapes.ttl, parses its N-Triples into a subject/predicate/object graph, and follows blank-node edges from wl:CoverageShape. Assert each required property node has sh:minCount 1/sh:maxCount 1, the coverage-level node has the exact RDF-list members wlc:full, wlc:partial, wlc:none, an sh:or branch has sh:maxCount 0 for wl:completedWith, and the sh:sparql node contains the missing direct wl:covers query. Keep that structural contract and add conform/nonconform behavior through the installed `shacl validate` command.

~~~python
shape = graph["https://worklode.io/ns/ontology#CoverageShape"]
level = property_node(shape, "http://www.w3.org/ns/shacl#path", "https://worklode.io/ns/ontology#coverageLevel")
self.assertEqual(rdf_list(graph, objects(level, SH + "in")[0]), [WLC + "full", WLC + "partial", WLC + "none"])
~~~

- [ ] **Step 2: Run the parsed-RDF test red.** Run: python3 -m unittest scripts.secmeta_test.CoverageShapeTest -v. Expected: FAIL because wl:CoverageShape is absent from Riot's parsed graph.

- [ ] **Step 3: Add the exact shape.** Place wl:CoverageShape beside wl:coversShape. Use typed cardinality property shapes, sh:in ( wlc:full wlc:partial wlc:none ), and an sh:or implication: partial permits completions, while full/none require sh:maxCount 0 on wl:completedWith. Add sh:sparql [ sh:select """SELECT $this WHERE { $this wl:coveringPlan ?plan ; wl:coveredSection ?section . FILTER NOT EXISTS { ?plan wl:covers ?section . } }""" ] with a message explaining direct-edge consistency.

- [ ] **Step 4: Run parsed-RDF, SHACL behavior, and syntax validation green.** Run: python3 -m unittest scripts.secmeta_test.CoverageShapeTest -v && riot --validate ns/*.ttl. Expected: PASS, including the conform/nonconform cases executed by the installed SHACL CLI.

- [ ] **Step 5: Commit.** Run:

~~~bash
git add ns/shapes.ttl scripts/secmeta_test.py
git commit -m "feat: constrain qualified coverage graph nodes"
~~~

### Task 4 — Regenerate indexes and verify completion

~~~yaml
kind: chore
priority: medium
skills:
  - superpowers:verification-before-completion
blockedBy: [1, 2, 3]
~~~

**Files:** Modify docs/plans/index.yaml only if regeneration changes it.

**Interfaces:** Consume this plan and Tasks 1–3; produce a fresh plan index and a verified repository state.

- [ ] **Step 1: Check generated indexes.** Run ./scripts/secindex.py --check. If it reports a stale plan index, run ./scripts/secindex.py, inspect the generated entry for this path, and stage only docs/plans/index.yaml; otherwise leave generated files untouched.

- [ ] **Step 2: Run the complete focused validation set.** Run:

~~~bash
go test ./internal/designdoc -count=1
python3 -m unittest scripts/secmeta_test.py -v
./scripts/secmeta.py
./scripts/secfmt.py -l
./scripts/secindex.py --check
riot --validate ns/*.ttl
~~~

Expected: every command exits 0. Repair only the responsible implementation or generated index; do not expand into 033 §6.

- [ ] **Step 3: Self-review exact coverage and consistency.** Confirm §§1–3 map to the typed compatibility API, §4’s unfinished SHACL rules are all in CoverageShape, §5 has one hermetic assertion per bullet, and §6 has no execution work. Scan this plan and changed code for unfinished-marker language; remove any result. Verify all task interfaces name the same Go/Python/Turtle symbols.

- [ ] **Step 4: Commit generated metadata only when changed.** Run:

~~~bash
git add docs/plans/index.yaml
git diff --cached --quiet || git commit -m "docs: index plan section coverage"
~~~
