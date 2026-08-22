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
	return designdoc.NewPlanIndex(docs)
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
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}})
}

func TestSectionFull_ClosedPartial(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"    fullCoverageWith:\n" +
			"      - docs/plans/b.md\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted", Level: "partial"},
		{Path: "docs/plans/b.md", Status: "accepted", Level: "full"},
	})
}

// A partial sibling still closes fullCoverageWith: the rule only requires
// "full or partial", not that the sibling is itself discharged (026 §2.1).
func TestSectionFull_ClosedByPartialSibling(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"    fullCoverageWith:\n" +
			"      - docs/plans/b.md\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted", Level: "partial"},
		{Path: "docs/plans/b.md", Status: "accepted", Level: "partial"},
	})
}

// A superseded plan discharges exactly like an accepted one (026 §2.1,
// amended: "not draft" is the discharging set, not "accepted" alone).
func TestSectionSuperseded_DischargesFull(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: superseded\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "superseded", Level: "full"}})
}

// A superseded plan's fullCoverageWith target may itself be superseded, not
// only accepted.
func TestSectionSuperseded_ClosesFullCoverageWith(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: superseded\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"    fullCoverageWith:\n" +
			"      - docs/plans/b.md\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: superseded\ncovers: " + specSec1 + "#sec-1\n---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "superseded", Level: "partial"},
		{Path: "docs/plans/b.md", Status: "superseded", Level: "full"},
	})
}

func TestSectionPartial_NoFullCoverageWith(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Partial,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "partial"}})
}

// The fullCoverageWith refusals (026 §2.1): each leaves the section partial
// rather than trusting the claim.
func TestSectionPartial_FullCoverageWithRefusals(t *testing.T) {
	cases := map[string]map[string]string{
		"empty list": {
			"a.md": "---\nstatus: accepted\ncovers:\n" +
				"  - spec: " + specSec1 + "#sec-1\n" +
				"    coverage: partial\n" +
				"    fullCoverageWith: []\n" +
				"---\n# A\n\nBody.\n",
		},
		"draft target": {
			"a.md": "---\nstatus: accepted\ncovers:\n" +
				"  - spec: " + specSec1 + "#sec-1\n" +
				"    coverage: partial\n" +
				"    fullCoverageWith:\n" +
				"      - docs/plans/b.md\n" +
				"---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: draft\ncovers: " + specSec1 + "#sec-1\n---\n# B\n\nBody.\n",
		},
		"target contributes none": {
			"a.md": "---\nstatus: accepted\ncovers:\n" +
				"  - spec: " + specSec1 + "#sec-1\n" +
				"    coverage: partial\n" +
				"    fullCoverageWith:\n" +
				"      - docs/plans/b.md\n" +
				"---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: accepted\ncovers:\n" +
				"  - spec: " + specSec1 + "#sec-1\n" +
				"    coverage: none\n" +
				"---\n# B\n\nBody.\n",
		},
		"target does not cover this section at all": {
			"a.md": "---\nstatus: accepted\ncovers:\n" +
				"  - spec: " + specSec1 + "#sec-1\n" +
				"    coverage: partial\n" +
				"    fullCoverageWith:\n" +
				"      - docs/plans/b.md\n" +
				"---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-2\n---\n# B\n\nBody.\n",
		},
		"names itself": {
			"a.md": "---\nstatus: accepted\ncovers:\n" +
				"  - spec: " + specSec1 + "#sec-1\n" +
				"    coverage: partial\n" +
				"    fullCoverageWith:\n" +
				"      - docs/plans/a.md\n" +
				"---\n# A\n\nBody.\n",
		},
	}
	// "b.md" (when present) also shows up in the covering list at whatever
	// level it claimed; a `none` claim (case "target contributes none") is
	// the one that never does.
	wantExtra := map[string][]designdoc.CoveringPlan{
		"draft target": {{Path: "docs/plans/b.md", Status: "draft", Level: "full"}},
	}
	for name, planFiles := range cases {
		t.Run(name, func(t *testing.T) {
			ix := buildIndex(t, planFiles)
			want := append([]designdoc.CoveringPlan{
				{Path: "docs/plans/a.md", Status: "accepted", Level: "partial"},
			}, wantExtra[name]...)
			checkSection(t, ix, specSec1, "sec-1", designdoc.Partial, want)
		})
	}
}

func TestSectionBoundOnly(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: none\n" +
			"---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.BoundOnly, nil)
}

func TestSectionBoundOnly_SupersededNoneDischarges(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: superseded\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: none\n" +
			"---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.BoundOnly, nil)
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

// A draft plan does not discharge, but a full/partial claim from one still
// appears in the covering list (026 §2.4 needs it to emit `plan-draft`).
func TestSectionUnplanned_OnlyDraftCovers(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: draft\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Unplanned,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "draft", Level: "full"}})
}

// A draft plan claiming `none` raises no plan-draft item (026 §2.4): it
// never appears in the covering list, and alone leaves the section
// unplanned rather than bound-only.
func TestSectionUnplanned_DraftNoneAlone(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: draft\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: none\n" +
			"---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Unplanned, nil)
}

// A draft `none` claim alongside an accepted `none` claim: the draft claim
// still never appears, and the accepted one alone decides bound-only.
func TestSectionBoundOnly_DraftNoneAlongsideAcceptedNone(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: draft\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: none\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: none\n" +
			"---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.BoundOnly, nil)
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
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}})
}

// Overlap is legal and unremarked: two discharging plans on one section both
// contribute, and the stronger claim decides the outcome.
func TestSectionOverlapIsLegal(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted", Level: "full"},
		{Path: "docs/plans/b.md", Status: "accepted", Level: "partial"},
	})
}

