package designdoc

import (
	"reflect"
	"strings"
	"testing"
)

// mustParse parses src or fails the test; a fixture that fails to parse is a
// bug in the test, not the case under test.
func mustParse(t *testing.T, src string) *Document {
	t.Helper()
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

// assertAnchors compares got against want, treating nil and empty as equal
// so a case that expects no anchors can just omit the field.
func assertAnchors(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func TestCompareSections(t *testing.T) {
	tests := []struct {
		name           string
		accepted       string
		candidate      string
		depthLimit     int
		wantAdded      []string
		wantRemoved    []string
		wantRenumbered []string
		wantChanged    []string
		wantTooDeep    []string
		wantViolations int
	}{
		{
			name: "identical",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n",
			depthLimit: DepthLimit,
		},
		{
			name: "section deleted",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n",
			depthLimit:     DepthLimit,
			wantRemoved:    []string{"sec-2"},
			wantViolations: 1,
		},
		{
			name: "renumbered under the same anchor",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 3. Second {#sec-2}\n\nBody two.\n",
			depthLimit:     DepthLimit,
			wantRenumbered: []string{"sec-2"},
			wantViolations: 1,
		},
		{
			name: "letter-suffix insert",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 1a. Extra {#sec-1a}\n\nNew body.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n",
			depthLimit: DepthLimit,
			wantAdded:  []string{"sec-1a"},
		},
		{
			name: "body edited, other section untouched",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two, edited.\n",
			depthLimit:  DepthLimit,
			wantChanged: []string{"sec-2"},
		},
		{
			name: "heading reworded, body identical",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n",
			candidate: "# Spec\n\n" +
				"## 1. Introduction {#sec-1}\n\nBody one.\n",
			depthLimit: DepthLimit,
		},
		{
			name: "heading reworded and body changed counts as changed only",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n",
			candidate: "# Spec\n\n" +
				"## 1. Introduction {#sec-1}\n\nBody one, edited.\n",
			depthLimit:  DepthLimit,
			wantChanged: []string{"sec-1"},
		},
		{
			// 025 §6.1: an anchorless heading is content within sec-4, so an
			// edit confined to it is an edit to sec-4. Section.Body alone stops
			// at the next heading of any level and would report no change,
			// leaving claims against sec-4 falsely fresh.
			name: "edit under an anchorless subheading changes its anchored ancestor",
			accepted: "# Spec\n\n" +
				"## 4. Ranking {#sec-4}\n\nIntro.\n\n" +
				"#### Tie-breaking\n\nOldest first.\n\n" +
				"## 5. Next {#sec-5}\n\nBody five.\n",
			candidate: "# Spec\n\n" +
				"## 4. Ranking {#sec-4}\n\nIntro.\n\n" +
				"#### Tie-breaking\n\nHighest priority first.\n\n" +
				"## 5. Next {#sec-5}\n\nBody five.\n",
			depthLimit:  DepthLimit,
			wantChanged: []string{"sec-4"},
		},
		{
			// The heading text of an anchorless subheading is content too, so
			// renaming it changes its ancestor — unlike rewording an anchored
			// heading, which is explicitly not a change (025 §3).
			name: "anchorless subheading renamed changes its anchored ancestor",
			accepted: "# Spec\n\n" +
				"## 4. Ranking {#sec-4}\n\nIntro.\n\n" +
				"#### Tie-breaking\n\nOldest first.\n",
			candidate: "# Spec\n\n" +
				"## 4. Ranking {#sec-4}\n\nIntro.\n\n" +
				"#### Ties\n\nOldest first.\n",
			depthLimit:  DepthLimit,
			wantChanged: []string{"sec-4"},
		},
		{
			// An anchored descendant is a node of its own: its body belongs to
			// it, never to its parent, so editing sec-4.1 leaves sec-4 alone
			// even though the anchorless heading beside it does roll up.
			name: "anchored descendant's body does not roll up into its parent",
			accepted: "# Spec\n\n" +
				"## 4. Ranking {#sec-4}\n\nIntro.\n\n" +
				"#### Tie-breaking\n\nOldest first.\n\n" +
				"### 4.1 Weights {#sec-4.1}\n\nWeights body.\n",
			candidate: "# Spec\n\n" +
				"## 4. Ranking {#sec-4}\n\nIntro.\n\n" +
				"#### Tie-breaking\n\nOldest first.\n\n" +
				"### 4.1 Weights {#sec-4.1}\n\nWeights body, edited.\n",
			depthLimit:  DepthLimit,
			wantChanged: []string{"sec-4.1"},
		},
		{
			// The walk stops at an anchored descendant, so an anchorless
			// heading nested under sec-4.1 rolls up into sec-4.1, not sec-4.
			name: "anchorless heading rolls up to its nearest anchored ancestor only",
			accepted: "# Spec\n\n" +
				"## 4. Ranking {#sec-4}\n\nIntro.\n\n" +
				"### 4.1 Weights {#sec-4.1}\n\nWeights body.\n\n" +
				"#### Tie-breaking\n\nOldest first.\n",
			candidate: "# Spec\n\n" +
				"## 4. Ranking {#sec-4}\n\nIntro.\n\n" +
				"### 4.1 Weights {#sec-4.1}\n\nWeights body.\n\n" +
				"#### Tie-breaking\n\nHighest priority first.\n",
			depthLimit:  DepthLimit,
			wantChanged: []string{"sec-4.1"},
		},
		{
			name: "depth-4 anchored heading exceeds default limit",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody.\n\n" +
				"### 1.1 Sub {#sec-1.1}\n\nSub body.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody.\n\n" +
				"### 1.1 Sub {#sec-1.1}\n\nSub body.\n\n" +
				"#### 1.1.1 Deep {#sec-1.1.1}\n\nDeep body.\n",
			depthLimit:     DepthLimit,
			wantAdded:      []string{"sec-1.1.1"},
			wantTooDeep:    []string{"sec-1.1.1"},
			wantViolations: 1,
		},
		{
			name: "depth-3 anchored heading is not a violation at the default limit",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody.\n\n" +
				"### 1.1 Sub {#sec-1.1}\n\nSub body.\n",
			depthLimit: DepthLimit,
			wantAdded:  []string{"sec-1.1"},
		},
		{
			name: "depth-3 anchored heading exceeds a lowered limit",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody.\n\n" +
				"### 1.1 Sub {#sec-1.1}\n\nSub body.\n",
			depthLimit:     2,
			wantAdded:      []string{"sec-1.1"},
			wantTooDeep:    []string{"sec-1.1"},
			wantViolations: 1,
		},
		{
			name: "insert plus an unrelated body edit",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one, edited.\n\n" +
				"## 1a. Extra {#sec-1a}\n\nNew body.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n",
			depthLimit:  DepthLimit,
			wantAdded:   []string{"sec-1a"},
			wantChanged: []string{"sec-1"},
		},
		{
			name: "multiple violations sort by anchor within each category",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody one.\n\n" +
				"## 2. Second {#sec-2}\n\nBody two.\n\n" +
				"## 3. Third {#sec-3}\n\nBody three.\n",
			candidate: "# Spec\n\n" +
				"## 4. First {#sec-1}\n\nBody one.\n",
			depthLimit:     DepthLimit,
			wantRemoved:    []string{"sec-2", "sec-3"},
			wantRenumbered: []string{"sec-1"},
			wantViolations: 3,
		},
		{
			name: "duplicate anchor: first occurrence wins on both sides",
			accepted: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nOriginal.\n\n" +
				"## 1. First again {#sec-1}\n\nDuplicate.\n",
			candidate: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nOriginal.\n\n" +
				"## 1. First again {#sec-1}\n\nDuplicate, edited.\n",
			depthLimit: DepthLimit,
			// Both sides resolve sec-1 to their first heading ("Original."),
			// which is identical on both sides — the edit to the ignored
			// duplicate must not surface as a change.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accepted := mustParse(t, tt.accepted)
			candidate := mustParse(t, tt.candidate)

			diff := CompareSections(accepted, candidate, tt.depthLimit)

			assertAnchors(t, "Added", diff.Added, tt.wantAdded)
			assertAnchors(t, "Removed", diff.Removed, tt.wantRemoved)
			assertAnchors(t, "Renumbered", diff.Renumbered, tt.wantRenumbered)
			assertAnchors(t, "Changed", diff.Changed, tt.wantChanged)
			assertAnchors(t, "TooDeep", diff.TooDeep, tt.wantTooDeep)

			violations := diff.Violations()
			if len(violations) != tt.wantViolations {
				t.Errorf("Violations() = %v, want %d entries", violations, tt.wantViolations)
			}
			for _, anchor := range tt.wantRemoved {
				assertViolationNames(t, violations, anchor)
			}
			for _, anchor := range tt.wantTooDeep {
				assertViolationNames(t, violations, anchor)
			}
		})
	}
}

// assertViolationNames fails unless some violation string names anchor.
func assertViolationNames(t *testing.T, violations []string, anchor string) {
	t.Helper()
	for _, v := range violations {
		if strings.Contains(v, anchor) {
			return
		}
	}
	t.Errorf("Violations() = %v, want an entry naming %q", violations, anchor)
}

// TestCompareSectionsRenumberViolationNamesBothNumbers checks the one piece
// Violations() carries beyond the anchor: the accepted and candidate section
// numbers, both required to be actionable on their own.
func TestCompareSectionsRenumberViolationNamesBothNumbers(t *testing.T) {
	accepted := mustParse(t, "# Spec\n\n## 2. Second {#sec-2}\n\nBody.\n")
	candidate := mustParse(t, "# Spec\n\n## 3. Second {#sec-2}\n\nBody.\n")

	diff := CompareSections(accepted, candidate, DepthLimit)
	violations := diff.Violations()
	if len(violations) != 1 {
		t.Fatalf("Violations() = %v, want 1 entry", violations)
	}
	v := violations[0]
	if !strings.Contains(v, "sec-2") || !strings.Contains(v, "2") || !strings.Contains(v, "3") {
		t.Errorf("Violations()[0] = %q, want it to name sec-2, the old number 2 and the new number 3", v)
	}
}

func TestTitle(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		want   string
		wantOK bool
	}{
		{
			name:   "H1 present",
			src:    "# Spec 025 — Documents in the backbone\n\n## 1. First {#sec-1}\n\nBody.\n",
			want:   "Spec 025 — Documents in the backbone",
			wantOK: true,
		},
		{
			name:   "no H1",
			src:    "## 1. First {#sec-1}\n\nBody.\n",
			wantOK: false,
		},
		{
			name:   "H1 not on the first line",
			src:    "\n<!-- generated -->\n\n# Spec 034 — Design-doc sync\n\n## 1. First {#sec-1}\n\nBody.\n",
			want:   "Spec 034 — Design-doc sync",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := mustParse(t, tt.src)
			got, ok := Title(doc)
			if ok != tt.wantOK {
				t.Fatalf("Title() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("Title() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDepthViolations pins the candidate-only 025 §6.1 rule: which anchors it
// fires on, the sorted order it reports them in, and that an anchorless or
// duplicate-suppressed heading never participates.
func TestDepthViolations(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		limit      int
		wantAnchor []string
	}{
		{
			name:  "every anchored section within the limit",
			src:   "# Spec\n\n## 1. First {#sec-1}\n\nBody.\n\n### 1.1 Sub {#sec-1-1}\n\nBody.\n",
			limit: DepthLimit,
		},
		{
			name:       "one section below the limit",
			src:        "# Spec\n\n## 1. First {#sec-1}\n\nBody.\n\n#### 1.1.1 Deep {#sec-1-1-1}\n\nBody.\n",
			limit:      DepthLimit,
			wantAnchor: []string{"sec-1-1-1"},
		},
		{
			name: "several offenders come back sorted, not in document order",
			src: "# Spec\n\n" +
				"#### 2. Zed {#sec-z}\n\nBody.\n\n" +
				"#### 1. Alpha {#sec-a}\n\nBody.\n",
			limit:      DepthLimit,
			wantAnchor: []string{"sec-a", "sec-z"},
		},
		{
			name:  "an anchorless deep heading is content, not a node",
			src:   "# Spec\n\n## 1. First {#sec-1}\n\nBody.\n\n#### Deep but anchorless\n\nBody.\n",
			limit: DepthLimit,
		},
		{
			name: "duplicate anchor: the shallow first occurrence wins",
			src: "# Spec\n\n" +
				"## 1. First {#sec-1}\n\nBody.\n\n" +
				"#### 1. First again {#sec-1}\n\nBody.\n",
			limit: DepthLimit,
		},
		{
			name: "duplicate anchor: the deep first occurrence wins",
			src: "# Spec\n\n" +
				"#### 1. First {#sec-1}\n\nBody.\n\n" +
				"## 1. First again {#sec-1}\n\nBody.\n",
			limit:      DepthLimit,
			wantAnchor: []string{"sec-1"},
		},
		{
			name:       "the limit is the caller's, not the package default",
			src:        "# Spec\n\n## 1. First {#sec-1}\n\nBody.\n\n### 1.1 Sub {#sec-1-1}\n\nBody.\n",
			limit:      2,
			wantAnchor: []string{"sec-1-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := mustParse(t, tt.src)

			got := DepthViolations(doc, tt.limit)
			if len(got) != len(tt.wantAnchor) {
				t.Fatalf("DepthViolations() = %v, want %d entries", got, len(tt.wantAnchor))
			}
			for i, anchor := range tt.wantAnchor {
				if !strings.Contains(got[i], anchor) {
					t.Errorf("DepthViolations()[%d] = %q, want it to name %q", i, got[i], anchor)
				}
			}

			// The rule has one implementation: what CompareSections reports as
			// TooDeep for this candidate must be the same anchors, so a caller
			// that wants only the depth gate can stop calling it.
			diff := CompareSections(&Document{}, doc, tt.limit)
			assertAnchors(t, "TooDeep", diff.TooDeep, tt.wantAnchor)
			if !reflect.DeepEqual(diff.Violations(), got) {
				t.Errorf("CompareSections(empty, doc).Violations() = %v, want it to equal DepthViolations() = %v",
					diff.Violations(), got)
			}
		})
	}
}
