package implements

import (
	"fmt"
	"sort"

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

// Resolve derives the claim set for a repository. m is the repo's
// components.yaml; nil means the single-component default (025 §11.4), an
// implicit component whose IRI is the repo coordinates (unchanged when a
// whole-repo components.yaml later declares it). An entry whose paths span
// several components splits into one claim per component; a path matching no
// component is an error naming the path, because an unattributable claim is
// an uncheckable claim.
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
			components[c.IRI] = true
		}
		for comp := range components {
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
