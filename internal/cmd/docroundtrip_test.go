package cmd

import (
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// roundTripCorpus is a corpus written to exercise the frontmatter edge set
// across both trees: `covers`, `requires`/`isRequiredBy`,
// `blocks`/`blockedBy`, `amends`/`amendedBy`, `replaces`/`isReplacedBy` and
// `wasDerivedFrom`, each authored both section-qualified and bare, on specs
// and on plans. Every document is draft: the round trip is about edges, and
// an accepted status would drag the accept gate's cascades in with it.
//
// The pairs that cross the two trees are deliberate. A plan's `requires` is
// what WL-357 reported as silently dropped, so it is authored from both ends —
// `requires: 001-alpha.md` on the plan and `isRequiredBy: <plan>` on the spec —
// and the same key on a spec (alpha requires beta) sits beside it as the
// control that is known to wire.
func roundTripCorpus() map[string]string {
	return map[string]string{
		"specs/001-alpha.md": `---
status: draft
requires:
  - 002-beta.md#sec-1
  - 002-beta.md
isRequiredBy: 2026-01-02-phase-two.md
amends:
  "#sec-1":
    - 002-beta.md#sec-2
replaces:
  ".":
    - 003-gamma.md
wasDerivedFrom: 003-gamma.md
---

# Alpha

## One {#sec-1}

Alpha's only section.
`,
		"specs/002-beta.md": `---
status: draft
isRequiredBy:
  - 001-alpha.md
amendedBy:
  "#sec-2":
    - 001-alpha.md#sec-1
---

# Beta

## One {#sec-1}

## Two {#sec-2}
`,
		"specs/003-gamma.md": `---
status: draft
isReplacedBy:
  ".":
    - 001-alpha.md
---

# Gamma

## One {#sec-1}
`,
		"plans/2026-01-01-phase-one.md": `---
status: draft
covers:
  - 001-alpha.md#sec-1
requires:
  - 002-beta.md#sec-1
  - 003-gamma.md
blocks: 2026-01-02-phase-two.md
---

# Phase one
`,
		"plans/2026-01-02-phase-two.md": `---
status: draft
covers: NO-SPEC
requires: 001-alpha.md
wasDerivedFrom: 2026-01-01-phase-one.md
---

# Phase two
`,
		"plans/2026-01-03-phase-three.md": `---
status: draft
blockedBy: 2026-01-02-phase-two.md
---

# Phase three
`,
		"plans/2026-01-04-orphan.md": `---
status: draft
requires: other-corpus:SPEC-99
---

# Orphan
`,
	}
}

// TestDocImportRoundTrip walks a corpus through `lode doc import` and reads it
// back, holding the result to one property: **every reference the frontmatter
// declares either exists in the store or was reported**, and the body is
// stored verbatim.
//
// The third outcome is the defect this test exists for. WL-357 (a plan's
// `requires` dropped) and PR #336 (bodies drifting on re-import) were both a
// corpus on disk disagreeing with the corpus in the store while the run
// reported success. An edge that resolves, an edge kept verbatim in
// to_external, and a reference named on stderr are all fine; vanishing is not.
//
// Read-back is `doc get`'s edge lists rather than a reconstructed header,
// because the store already presents an incoming edge in the reading
// document's frame under its inverse spelling (store.docEdgeInverse) — which
// is exactly how the corpus authors the bidirectional pairs (025 §14.2). So
// `isRequiredBy` on one document and `requires` on the other are one stored
// row that satisfies both declarations, and one check covers both directions.
func TestDocImportRoundTrip(t *testing.T) {
	files := roundTripCorpus()
	dir := writeCorpus(t, files)
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	stdout, stderr, err := runLodeOutErr(t, "doc", "import", "--project", "proj", "--docs", dir)
	if err != nil {
		t.Fatalf("doc import: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "7 created") {
		t.Fatalf("import summary = %q, want 7 created", stdout)
	}

	for file, src := range files {
		slug := strings.TrimSuffix(path.Base(file), ".md")
		t.Run(slug, func(t *testing.T) {
			d := importedDoc(t, c, slug)
			if d.Body != src {
				t.Errorf("body did not round-trip:\n--- on disk ---\n%s\n--- in the store ---\n%s", src, d.Body)
			}
			doc, err := designdoc.Parse([]byte(src))
			if err != nil {
				t.Fatalf("parse the fixture: %v", err)
			}
			for _, ref := range doc.Frontmatter.Refs() {
				if edgeSurvives(d, ref) || refReported(stderr, slug, ref.Ref) {
					continue
				}
				t.Errorf("%s %q (from anchor %q) survives neither as an edge nor as a report: "+
					"silently dropped\nedges out: %+v\nedges in: %+v\nreported: %s",
					ref.Rel, ref.Ref, ref.SrcAnchor, d.Edges, d.EdgesIn, stderr)
			}
		})
	}

	// The reporting half, stated directly: a reference that genuinely resolves
	// to nothing is kept verbatim *and* named, so a clean import and a corpus
	// with a dangling reference do not read the same.
	t.Run("a dangling reference is kept verbatim and named", func(t *testing.T) {
		if !strings.Contains(stderr, "2026-01-04-orphan: other-corpus:SPEC-99") {
			t.Errorf("stderr = %q, want the unresolvable reference named", stderr)
		}
		d := importedDoc(t, c, "2026-01-04-orphan")
		if len(d.Edges) != 1 || d.Edges[0].ToExternal != "other-corpus:SPEC-99" {
			t.Errorf("edges = %+v, want the one reference kept in to_external", d.Edges)
		}
	})

	t.Run("re-importing changes neither body nor edges", func(t *testing.T) {
		before := readBackCorpus(t, c, files)
		out, err := runLode(t, "doc", "import", "--project", "proj", "--docs", dir)
		if err != nil {
			t.Fatalf("second doc import: %v\noutput: %s", err, out)
		}
		for slug, was := range before {
			now := importedDoc(t, c, slug)
			if now.Body != was.Body {
				t.Errorf("%s: the second import rewrote the body", slug)
			}
			if !sameEdges(was.Edges, now.Edges) || !sameEdges(was.EdgesIn, now.EdgesIn) {
				t.Errorf("%s: the second import changed the edges:\nbefore out %+v in %+v\nafter  out %+v in %+v",
					slug, was.Edges, was.EdgesIn, now.Edges, now.EdgesIn)
			}
		}
	})
}

// readBackCorpus reads every document of the corpus back, keyed by slug.
func readBackCorpus(t *testing.T, c *cli.Client, files map[string]string) map[string]model.DocDetail {
	t.Helper()
	out := make(map[string]model.DocDetail, len(files))
	for file := range files {
		slug := strings.TrimSuffix(path.Base(file), ".md")
		out[slug] = importedDoc(t, c, slug)
	}
	return out
}

// edgeSurvives reports whether the store holds an edge answering ref, in
// either direction: an outgoing edge for a relation this document asserts, an
// incoming one for the inverse spelling of a relation the other end asserts.
func edgeSurvives(d model.DocDetail, ref designdoc.Ref) bool {
	for _, e := range append(append([]model.DocEdge{}, d.Edges...), d.EdgesIn...) {
		if edgeAnswers(e, ref) {
			return true
		}
	}
	return false
}

// edgeAnswers reports whether one stored edge is the one ref declared: same
// relation, same anchor on this end, same anchor on the far end, and a far end
// naming the same document — by slug or corpus number when it resolved, by the
// reference text itself when it was kept in to_external.
func edgeAnswers(e model.DocEdge, ref designdoc.Ref) bool {
	if e.Type != ref.Rel || e.FromAnchor != ref.SrcAnchor {
		return false
	}
	base, fragment := designdoc.SplitFragment(ref.Ref)
	if e.ToAnchor != fragment {
		return false
	}
	if e.ToExternal != "" {
		return e.ToExternal == ref.Ref
	}
	base = strings.TrimSuffix(path.Base(base), ".md")
	if e.ToSlug == base {
		return true
	}
	n, err := strconv.Atoi(base)
	return err == nil && e.ToNumber == n
}

// refReported reports whether the import named this reference as one it could
// not resolve (printUnresolvedRefs writes "<slug>: <ref>" per line).
func refReported(stderr, slug, ref string) bool {
	return strings.Contains(stderr, slug+": "+ref+"\n")
}

// sameEdges compares two edge lists as sets — ListDocEdges orders by the
// stored columns, so a re-import that changed nothing returns them in the same
// order, but the property under test is membership, not order.
func sameEdges(a, b []model.DocEdge) bool {
	if len(a) != len(b) {
		return false
	}
	for _, want := range a {
		found := false
		for _, got := range b {
			if got.Type == want.Type && got.FromAnchor == want.FromAnchor &&
				got.ToSlug == want.ToSlug && got.ToAnchor == want.ToAnchor &&
				got.ToExternal == want.ToExternal {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestDocImportDropsAOneSidedInverseSpelling pins the one shape the round-trip
// property above forbids and the import still does: an inverse spelling whose
// acting end declares nothing back.
//
// The store records ActingRels plus `blockedBy` (store.frontmatterEdges), on
// the reasoning that `isRequiredBy` is a restatement of the other document's
// `requires` (025 §14.2) — true when the other document declares it, and
// nothing checks that it did. So this reference becomes neither an edge nor a
// report, which is exactly the third outcome WL-370 exists to forbid.
//
// It is pinned here rather than folded into roundTripCorpus so the gap is
// visible without a red suite, and so fixing it fails this test and names the
// task. Fix is WL-375: when it lands, delete this test and move the shape into
// the fixture.
func TestDocImportDropsAOneSidedInverseSpelling(t *testing.T) {
	dir := writeCorpus(t, map[string]string{
		"specs/001-target.md": "---\nstatus: draft\n---\n\n# Target\n",
		"plans/2026-02-01-one-sided.md": "---\nstatus: draft\n" +
			"isRequiredBy: 001-target.md\n---\n\n# One sided\n",
	})
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	stdout, stderr, err := runLodeOutErr(t, "doc", "import", "--project", "proj", "--docs", dir)
	if err != nil {
		t.Fatalf("doc import: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	d := importedDoc(t, c, "2026-02-01-one-sided")
	if len(d.Edges) != 0 || len(d.EdgesIn) != 0 {
		t.Fatalf("edges = %+v / %+v: WL-375 is fixed — delete this test and put the "+
			"one-sided inverse spelling in roundTripCorpus", d.Edges, d.EdgesIn)
	}
	if strings.Contains(stderr, "001-target.md") {
		t.Fatalf("stderr = %q: the reference is reported now — WL-375 is fixed, and the "+
			"round-trip property covers this shape", stderr)
	}
}
