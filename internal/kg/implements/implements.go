// Package implements reads .worklode/implements.yaml (spec 025 §11.2): the
// machine-readable claim that this repository's code satisfies specific
// design-document sections, pinned to the document version validated
// against. The manifest deliberately has no component field — the claiming
// component is derived from the by: paths (resolve.go, spec 025 §11.3),
// never declared.
package implements

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

// Derived from iri.IDNS, the single owner of the id/ instance namespace.
const (
	sectionPrefix = iri.IDNS + "section/"
	docPrefix     = iri.IDNS + "doc/"
)

var (
	versionRE = regexp.MustCompile(`^v[1-9][0-9]*$`)
	// slugRE holds a document slug to the same shape as an anchor
	// (designdoc.ValidAnchor) minus the sec- prefix. Nothing else validates
	// it — docs.slug carries no CHECK constraint — and an unvalidated slug
	// reaches graphproj.IRIRef, which wraps its value in <> without escaping.
	// A slug holding a space or an angle bracket would then fail as a broken
	// N-Triples document at PUT time instead of here, naming the entry.
	slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
)

// Entry is one claim: this repo's files in By satisfy Section, validated
// against the Pinned document version. Parse normalizes Section and Pinned
// to full IRIs.
type Entry struct {
	Section string   `yaml:"section"`
	Pinned  string   `yaml:"pinned"`
	By      []string `yaml:"by"`
}

// File is a parsed .worklode/implements.yaml.
type File struct {
	Implements []Entry `yaml:"implements"`
}

// Load reads and parses the manifest at p. A missing file surfaces as
// os.IsNotExist: a repo with no claims simply has no manifest.
func Load(p string) (*File, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return f, nil
}

// Parse parses and validates manifest YAML. The wlid: CURIE of the spec's
// examples expands to the full instance IRI; both forms are accepted.
//
// Unknown keys are rejected, not ignored. The field this most matters for is
// component: — 025 §11.3 says the claiming component is derived from the by:
// paths and never declared, and a silently-ignored component: key is exactly
// the declaration the spec forbids, spelled so that nothing complains.
func Parse(data []byte) (*File, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse implements manifest: %w", err)
	}
	if len(f.Implements) == 0 {
		return nil, fmt.Errorf("implements manifest: at least one entry is required")
	}
	for i := range f.Implements {
		e := &f.Implements[i]
		docSlug, anchor, err := sectionDoc(expand(e.Section))
		if err != nil {
			return nil, fmt.Errorf("implements entry %d: %w", i, err)
		}
		pinSlug, version, err := pinnedDoc(expand(e.Pinned))
		if err != nil {
			return nil, fmt.Errorf("implements entry %d: %w", i, err)
		}
		// Re-mint from the validated parts rather than keep the input
		// string, so the stored form is whatever internal/kg/iri says the
		// grammar is even if the two ever diverge.
		e.Section = iri.Section(docSlug, anchor)
		e.Pinned = iri.DocVersion(pinSlug, version)
		if pinSlug != docSlug {
			return nil, fmt.Errorf("implements entry %d: pinned %q names doc %q, but the section belongs to %q",
				i, e.Pinned, pinSlug, docSlug)
		}
		if len(e.By) == 0 {
			return nil, fmt.Errorf("implements entry %d: by needs at least one path", i)
		}
		for j, p := range e.By {
			// path.Clean resolves the interior escapes — a/../../b becomes
			// ../b — so a leading ".." segment is the whole test. Matching
			// the segment rather than the prefix keeps a real ..hidden file
			// claimable.
			clean := path.Clean(strings.TrimSpace(p))
			if clean == "" || clean == "." || clean == ".." ||
				strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") {
				return nil, fmt.Errorf("implements entry %d: path %q is not repo-relative", i, p)
			}
			e.By[j] = clean
		}
	}
	return &f, nil
}

// expand resolves the wlid: prefix (025 §17) to the full instance namespace.
func expand(v string) string {
	if rest, ok := strings.CutPrefix(strings.TrimSpace(v), "wlid:"); ok {
		return iri.IDNS + rest
	}
	return strings.TrimSpace(v)
}

// sectionDoc validates a section IRI — id/section/<doc-slug>/<anchor> — and
// returns its doc slug and anchor.
func sectionDoc(ref string) (docSlug, anchor string, err error) {
	rest, ok := strings.CutPrefix(ref, sectionPrefix)
	if !ok {
		return "", "", fmt.Errorf("section %q is not a %s IRI", ref, sectionPrefix)
	}
	slug, anchor, ok := strings.Cut(rest, "/")
	if !ok || strings.Contains(anchor, "/") {
		return "", "", fmt.Errorf("section %q is not id/section/<doc-slug>/<anchor>", ref)
	}
	if !slugRE.MatchString(slug) {
		return "", "", fmt.Errorf("section %q: doc slug %q does not match the slug grammar", ref, slug)
	}
	if !designdoc.ValidAnchor(anchor) {
		return "", "", fmt.Errorf("section %q: anchor %q does not match the sec- grammar", ref, anchor)
	}
	return slug, anchor, nil
}

// pinnedDoc validates a versioned doc IRI — id/doc/<slug>/v<n> (025 §4) —
// and returns its doc slug and version number.
func pinnedDoc(ref string) (docSlug string, version int, err error) {
	rest, ok := strings.CutPrefix(ref, docPrefix)
	if !ok {
		return "", 0, fmt.Errorf("pinned %q is not a %s IRI", ref, docPrefix)
	}
	slug, v, ok := strings.Cut(rest, "/")
	if !ok || !versionRE.MatchString(v) {
		return "", 0, fmt.Errorf("pinned %q is not id/doc/<slug>/v<n>", ref)
	}
	if !slugRE.MatchString(slug) {
		return "", 0, fmt.Errorf("pinned %q: doc slug %q does not match the slug grammar", ref, slug)
	}
	// versionRE has held v to v[1-9][0-9]*, so Atoi can only fail on overflow.
	n, err := strconv.Atoi(strings.TrimPrefix(v, "v"))
	if err != nil {
		return "", 0, fmt.Errorf("pinned %q: version %q is out of range", ref, v)
	}
	return slug, n, nil
}
