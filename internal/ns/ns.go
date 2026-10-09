// This is the package's hand-written half — gen.go carries the enums and the
// package doc. What lives here is the two shapes every caller of an enum
// needs, so that deriving a gate from a generated list stays shorter than
// re-typing the list.

package ns

import (
	"slices"
	"strings"
)

// Set turns a generated enum into the lookup map a validation gate wants.
func Set(values []string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// OrList renders an enum the way this codebase's 422 bodies name a closed set
// — "draft, accepted, or superseded" — so a generated message reads like the
// hand-written ones it replaces.
func OrList(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " or " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", or " + values[len(values)-1]
	}
}

// EdgeTerm is one stored edge type and the terms it projects as (WL-SPEC-77 §8.1).
type EdgeTerm struct {
	Table     string // doc_edges, rule_edges or task_edges
	Type      string // the stored type value
	Property  string // full IRI of the declared property
	Inverse   string // full IRI of the inferred inverse, "" when none
	Symmetric bool
}

// DeclaredEdges returns the stored types of one edge table, sorted.
func DeclaredEdges(table string) []string {
	var out []string
	for _, e := range EdgeTerms {
		if e.Table == table {
			out = append(out, e.Type)
		}
	}
	slices.Sort(out)
	return out
}

// ConceptNS is the wlc: namespace of ns/concept.ttl.
const ConceptNS = "https://worklode.io/ns/concept/"

// IsConceptIRI reports whether iri names a scheme or a concept of
// ns/concept.ttl: wlc: followed by a scheme's or a member's local name.
func IsConceptIRI(iri string) bool {
	local, ok := strings.CutPrefix(iri, ConceptNS)
	if !ok || local == "" {
		return false
	}
	for scheme, members := range Schemes {
		if scheme == local || slices.Contains(members, local) {
			return true
		}
	}
	return false
}
