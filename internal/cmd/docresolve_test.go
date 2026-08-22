package cmd

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// resolveFixture is the document set the ref-grammar table runs against: a
// decoy that shares 014's leading digits under zero-padding, one ADR so the
// kind check has something to enforce, and a plan, which is never a
// candidate.
func resolveFixture() []model.Doc {
	return []model.Doc{
		{ID: 1, Kind: "spec", Number: 4, Slug: "004-execution"},
		{ID: 2, Kind: "spec", Number: 14, Slug: "014-design-documents-as-graph-objects"},
		{ID: 3, Kind: "spec", Number: 140, Slug: "0140-decoy"},
		{ID: 4, Kind: "adr", Number: 17, Slug: "017-some-adr"},
		{ID: 5, Kind: "plan", Number: 0, Slug: "2026-08-19-a-plan"},
	}
}

func TestResolveDocRefForms(t *testing.T) {
	docs := resolveFixture()
	tests := []struct {
		name string
		ref  string
	}{
		{"path", "docs/specs/014-design-documents-as-graph-objects.md"},
		{"bare filename", "014-design-documents-as-graph-objects.md"},
		{"slug prefix path", "014-design-documents.md"},
		{"number no padding", "14"},
		{"number padded", "014"},
		{"number with slug text", "014-design-documents"},
		{"shorthand", "WL-SPEC-14"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, section, err := resolveDocRef(docs, "WL", tt.ref)
			if err != nil {
				t.Fatalf("resolveDocRef(%q): %v", tt.ref, err)
			}
			if got.ID != 2 {
				t.Errorf("ID = %d, want 2 (%q)", got.ID, got.Slug)
			}
			if section != "" {
				t.Errorf("section = %q, want empty", section)
			}
		})
	}
}

func TestResolveDocRefZeroPaddingDoesNotCollide(t *testing.T) {
	docs := resolveFixture()

	got, _, err := resolveDocRef(docs, "WL", "WL-SPEC-4")
	if err != nil {
		t.Fatalf("WL-SPEC-4: %v", err)
	}
	if got.Slug != "004-execution" {
		t.Errorf("WL-SPEC-4 = %q, want 004-execution (zero-padding must normalise)", got.Slug)
	}

	got, _, err = resolveDocRef(docs, "WL", "014")
	if err != nil {
		t.Fatalf("014: %v", err)
	}
	if got.Slug != "014-design-documents-as-graph-objects" {
		t.Errorf("014 = %q, must not match the 0140 decoy", got.Slug)
	}
}

func TestResolveDocRefFragment(t *testing.T) {
	docs := resolveFixture()

	for _, ref := range []string{
		"WL-SPEC-14#sec-2.1",
		"14#sec-2.1",
		"docs/specs/014-design-documents-as-graph-objects.md#sec-2.1",
	} {
		got, section, err := resolveDocRef(docs, "WL", ref)
		if err != nil {
			t.Fatalf("resolveDocRef(%q): %v", ref, err)
		}
		if section != "sec-2.1" {
			t.Errorf("%q: section = %q, want sec-2.1", ref, section)
		}
		if got.ID != 2 {
			t.Errorf("%q: ID = %d, want 2", ref, got.ID)
		}
	}
}

func TestResolveDocRefAmbiguous(t *testing.T) {
	docs := []model.Doc{
		{ID: 1, Kind: "spec", Number: 4, Slug: "004-execution"},
		{ID: 2, Kind: "spec", Number: 4, Slug: "004-other"},
	}

	_, _, err := resolveDocRef(docs, "WL", "4")
	var amb *designdoc.AmbiguousRefError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want *AmbiguousRefError", err)
	}
	if amb.Ref != "4" {
		t.Errorf("Ref = %q, want 4", amb.Ref)
	}
	if want := []string{"004-execution", "004-other"}; !reflect.DeepEqual(amb.Candidates, want) {
		t.Errorf("Candidates = %v, want %v", amb.Candidates, want)
	}
}

// TestResolveDocRefSlugForm covers ref form 3: the letter-leading slugs the
// backbone mints (`lode doc new --slug`), which carry no number prefix.
func TestResolveDocRefSlugForm(t *testing.T) {
	docs := []model.Doc{
		{ID: 1, Kind: "spec", Number: 45, Slug: "per-project-workflows"},
		{ID: 2, Kind: "spec", Number: 46, Slug: "workflow-rule-engine"},
		{ID: 3, Kind: "adr", Number: 43, Slug: "secrets-catalog-home"},
		{ID: 4, Kind: "spec", Number: 47, Slug: "workflow-cockpit-columns"},
	}

	got, section, err := resolveDocRef(docs, "WL", "per-project-workflows")
	if err != nil {
		t.Fatalf("exact slug: %v", err)
	}
	if got.ID != 1 || section != "" {
		t.Errorf("exact slug = %d %q; want 1, empty section", got.ID, section)
	}

	got, section, err = resolveDocRef(docs, "WL", "secrets#sec-2")
	if err != nil {
		t.Fatalf("slug prefix with fragment: %v", err)
	}
	if got.ID != 3 || section != "sec-2" {
		t.Errorf("slug prefix = %d %q; want 3, sec-2", got.ID, section)
	}

	_, _, err = resolveDocRef(docs, "WL", "workflow")
	var amb *designdoc.AmbiguousRefError
	if !errors.As(err, &amb) {
		t.Fatalf("prefix of two slugs: err = %v; want *AmbiguousRefError", err)
	}

	_, _, err = resolveDocRef(docs, "WL", "no-such-doc")
	if err == nil || err.Error() != notFoundRefError("no-such-doc").Error() {
		t.Fatalf("miss: err = %v; want the tier-1 not-found error", err)
	}
}

