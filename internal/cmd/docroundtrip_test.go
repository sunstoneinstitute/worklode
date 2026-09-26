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
// across both trees: `covers`, `requires`, `blockedBy` and `wasDerivedFrom`,
// each authored both section-qualified and bare, on specs and on plans. Every
// document is draft: the round trip is about edges, and an accepted status
// would drag the accept gate's cascades in with it.
//
// A plan's `requires` is what WL-357 reported as silently dropped, and the same
// key on a spec (alpha requires beta) sits beside it as the control that is
// known to wire.
func roundTripCorpus() map[string]string {
	return map[string]string{
		"specs/001-alpha.md": `---
status: draft
requires:
  - 002-beta.md#sec-1
  - 002-beta.md
wasDerivedFrom: 003-gamma.md
---

# Alpha

## One {#sec-1}

Alpha's only section.
`,
		"specs/002-beta.md": `---
status: draft
---

# Beta

## One {#sec-1}

## Two {#sec-2}
`,
		"specs/003-gamma.md": `---
status: draft
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
---

# Phase one
`,
		"plans/2026-01-02-phase-two.md": `---
status: draft
covers: NO-SPEC
requires: 001-alpha.md
wasDerivedFrom: 2026-01-01-phase-one.md
blockedBy: 2026-01-01-phase-one.md
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
// stored verbatim less its header (WL-SPEC-77 §7).
//
// The third outcome is the defect this test exists for. WL-357 (a plan's
// `requires` dropped) and PR #336 (bodies drifting on re-import) were both a
// corpus on disk disagreeing with the corpus in the store while the run
// reported success. An edge that resolves, an edge kept verbatim in
// to_external, and a reference named on stderr are all fine; vanishing is not.
//
// Read-back is `doc show`'s edge lists rather than a reconstructed header.
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
			doc, err := designdoc.Parse([]byte(src))
			if err != nil {
				t.Fatalf("parse the fixture: %v", err)
			}
			fm := doc.Frontmatter
			doc.Frontmatter = nil
			if want := strings.TrimLeft(string(doc.Bytes()), "\n"); d.Body != want {
				t.Errorf("body did not round-trip:\n--- on disk, header removed ---\n%s\n--- in the store ---\n%s", want, d.Body)
			}
			doc.Frontmatter = fm
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
