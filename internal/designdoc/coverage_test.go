package designdoc_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// buildIndex loads planFiles as the plan corpus under a temp repo root,
// using the real loader (LoadSyncCorpus) so coverage.go is exercised against
// what it will actually see. planDir is relative ("docs/plans") so the
// resulting CorpusDoc.Path values are already repo-relative — the absolute
// case is exercised separately by TestSectionAbsoluteCorpusRoot.
func buildIndex(t *testing.T, planFiles map[string]string) *designdoc.PlanIndex {
	t.Helper()
	t.Chdir(t.TempDir())
	const planDir = "docs/plans"
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range planFiles {
		writeDoc(t, planDir, name, content)
	}
	docs, err := designdoc.LoadSyncCorpus("", planDir)
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	return designdoc.NewPlanIndex(docs, "")
}

// checkSection asserts one Section() call: outcome, the covering-plan list
// (nil compares equal to an empty want slice), and the deferred-to owner.
// wantOwner is variadic purely so the ~30 pre-existing call sites that predate
// the owner return need not all be touched to pass "" explicitly; every
// caller that cares passes exactly one string.
func checkSection(t *testing.T, ix *designdoc.PlanIndex, spec, anchor string,
	wantOutcome designdoc.PlanningOutcome, want []designdoc.CoveringPlan, wantOwner ...string) {
	t.Helper()
	if len(wantOwner) > 1 {
		t.Fatalf("checkSection: got %d wantOwner args, want 0 or 1", len(wantOwner))
	}
	outcome, covering, owner := ix.Section(spec, anchor)
	if outcome != wantOutcome {
		t.Errorf("Section(%q,%q) outcome = %q, want %q", spec, anchor, outcome, wantOutcome)
	}
	if !(len(covering) == 0 && len(want) == 0) && !reflect.DeepEqual(covering, want) {
		t.Errorf("Section(%q,%q) covering = %+v, want %+v", spec, anchor, covering, want)
	}
	wantOwn := ""
	if len(wantOwner) == 1 {
		wantOwn = wantOwner[0]
	}
	if owner != wantOwn {
		t.Errorf("Section(%q,%q) owner = %q, want %q", spec, anchor, owner, wantOwn)
	}
}

const specSec1 = "docs/specs/001-example.md"

func TestSectionFull_DirectClaim(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// A superseded plan discharges exactly like an accepted one (026 §2.1,
// amended: "not draft" is the discharging set, not "accepted" alone).
func TestSectionSuperseded_DischargesFull(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: superseded\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "superseded"}})
}

// A spent plan discharges like a superseded one.
func TestSectionSpent_DischargesFull(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: spent\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "spent"}})
}

func TestSectionUnplanned_EmptyCorpus(t *testing.T) {
	ix := buildIndex(t, map[string]string{})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Unplanned, nil)
}

// covers: NO-SPEC contributes to nothing and is never a gap (026 §4.3): an
// unrelated section stays unplanned in a corpus that has one.
func TestSectionUnplanned_NoSpecPlanContributesNothing(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: NO-SPEC\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Unplanned, nil)
}

// A draft plan does not discharge: the section is plan-draft, and the plan
// appears in the covering list (WL-SPEC-78 §1.3).
func TestSectionPlanDraft_OnlyDraftCovers(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: draft\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.PlanDraft,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "draft"}})
}

// A section covered by more than one plan is planned only once every
// covering plan is accepted (WL-SPEC-78 §1.3).
func TestSectionPlanDraft_AcceptedBesideDraft(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: draft\ncovers: " + specSec1 + "#sec-1\n---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.PlanDraft, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted"},
		{Path: "docs/plans/b.md", Status: "draft"},
	})
}

// Overlap is legal: two accepted plans on one section plan it.
func TestSectionOverlapIsLegal(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: superseded\ncovers: " + specSec1 + "#sec-1\n---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted"},
		{Path: "docs/plans/b.md", Status: "superseded"},
	})
}

// A whole-document covers (no #sec-N fragment) contributes to nothing: the
// section it would have named stays unplanned.
func TestSectionWholeDocumentCoversContributesNothing(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: " + specSec1 + "\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Unplanned, nil)
}

