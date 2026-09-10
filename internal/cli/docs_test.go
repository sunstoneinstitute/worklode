package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

const inlineSpecBody = `---
status: accepted
---

# A spec

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// TestInlineDocNotesMarksPatchedSections is 025 §7.3's render: a patched
// section carries the marker and its notes inline, and an unpatched
// neighbour carries neither.
func TestInlineDocNotesMarksPatchedSections(t *testing.T) {
	out := InlineDocNotes(inlineSpecBody,
		[]model.DocNote{{Anchor: "sec-2", Body: "tightened the wording", CreatedBy: "stig"}},
		[]model.DocSection{{Anchor: "sec-1", Patched: true}, {Anchor: "sec-2"}})

	sec1, sec2, ok := strings.Cut(out, "## 2. Model")
	if !ok {
		t.Fatalf("render lost the second section:\n%s", out)
	}
	if !strings.Contains(sec1, docPatchedMarker) {
		t.Errorf("patched section carries no marker:\n%s", sec1)
	}
	if strings.Contains(sec2, docPatchedMarker) {
		t.Errorf("unpatched section carries the marker:\n%s", sec2)
	}
	if !strings.Contains(sec2, "tightened the wording") {
		t.Errorf("note not folded into its section:\n%s", sec2)
	}
}

// TestInlineDocNotesUntouchedWithoutMarksOrNotes: the common case returns the
// body verbatim rather than round-tripping it through the parser.
func TestInlineDocNotesUntouchedWithoutMarksOrNotes(t *testing.T) {
	if got := InlineDocNotes(inlineSpecBody, nil, []model.DocSection{{Anchor: "sec-1"}}); got != inlineSpecBody {
		t.Errorf("body was rewritten with nothing to fold:\n%s", got)
	}
}

// TestDocDetailRenderFlagsStaleness is 025 §8.7's rendering rule on
// `lode doc show`: a stale document leads with a banner, a `requires` edge
// whose target is stale says so on its own line, and a spec section covered
// by a stale plan is named in the sections table. A current target picks up
// no suffix.
func TestDocDetailRenderFlagsStaleness(t *testing.T) {
	var buf strings.Builder
	DocDetailRender(&buf, model.DocDetail{
		Doc: model.Doc{
			ID: 7, Title: "A spec", Project: "p1", Kind: "spec", Number: 25,
			Slug: "025-a", Status: "stale", Version: 3,
			UpdatedAt: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		},
		Sections: []model.DocSection{
			{Anchor: "sec-1", Number: "1", Heading: "Scope"},
			{Anchor: "sec-2", Number: "2", Heading: "Model"},
		},
		Edges: []model.DocEdge{
			{Type: "requires", ToDoc: 9, ToSlug: "004-backbone", ToAnchor: "sec-6", ToStatus: "stale"},
			{Type: "requires", ToDoc: 10, ToSlug: "006-graph", ToStatus: "accepted"},
		},
		EdgesIn: []model.DocEdge{
			{Type: "isCoveredBy", FromAnchor: "sec-2", ToDoc: 11, ToSlug: "210-plan", ToStatus: "stale"},
			{Type: "isCoveredBy", FromAnchor: "sec-1", ToDoc: 12, ToSlug: "211-plan", ToStatus: "accepted"},
		},
	})
	out := buf.String()
	for _, want := range []string{
		"STALE since " + LocalTime(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)) +
			" — re-planning owed (025 §8.6)",
		"025-a requires 004-backbone#sec-6 (stale)",
		"COVERED BY",
		"covered by 210-plan (stale)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "006-graph (stale)") {
		t.Errorf("output = %q, want no stale suffix on an accepted target", out)
	}
	// 211-plan is still listed as an inbound edge; what it must not do is
	// flag sec-1, which it covers and has not left stale.
	if strings.Contains(out, "covered by 211-plan") {
		t.Errorf("output = %q, want no coverage flag for a current covering plan", out)
	}
}

// TestDocDetailRenderWithdrawnBanner: the second status §8.7 serves
// differently gets its own banner, and a document in good standing gets none
// and no COVERED BY column.
func TestDocDetailRenderWithdrawnBanner(t *testing.T) {
	var withdrawn, accepted strings.Builder
	base := model.DocDetail{
		Doc:      model.Doc{ID: 7, Title: "A spec", Slug: "025-a", Status: "withdrawn"},
		Sections: []model.DocSection{{Anchor: "sec-1", Number: "1", Heading: "Scope"}},
	}
	DocDetailRender(&withdrawn, base)
	if !strings.Contains(withdrawn.String(), "WITHDRAWN since") {
		t.Errorf("output = %q, want a withdrawn banner", withdrawn.String())
	}

	base.Status = "accepted"
	DocDetailRender(&accepted, base)
	out := accepted.String()
	if strings.Contains(out, "WITHDRAWN") || strings.Contains(out, "STALE") {
		t.Errorf("output = %q, want no banner on an accepted document", out)
	}
	if strings.Contains(out, "COVERED BY") {
		t.Errorf("output = %q, want no coverage column when nothing is stale", out)
	}
}

// TestDocUnresolvedTableShowsAge: `lode doc list --unresolved`'s extra column
// is what makes the view readable — the age of the document, in days.
func TestDocUnresolvedTableShowsAge(t *testing.T) {
	var buf strings.Builder
	DocUnresolvedTable(&buf, []model.Doc{{
		ProjectKey: "WL", Kind: "spec", Number: 25, Title: "A spec", Status: "accepted",
		UpdatedAt: time.Now().Add(-45 * 24 * time.Hour),
	}})
	out := buf.String()
	if !strings.Contains(out, "AGE") || !strings.Contains(out, "45d") {
		t.Errorf("output = %q, want an AGE column reading 45d", out)
	}
}
