package ns

import "testing"

func TestIsConceptIRI(t *testing.T) {
	for iri, want := range map[string]bool{
		ConceptNS + "requirement":         true,
		ConceptNS + "RuleKind":            true,
		ConceptNS + "nope":                false,
		"requirement":                     false,
		"https://example.org/requirement": false,
	} {
		if got := IsConceptIRI(iri); got != want {
			t.Errorf("IsConceptIRI(%q) = %v, want %v", iri, got, want)
		}
	}
}