// The retired `implements` spelling reads as `covers` (026 §5.1).
func TestSectionRetiredImplementsSpelling(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\nimplements: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// A plan claiming the same section twice reports once in the covering list.
func TestSectionDuplicateClaimDeduplicates(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - " + specSec1 + "#sec-1\n" +
			"  - " + specSec1 + "#sec-1\n" +
			"---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// NewPlanIndex ignores non-plan documents entirely, even one whose
// frontmatter happens to carry a covers-shaped key: only Kind == "plan" is
// walked for claims.
func TestNewPlanIndexIgnoresNonPlanDocs(t *testing.T) {
	specDoc := designdoc.CorpusDoc{
		Kind:   "spec",
		Path:   "docs/specs/001-example.md",
		Status: "accepted",
		Edges:  []designdoc.EdgeMeta{{Rel: "covers", Target: specSec1, TargetAnchor: "sec-1"}},
	}
	ix := designdoc.NewPlanIndex([]designdoc.CorpusDoc{specDoc}, "")
	checkSection(t, ix, specSec1, "sec-1", designdoc.Unplanned, nil)
}

// The covering list is explicitly sorted by Path, not incidentally ordered
// by however the caller happened to hand documents to NewPlanIndex — built
// directly from CorpusDoc values (bypassing LoadSyncCorpus's own
// filename-sorted loading) specifically so arrival order disagrees with the
// expected sorted order.
func TestSectionCoveringSortedByPath(t *testing.T) {
	mk := func(name string) designdoc.CorpusDoc {
		return designdoc.CorpusDoc{
			Kind:   "plan",
			Path:   "docs/plans/" + name,
			Status: "accepted",
			Edges:  []designdoc.EdgeMeta{{Rel: "covers", Target: specSec1, TargetAnchor: "sec-1"}},
		}
	}
	docs := []designdoc.CorpusDoc{mk("z.md"), mk("a.md"), mk("m.md")}
	ix := designdoc.NewPlanIndex(docs, "")
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted"},
		{Path: "docs/plans/m.md", Status: "accepted"},
		{Path: "docs/plans/z.md", Status: "accepted"},
	})
}

