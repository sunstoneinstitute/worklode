package derive

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

// Dublin Core hasPart; the other reused IRIs come from graphproj, wl: terms
// from iri.Term.
const dctHasPart = "http://purl.org/dc/terms/hasPart"

// LayoutTriples derives the observed/repo-layout document (spec 007
// deriver 2): the repo's dct:hasPart edge to each manifest component, each
// component typed wl:Component, and every unmatched path collapsed to its
// top-level prefix as a wl:unmatchedPath gap. Dot-directories (.git,
// .worklode, .github, …) are infrastructure, not coverage gaps.
func LayoutTriples(root, host, owner, name string, m *manifest.Manifest) ([]byte, error) {
	repo := iri.Repo(host, owner, name)
	var ts []graphproj.Triple
	for _, c := range m.Components {
		ts = append(ts,
			graphproj.Triple{S: repo, P: dctHasPart, O: graphproj.IRIRef(c.IRI)},
			graphproj.Triple{S: c.IRI, P: graphproj.RDFType, O: graphproj.IRIRef(iri.Term("Component"))},
		)
	}

	gaps := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if _, ok := m.Match(rel); !ok {
			top, _, _ := strings.Cut(rel, "/")
			gaps[top] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}

	prefixes := make([]string, 0, len(gaps))
	for p := range gaps {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	for _, p := range prefixes {
		ts = append(ts, graphproj.Triple{S: repo, P: iri.Term("unmatchedPath"), O: graphproj.Text(p)})
	}
	return graphproj.Document(ts), nil
}
