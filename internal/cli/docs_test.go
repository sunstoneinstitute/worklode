package cli

import (
	"strings"
	"testing"

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
