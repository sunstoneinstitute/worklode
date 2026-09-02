package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
// backbone mints (`lode doc add --slug`), which carry no number prefix.
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

// Since 029 §4 a plan carries a number like every other kind, so a ref reaches
// it: the corpus has one name space, not one for the kinds `lode show` used to
// render and another for plans.
func TestResolveDocRefResolvesPlans(t *testing.T) {
	docs := resolveFixture()

	d, _, err := resolveDocRef(docs, "WL", "2026-08-19-a-plan.md")
	if err != nil {
		t.Fatalf("a plan slug did not resolve: %v", err)
	}
	if d.Kind != "plan" {
		t.Errorf("resolved kind = %q, want plan", d.Kind)
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

// WL-358: a number-led slug names the document whose slug it is. Other
// documents that merely share its number — a plan on its own 029 §4 sequence,
// another kind's number — are not candidates, and the union of the two
// criteria used to report them as a bogus ambiguity.
func TestResolveDocRefNumberLedSlugBeatsSharedNumber(t *testing.T) {
	docs := []model.Doc{
		{ID: 26, Kind: "spec", Number: 1, Slug: "001-zero-trust-gateway"},
		{ID: 27, Kind: "spec", Number: 2, Slug: "002-distributed-downloader-mesh"},
		{ID: 30, Kind: "plan", Number: 1, Slug: "2026-08-22-mesh-5-tray"},
	}

	got, _, err := resolveDocRef(docs, "EA", "001-zero-trust-gateway")
	if err != nil {
		t.Fatalf("number-led slug: %v", err)
	}
	if got.ID != 26 {
		t.Errorf("ID = %d, want 26", got.ID)
	}

	// The number fallback still serves a ref whose slug text drifted from the
	// document's current slug: nothing prefix-matches, the number is unique.
	got, _, err = resolveDocRef(docs, "EA", "002-renamed-since")
	if err != nil {
		t.Fatalf("renamed slug falls back to the number: %v", err)
	}
	if got.ID != 27 {
		t.Errorf("ID = %d, want 27", got.ID)
	}

	// A bare number names the spec: a plan sits on its own 029 §4 sequence, so
	// plan 1 shares the number with spec 001 by construction and reporting
	// that as an ambiguity would break every "025 §10" link in the corpus.
	got, _, err = resolveDocRef(docs, "EA", "1")
	if err != nil {
		t.Fatalf("bare number shared with a plan: %v", err)
	}
	if got.ID != 26 {
		t.Errorf("ID = %d, want 26", got.ID)
	}

	// Two *numbered* kinds sharing one number is still genuinely ambiguous,
	// and the candidates are exactly the documents carrying that number.
	docs = append(docs, model.Doc{ID: 31, Kind: "adr", Number: 1, Slug: "001-use-postgres"})
	_, _, err = resolveDocRef(docs, "EA", "1")
	var amb *designdoc.AmbiguousRefError
	if !errors.As(err, &amb) {
		t.Fatalf("bare shared number: err = %v, want *AmbiguousRefError", err)
	}
	if want := []string{"001-use-postgres", "001-zero-trust-gateway"}; !reflect.DeepEqual(amb.Candidates, want) {
		t.Errorf("Candidates = %v, want %v", amb.Candidates, want)
	}
}

// WL-358, second round: a number-led slug the candidate set does not hold is
// not-found, not an ambiguity over that set's own documents of the same
// number. The fixtures are the ones the bug was reported on, ids included:
// resolving edge-agent's "001-zero-trust-gateway" against worklode's corpus
// used to report worklode's spec 001 and plan 1 as candidates, neither of
// which bears the name asked for. The assertion is on the candidate list, not
// on resolve success — the previous round passed a success-only test while
// this was live.
func TestResolveDocRefForeignNumberLedSlugIsNotFound(t *testing.T) {
	// worklode's corpus, as `lode doc list --project worklode` serves it.
	docs := []model.Doc{
		{ID: 33, Kind: "spec", Number: 1, Slug: "001-identity-and-authentication"},
		{ID: 9, Kind: "plan", Number: 1, Slug: "2026-08-21-cloud-sandbox-provisioning"},
		{ID: 48, Kind: "spec", Number: 26, Slug: "026-design-doc-queries"},
		{ID: 21, Kind: "plan", Number: 26, Slug: "2026-07-25-worklode-homebrew-bottles"},
	}

	// The reported symptom: a slug from another project's corpus.
	_, _, err := resolveDocRef(docs, "WL", "001-zero-trust-gateway")
	var amb *designdoc.AmbiguousRefError
	if errors.As(err, &amb) {
		t.Fatalf("foreign number-led slug: reported candidates %v; want no document", amb.Candidates)
	}
	var notFound *designdoc.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("foreign number-led slug: err = %v, want *NotFoundError", err)
	}

	// A bare number names the spec: plan 26 sits on its own 029 §4 sequence
	// and shares the number by construction, which used to make every
	// "026 §N" the corpus writes ambiguous.
	got, _, err := resolveDocRef(docs, "WL", "26")
	if err != nil {
		t.Fatalf("bare number shared with a plan: %v", err)
	}
	if got.ID != 48 {
		t.Errorf("ID = %d, want 48", got.ID)
	}

	// The drifted-slug fallback survives only where the number is unique:
	// spec 26 and plan 26 both exist, so "026-renamed-since" names neither.
	if _, _, err := resolveDocRef(docs, "WL", "026-renamed-since"); !errors.As(err, &notFound) {
		t.Errorf("drifted slug on a shared number: err = %v, want *NotFoundError", err)
	}
}

// WL-358, second round: the four doc surfaces resolve one grammar over one
// corpus. A ref with no project key of its own — a slug, a path, a number-led
// slug — used to stop at the current project's documents in `lode show` and
// `lode doc todo` while `lode doc show` resolved the same string org-wide.
// resolveDocRefTiers now falls through to the backbone's own resolver, the
// endpoint the doc verbs already call.
//
// Only a not-found falls through: an ambiguity is an answer about this ref,
// and the fallback must not launder it into some other project's document.
func TestResolveDocRefTiersFallsBackToBackbone(t *testing.T) {
	foreign := model.Doc{ID: 26, Project: "edge-agent", Kind: "spec", Number: 1, Slug: "001-zero-trust-gateway"}
	local := []model.Doc{
		{ID: 33, Project: "worklode", Kind: "spec", Number: 1, Slug: "001-identity-and-authentication"},
		{ID: 9, Project: "worklode", Kind: "plan", Number: 1, Slug: "2026-08-21-cloud-sandbox-provisioning"},
		// A bare number is narrowed to spec and ADR, so the ambiguity below
		// needs two of those kinds sharing one number, not a plan.
		{ID: 12, Project: "worklode", Kind: "adr", Number: 1, Slug: "001-use-postgres"},
	}

	var resolved []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/docs/resolve", func(w http.ResponseWriter, r *http.Request) {
		ref := r.URL.Query().Get("ref")
		resolved = append(resolved, ref)
		if ref == foreign.Slug {
			writeTestJSON(t, w, foreign)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	c, _, err := newAPIClientWithConfig()
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx := context.Background()

	// The reported symptom, end to end: a slug the local corpus does not hold.
	got, section, err := resolveDocRefTiers(ctx, c, local, "WL", foreign.Slug+"#sec-3")
	if err != nil {
		t.Fatalf("foreign slug: %v", err)
	}
	if got.ID != foreign.ID || section != "sec-3" {
		t.Errorf("got id %d section %q, want %d sec-3", got.ID, section, foreign.ID)
	}

	// A local hit never reaches the backbone.
	before := len(resolved)
	if got, _, err := resolveDocRefTiers(ctx, c, local, "WL", "001-identity-and-authentication"); err != nil || got.ID != 33 {
		t.Fatalf("local slug: got id %d, err %v", got.ID, err)
	}
	if len(resolved) != before {
		t.Errorf("local slug hit the backbone: %v", resolved[before:])
	}

	// An ambiguity is returned as such, not widened into a lookup that would
	// answer with a document nobody asked for.
	_, _, err = resolveDocRefTiers(ctx, c, local, "WL", "1")
	var amb *designdoc.AmbiguousRefError
	if !errors.As(err, &amb) {
		t.Fatalf("ambiguous number: err = %v, want *AmbiguousRefError", err)
	}
}
