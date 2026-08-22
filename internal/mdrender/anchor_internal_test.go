package mdrender

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// TestSectionAnchorMatchesDesigndoc: sectionAnchor decides which heading ids
// reach a document page, and internal/designdoc decides which anchors the
// corpus may carry. If the two disagree, a legal anchor is either stripped —
// leaving the Sections table linking at nothing — or an anchor the corpus
// would refuse becomes an id. This package cannot use designdoc's regexp
// directly (bluemonday wants a *regexp.Regexp, and designdoc exposes a
// predicate), so agreement is asserted rather than shared.
func TestSectionAnchorMatchesDesigndoc(t *testing.T) {
	for _, a := range []string{
		"sec-1", "sec-1.1", "sec-12.3.4", "sec-2.1a", "sec-purpose",
		"sec-a", "sec-0", "sec-one-two",
		"", "sec-", "sec--1", "sec-.1", "Sec-1", "SEC-1", "sec_1",
		"section-1", "1", "1.1", "main-content", "sec-1 sec-2",
		"sec-1\nsec-2", "sec-1\n", "\nsec-1",
	} {
		if got, want := sectionAnchor.MatchString(a), designdoc.ValidAnchor(a); got != want {
			t.Errorf("sectionAnchor.MatchString(%q) = %v, designdoc.ValidAnchor = %v", a, got, want)
		}
	}
}