// A document matching through both criteria of the number form — its number
// and its slug prefix — is one match, not an ambiguity.
func TestResolveDocRefDedupesAcrossCriteria(t *testing.T) {
	docs := resolveFixture()

	got, _, err := resolveDocRef(docs, "WL", "014-design-documents-as-graph-objects")
	if err != nil {
		t.Fatalf("resolveDocRef: %v", err)
	}
	if got.ID != 2 {
		t.Errorf("ID = %d, want 2", got.ID)
	}
}

func TestResolveDocRefUnresolvedForeignKey(t *testing.T) {
	docs := resolveFixture()

	_, _, err := resolveDocRef(docs, "WL", "CMS-SPEC-4")
	var unresolved *designdoc.UnresolvedError
	if !errors.As(err, &unresolved) {
		t.Fatalf("err = %v, want *UnresolvedError", err)
	}
	if unresolved.Key != "CMS" {
		t.Errorf("Key = %q, want CMS", unresolved.Key)
	}
}

func TestResolveDocRefUnresolvedWhenProjectKeyEmpty(t *testing.T) {
	docs := resolveFixture()

	_, _, err := resolveDocRef(docs, "", "WL-SPEC-14")
	var unresolved *designdoc.UnresolvedError
	if !errors.As(err, &unresolved) {
		t.Fatalf("err = %v, want *UnresolvedError", err)
	}
	if unresolved.Key != "WL" {
		t.Errorf("Key = %q, want WL", unresolved.Key)
	}
}

func TestResolveDocRefTier1Miss(t *testing.T) {
	docs := resolveFixture()

	for _, ref := range []string{"WL-SPEC-99", "99", "docs/specs/999-nope.md", "not-a-ref"} {
		_, _, err := resolveDocRef(docs, "WL", ref)
		if err == nil {
			t.Fatalf("ref %q: want an error", ref)
		}
		var amb *designdoc.AmbiguousRefError
		var unresolved *designdoc.UnresolvedError
		var mismatch *designdoc.KindMismatchError
		if errors.As(err, &amb) || errors.As(err, &unresolved) ||
			errors.As(err, &mismatch) || errors.Is(err, designdoc.ErrNoSpec) {
			t.Errorf("ref %q: err = %v (%T), want a plain tier-1-miss error", ref, err, err)
		}
	}
}

// A plan is never a candidate: `lode show` renders specs and ADRs only.
func TestResolveDocRefSkipsPlans(t *testing.T) {
	docs := resolveFixture()

	if _, _, err := resolveDocRef(docs, "WL", "2026-08-19-a-plan.md"); err == nil {
		t.Fatal("a plan slug resolved; want a miss")
	}
}

func TestResolveDocRefKindMismatch(t *testing.T) {
	docs := resolveFixture()

	_, _, err := resolveDocRef(docs, "WL", "WL-ADR-14")
	var mismatch *designdoc.KindMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("WL-ADR-14: err = %v, want *KindMismatchError", err)
	}
	if mismatch.Want != "adr" || mismatch.Got != "spec" {
		t.Errorf("WL-ADR-14 mismatch = %+v, want {adr spec}", mismatch)
	}

	got, _, err := resolveDocRef(docs, "WL", "WL-ADR-17")
	if err != nil {
		t.Fatalf("WL-ADR-17: %v", err)
	}
	if got.Slug != "017-some-adr" {
		t.Errorf("WL-ADR-17 = %q", got.Slug)
	}

	_, _, err = resolveDocRef(docs, "WL", "WL-SPEC-17")
	if !errors.As(err, &mismatch) {
		t.Fatalf("WL-SPEC-17: err = %v, want *KindMismatchError", err)
	}
	if mismatch.Want != "spec" || mismatch.Got != "adr" {
		t.Errorf("WL-SPEC-17 mismatch = %+v, want {spec adr}", mismatch)
	}
}

func TestResolveDocRefNoSpec(t *testing.T) {
	docs := resolveFixture()

	for _, ref := range []string{"NO-SPEC", "WL-SPEC-0", "0"} {
		if _, _, err := resolveDocRef(docs, "WL", ref); !errors.Is(err, designdoc.ErrNoSpec) {
			t.Errorf("ref %q: err = %v, want ErrNoSpec", ref, err)
		}
	}
}

func TestCheckDocKind(t *testing.T) {
	spec := model.Doc{Kind: "spec", Slug: "014-x"}
	adr := model.Doc{Kind: "adr", Slug: "017-y"}

	if err := checkDocKind(spec, "SPEC"); err != nil {
		t.Errorf("SPEC on a spec: %v", err)
	}
	if err := checkDocKind(adr, "ADR"); err != nil {
		t.Errorf("ADR on an ADR: %v", err)
	}
	var mismatch *designdoc.KindMismatchError
	if err := checkDocKind(spec, "ADR"); !errors.As(err, &mismatch) {
		t.Errorf("ADR on a spec: err = %v, want *KindMismatchError", err)
	}
	if err := checkDocKind(adr, "SPEC"); !errors.As(err, &mismatch) {
		t.Errorf("SPEC on an ADR: err = %v, want *KindMismatchError", err)
	}
}