// TestSectionNumberedAliasResolvesToKnownSlug (WL-404): a spec whose live
// corpus identity dropped its leading number (docs/authoring-design-docs.md's
// own examples, and scripts/secmeta.py's on-disk check, still write the
// numbered git filename) is still found when a plan's covers entry names it
// that way — the plan-committed reference and the backbone's slug-derived
// identity are allowed to diverge in only that one respect.
func TestSectionNumberedAliasResolvesToKnownSlug(t *testing.T) {
	spec := designdoc.CorpusDoc{Kind: "spec", Path: "docs/specs/per-project-workflows.md"}
	plan := designdoc.CorpusDoc{
		Kind: "plan", Path: "docs/plans/a.md", Status: "accepted",
		Edges: []designdoc.EdgeMeta{{Rel: "covers", Target: "docs/specs/045-per-project-workflows.md", TargetAnchor: "sec-1"}},
	}
	ix := designdoc.NewPlanIndex([]designdoc.CorpusDoc{spec, plan}, "")
	checkSection(t, ix, "docs/specs/per-project-workflows.md", "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// TestSectionNumberedAliasRequiresAKnownDocument (WL-404): the numbered form
// is rewritten only when stripping its prefix names a document the corpus
// actually holds — a reference that merely starts with digits and matches
// nothing is a plain unresolved reference, not this alias.
func TestSectionNumberedAliasRequiresAKnownDocument(t *testing.T) {
	plan := designdoc.CorpusDoc{
		Kind: "plan", Path: "docs/plans/a.md", Status: "accepted",
		Edges: []designdoc.EdgeMeta{{Rel: "covers", Target: "docs/specs/045-per-project-workflows.md", TargetAnchor: "sec-1"}},
	}
	ix := designdoc.NewPlanIndex([]designdoc.CorpusDoc{plan}, "")
	checkSection(t, ix, "docs/specs/per-project-workflows.md", "sec-1", designdoc.Unplanned, nil)
}

// TestSectionAbsoluteCorpusRoot loads a real spec+plan corpus rooted at an
// absolute path (as designdoc.FindCorpus would hand LoadSyncCorpus), so
// every CorpusDoc.Path is absolute. Section must still find the plan a
// repo-relative `covers` entry names, by recovering the repo-relative form
// of the specPath it is handed — the CorpusDoc.Path a caller has on hand,
// not a path the caller normalises itself.
func TestSectionAbsoluteCorpusRoot(t *testing.T) {
	root := t.TempDir()
	specDir := filepath.Join(root, "docs", "specs")
	planDir := filepath.Join(root, "docs", "plans")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeDoc(t, specDir, "001-example.md",
		"---\nstatus: accepted\n---\n# Spec\n\n## 1. One {#sec-1}\n\nBody.\n")
	writeDoc(t, planDir, "a.md",
		"---\nstatus: accepted\ncovers: "+specSec1+"#sec-1\n---\n# A\n\nBody.\n")

	docs, err := designdoc.LoadSyncCorpus(specDir, planDir)
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	var spec designdoc.CorpusDoc
	found := false
	for _, d := range docs {
		if d.Kind == "spec" {
			spec, found = d, true
		}
	}
	if !found {
		t.Fatal("no spec doc loaded")
	}
	if !filepath.IsAbs(spec.Path) {
		t.Fatalf("spec.Path = %q, want absolute (test setup didn't reproduce the bug scenario)", spec.Path)
	}

	ix := designdoc.NewPlanIndex(docs, "")
	checkSection(t, ix, spec.Path, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// shorthandFixture builds the spec+plan pair WL-409's tests share: a spec
// numbered 25, and a plan covering it by the <KEY>-<TYPE>-<n> shorthand.
func shorthandFixture(t *testing.T) []designdoc.CorpusDoc {
	t.Helper()
	spec := designdoc.CorpusDoc{
		Kind: "spec", Path: designdoc.CorpusPath("spec", "example"), Number: 25, Status: "accepted",
		Sections: []designdoc.SectionMeta{{Anchor: "sec-1", Heading: "1. One", Depth: 2}},
	}
	plan := designdoc.CorpusDoc{
		Kind: "plan", Path: designdoc.CorpusPath("plan", "a"), Number: 1, Status: "accepted",
		Edges: []designdoc.EdgeMeta{{Rel: "covers", Target: "WL-SPEC-25", TargetAnchor: "sec-1"}},
	}
	return []designdoc.CorpusDoc{spec, plan}
}

// TestSectionShorthandCoversResolves reproduces WL-409: a plan's covers
// entry written as the <KEY>-<TYPE>-<n> shorthand names a spec that never
// lives in the plan's own directory, so normalizeRef's old bare-filename
// guess ("docs/plans/WL-SPEC-25") could never match it. With a project key,
// the fallback resolves the shorthand the same way ResolveRef does for
// `lode show`, and the section reports covered rather than unplanned.
func TestSectionShorthandCoversResolves(t *testing.T) {
	ix := designdoc.NewPlanIndex(shorthandFixture(t), "WL")
	checkSection(t, ix, "docs/specs/example.md", "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// Without a project key (every caller before WL-409, and every offline
// caller), the shorthand fallback declines rather than guessing one: the
// section reports exactly as it did before the fallback existed.
func TestSectionShorthandCoversNeedsProjectKey(t *testing.T) {
	ix := designdoc.NewPlanIndex(shorthandFixture(t), "")
	checkSection(t, ix, "docs/specs/example.md", "sec-1", designdoc.Unplanned, nil)
}

// A bare filename reaches the same claims as the repo-relative form: it
// resolves against the spec corpus's own directory, not left unresolved to
// silently miss every claim (026 review round 2, R2-1).
func TestSectionBareFilenameSpecPath(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, "001-example.md", "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// A `covers` entry written relative to the plan's own directory (§4's
// "../" form) resolves to the same key as the canonical docs/specs/... form
// (026 review round 2, R2-2).
func TestSectionCoversDotDotResolves(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: ../specs/001-example.md#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// A plan corpus rooted somewhere whose own absolute path coincidentally
// contains "docs/specs/" as an ancestor segment must still resolve its own
// plans onto docs/plans/... — resolution keys off the corpus's own loaded
// directory, never a substring match anywhere in the path (026 review round
// 2, R2-3).
func TestSectionPlanDirContainingSpecsSubstringDoesNotMisnormalise(t *testing.T) {
	base := t.TempDir()
	planDir := filepath.Join(base, "docs", "specs", "decoy", "docs", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeDoc(t, planDir, "a.md", "---\nstatus: accepted\ncovers: "+specSec1+"#sec-1\n---\n# A\n\nBody.\n")

	docs, err := designdoc.LoadSyncCorpus("", planDir)
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	ix := designdoc.NewPlanIndex(docs, "")
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// A `covers` entry with the optional leading "/" (026 §4: "docs/specs/x.md"
// and "/docs/specs/x.md" are the same reference — live in the corpus at
// docs/plans/2026-08-03-design-doc-queries-1-corpus-and-list.md's
// `covers: /docs/specs/003-gamma.md`) reaches the same claims as the
// unprefixed form (026 review round 3, R3-1 regression).
func TestSectionCoversLeadingSlashResolves(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: /" + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// A specPath written with §4's optional leading "/" reaches the same claims
// as the unprefixed form. This is the resolveDoc side of the leading slash:
// unlike a `covers` value (normalizeRef), a specPath may also be a real
// absolute filesystem path, so the "/" is stripped only on retry — remove
// that retry and this lookup keys to nothing (026 review round 3, R3-1).
func TestSectionLeadingSlashSpecPathResolves(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, "/"+specSec1, "sec-1", designdoc.Planned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}})
}

// Both meanings of a leading "/" are live at once against an absolute-rooted
// corpus: an absolute CorpusDoc.Path, whose "/" is a filesystem root that
// underDir needs intact, and a repo-relative "/docs/specs/..." reference,
// whose "/" §4 makes optional. Both must reach the same claims — stripping
// unconditionally breaks the first, never stripping breaks the second.
func TestSectionAbsolutePathAndLeadingSlashRefBothResolve(t *testing.T) {
	root := t.TempDir()
	specDir := filepath.Join(root, "docs", "specs")
	planDir := filepath.Join(root, "docs", "plans")
	for _, dir := range []string{specDir, planDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeDoc(t, specDir, "001-example.md",
		"---\nstatus: accepted\n---\n# Spec\n\n## 1. One {#sec-1}\n\nBody.\n")
	writeDoc(t, planDir, "a.md",
		"---\nstatus: accepted\ncovers: "+specSec1+"#sec-1\n---\n# A\n\nBody.\n")

	docs, err := designdoc.LoadSyncCorpus(specDir, planDir)
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	var specPath string
	for _, d := range docs {
		if d.Kind == "spec" {
			specPath = d.Path
		}
	}
	if !filepath.IsAbs(specPath) {
		t.Fatalf("spec.Path = %q, want absolute (test setup didn't reproduce the scenario)", specPath)
	}

	ix := designdoc.NewPlanIndex(docs, "")
	want := []designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted"}}
	checkSection(t, ix, specPath, "sec-1", designdoc.Planned, want)
	checkSection(t, ix, "/"+specSec1, "sec-1", designdoc.Planned, want)
}

// A plan may defer a section it does not cover at all — `covers` and
// `defers` are independent frontmatter fields (026 §5.3) — and an accepted
// plan's deferral alone reports the section deferred, with its owner, and no
// covering plan (a defers claim is not a covers claim, so it never appears in
// the covering list).
func TestSectionDeferred_NoCoveringPlan(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ndefers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    to: docs/specs/006-knowledge-graph.md\n" +
			"---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Deferred, nil,
		"docs/specs/006-knowledge-graph.md")
}

// A superseded plan's deferral still discharges the "not draft" eligibility
// test (026 §2.1's "not `draft`" discharging set applies to defers exactly as
// it does to covers), the same as TestSectionSuperseded_DischargesFull.
func TestSectionDeferred_SupersededPlanStillDefers(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: superseded\ndefers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    to: docs/specs/006-knowledge-graph.md\n" +
			"---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Deferred, nil,
		"docs/specs/006-knowledge-graph.md")
}

// A draft plan's deferral binds nothing (026 §2.1's "a draft plan has not yet
// undertaken work" applies to defers too, per WL-290's brief: the same
// eligibility rule as covers, not a separate one for defers), so the section
// stays unplanned.
func TestSectionUnplanned_DraftDefersDoesNotDefer(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: draft\ndefers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    to: docs/specs/006-knowledge-graph.md\n" +
			"---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Unplanned, nil)
}

// Two plans deferring the same section to two different owners report both,
// sorted and comma-joined — the same join spelling
// internal/store/docs.go's NeedsPlanning uses for `string_agg`.
func TestSectionDeferred_MultipleOwnersSortedAndJoined(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ndefers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    to: docs/specs/010-later.md\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ndefers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    to: docs/specs/005-earlier.md\n" +
			"---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Deferred, nil,
		"docs/specs/005-earlier.md,docs/specs/010-later.md")
}

// The same owner named by two plans reports once, the same dedup
// NeedsPlanning's `DISTINCT` gives the store-side answer.
func TestSectionDeferred_DuplicateOwnerDeduplicates(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ndefers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    to: docs/specs/006-knowledge-graph.md\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: superseded\ndefers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    to: docs/specs/006-knowledge-graph.md\n" +
			"---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Deferred, nil,
		"docs/specs/006-knowledge-graph.md")
}