// A fullCoverageWith target named by bare filename (026 §4's legal
// shorthand, in live use across the corpus for plan references) still
// closes — resolved relative to the claiming plan's own directory, exactly
// scripts/secmeta.py's resolve_ref.
func TestSectionFullCoverageWithBareFilenameCloses(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"    fullCoverageWith:\n" +
			"      - b.md\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted", Level: "partial"},
		{Path: "docs/plans/b.md", Status: "accepted", Level: "full"},
	})
}

// Mutual fullCoverageWith (a names b, b names a, both accepted partial) is
// not recursively re-verified — each only has to see the other contribute
// full or partial, not itself be closed — so both close, and the covering
// list still reports each plan's own raw claimed level (partial), not the
// resolved outcome.
func TestSectionMutualFullCoverageWith(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"    fullCoverageWith:\n" +
			"      - docs/plans/b.md\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"    fullCoverageWith:\n" +
			"      - docs/plans/a.md\n" +
			"---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted", Level: "partial"},
		{Path: "docs/plans/b.md", Status: "accepted", Level: "partial"},
	})
}

// A plan claiming the same section twice reports once in the covering list.
func TestSectionDuplicateClaimDeduplicates(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: full\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: full\n" +
			"---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}})
}

// NewPlanIndex ignores non-plan documents entirely, even one whose
// frontmatter happens to carry a covers-shaped key: only Kind == "plan" is
// walked for claims.
func TestNewPlanIndexIgnoresNonPlanDocs(t *testing.T) {
	specDoc := designdoc.CorpusDoc{
		Kind:   "spec",
		Path:   "docs/specs/001-example.md",
		Status: "accepted",
		Source: []byte("---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# S\n\nBody.\n"),
	}
	ix := designdoc.NewPlanIndex([]designdoc.CorpusDoc{specDoc})
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
			Source: []byte("---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# " + name + "\n\nBody.\n"),
		}
	}
	docs := []designdoc.CorpusDoc{mk("z.md"), mk("a.md"), mk("m.md")}
	ix := designdoc.NewPlanIndex(docs)
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted", Level: "full"},
		{Path: "docs/plans/m.md", Status: "accepted", Level: "full"},
		{Path: "docs/plans/z.md", Status: "accepted", Level: "full"},
	})
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

	ix := designdoc.NewPlanIndex(docs)
	checkSection(t, ix, spec.Path, "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}})
}

// A bare filename reaches the same claims as the repo-relative form: it
// resolves against the spec corpus's own directory, not left unresolved to
// silently miss every claim (026 review round 2, R2-1).
func TestSectionBareFilenameSpecPath(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, "001-example.md", "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}})
}

// A `covers` entry written relative to the plan's own directory (§4's
// "../" form) resolves to the same key as the canonical docs/specs/... form
// (026 review round 2, R2-2).
func TestSectionCoversDotDotResolves(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: ../specs/001-example.md#sec-1\n---\n# A\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}})
}

// A "./"-prefixed sibling reference in fullCoverageWith closes correctly
// (026 review round 2, R2-2).
func TestSectionFullCoverageWithDotSlashCloses(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"    fullCoverageWith:\n" +
			"      - ./b.md\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted", Level: "partial"},
		{Path: "docs/plans/b.md", Status: "accepted", Level: "full"},
	})
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
	ix := designdoc.NewPlanIndex(docs)
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}})
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
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}})
}

// A leading "/" on a fullCoverageWith target closes correctly too.
func TestSectionFullCoverageWithLeadingSlashCloses(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"    fullCoverageWith:\n" +
			"      - /docs/plans/b.md\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ncovers: " + specSec1 + "#sec-1\n---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Full, []designdoc.CoveringPlan{
		{Path: "docs/plans/a.md", Status: "accepted", Level: "partial"},
		{Path: "docs/plans/b.md", Status: "accepted", Level: "full"},
	})
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
	checkSection(t, ix, "/"+specSec1, "sec-1", designdoc.Full,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}})
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

	ix := designdoc.NewPlanIndex(docs)
	want := []designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "full"}}
	checkSection(t, ix, specPath, "sec-1", designdoc.Full, want)
	checkSection(t, ix, "/"+specSec1, "sec-1", designdoc.Full, want)
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

// Precedence (026 §2.1): partial outranks deferred. Plan a partially covers
// the section; plan b (a different plan, so the combination is unambiguous)
// defers the same section. The outcome is partial, not deferred, and the
// owner is not reported.
func TestSectionPartial_OutranksDeferred(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: partial\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ndefers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    to: docs/specs/006-knowledge-graph.md\n" +
			"---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Partial,
		[]designdoc.CoveringPlan{{Path: "docs/plans/a.md", Status: "accepted", Level: "partial"}})
}

// Precedence (026 §2.1): deferred outranks bound-only. Plan a claims `none`
// on the section (bound-only material on its own); plan b defers it. The
// outcome is deferred, with the owner, not bound-only.
func TestSectionDeferred_OutranksBoundOnly(t *testing.T) {
	ix := buildIndex(t, map[string]string{
		"a.md": "---\nstatus: accepted\ncovers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    coverage: none\n" +
			"---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ndefers:\n" +
			"  - spec: " + specSec1 + "#sec-1\n" +
			"    to: docs/specs/006-knowledge-graph.md\n" +
			"---\n# B\n\nBody.\n",
	})
	checkSection(t, ix, specSec1, "sec-1", designdoc.Deferred, nil,
		"docs/specs/006-knowledge-graph.md")
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
