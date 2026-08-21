package implements

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

// Claim is one derived implementation claim: Component (derived from paths,
// 025 §11.3 — never declared) satisfies Section, validated against Pinned.
type Claim struct {
	Component string
	Section   string
	Pinned    string
}

// componentPrefix is the id/component/ namespace every claiming component
// must sit in. A manifest may spell its iri: freely (manifest.Parse only
// requires it non-empty), but a claim subject is rendered straight into
// N-Triples, where a relative reference like "ingest" is not a legal IRI.
const componentPrefix = iri.IDNS + "component/"

// Resolve derives the claim set for a repository. m is the repo's
// components.yaml; nil means the single-component default (025 §11.4), an
// implicit component whose IRI is the repo coordinates (unchanged when a
// whole-repo components.yaml later declares it). Pass nil for both halves of
// that default — no components.yaml, and one that declares no components.
// An entry whose paths span several components splits into one claim per
// component; a path matching no component is an error naming the path,
// because an unattributable claim is an uncheckable claim.
func Resolve(f *File, m *manifest.Manifest, repoCoords string) ([]Claim, error) {
	implicit := ""
	if m == nil {
		implicit = iri.Component(repoCoords)
	}

	seen := map[Claim]bool{}
	pins := map[[2]string]string{} // (component, section) -> pinned
	var out []Claim
	for _, e := range f.Implements {
		components := map[string]bool{}
		for _, p := range e.By {
			if m == nil {
				components[implicit] = true
				continue
			}
			c, ok := m.Match(p)
			if !ok {
				return nil, fmt.Errorf("implements: path %q matches no component in components.yaml", p)
			}
			if !strings.HasPrefix(c.IRI, componentPrefix) {
				return nil, fmt.Errorf("implements: path %q maps to component %q, which is not a %s IRI",
					p, c.IRI, componentPrefix)
			}
			components[c.IRI] = true
		}
		// Sorted, not ranged over the map: on a conflicting pin the error
		// below names one of these components, and which one must not depend
		// on map iteration order.
		for _, comp := range sortedKeys(components) {
			key := [2]string{comp, e.Section}
			if prev, ok := pins[key]; ok && prev != e.Pinned {
				return nil, fmt.Errorf("implements: %s claims %s at both %s and %s — one pin per (component, section)",
					comp, e.Section, prev, e.Pinned)
			}
			pins[key] = e.Pinned
			c := Claim{Component: comp, Section: e.Section, Pinned: e.Pinned}
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Component != out[j].Component {
			return out[i].Component < out[j].Component
		}
		return out[i].Section < out[j].Section
	})
	return out, nil
}

// sortedKeys returns set's members in order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
