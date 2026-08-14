package designdoc

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is a design document's YAML header. Its keys are ontology
// property local names, not a second vocabulary (docs/authoring-design-docs.md),
// so the field set is closed: a key with no term behind it means the ontology
// is missing one.
//
// This is transitional. Once documents live in the backbone (spec 025) the
// header goes away and these become columns, so the struct deliberately does
// not grow an escape hatch for arbitrary keys.
type Frontmatter struct {
	Status         string       `yaml:"status,omitempty"`         // wl:status
	Issued         string       `yaml:"issued,omitempty"`         // dct:issued
	Covers         CoverageList `yaml:"covers,omitempty"`         // wl:covers — plans only, 026 §5
	Implements     CoverageList `yaml:"implements,omitempty"`     // retired spelling of Covers; 026 §5.1
	Requires       RefList      `yaml:"requires,omitempty"`       // dct:requires
	IsRequiredBy   RefList      `yaml:"isRequiredBy,omitempty"`   // dct:isRequiredBy
	WasDerivedFrom string       `yaml:"wasDerivedFrom,omitempty"` // prov:wasDerivedFrom
	Amends         AnchorMap    `yaml:"amends,omitempty"`         // 025 §14
	AmendedBy      AnchorMap    `yaml:"amendedBy,omitempty"`      // 025 §14
	Replaces       AnchorMap    `yaml:"replaces,omitempty"`       // dct:replaces
	IsReplacedBy   AnchorMap    `yaml:"isReplacedBy,omitempty"`   // dct:isReplacedBy
	Task           string       `yaml:"task,omitempty"`           // transitional, no term
	Kind           string       `yaml:"kind,omitempty"`           // transitional, no term; 026 §4.2 — "adr" or absent (spec)

	// raw is the header exactly as it appeared, fences and all, and inner
	// the YAML between them. raw is emitted verbatim until a field is
	// edited; inner is what an edit is detected against.
	raw   string
	inner string
}

// CoveredSections is the sections a plan undertakes to realise, reading the
// retired `implements` spelling when `covers` is absent (026 §5.1). Callers use
// this rather than either field: a document carrying both is an error
// scripts/secmeta.py reports, so the precedence here never silently picks.
func (f Frontmatter) CoveredSections() RefList {
	entries := f.CoverageEntries()
	out := make(RefList, len(entries))
	for i, entry := range entries {
		out[i] = entry.Spec
	}
	return out
}

// CoverageEntries returns the coverage assertions in the current spelling,
// falling back to the retired `implements` spelling when `covers` is absent.
func (f Frontmatter) CoverageEntries() CoverageList {
	if f.Covers != nil {
		return f.Covers
	}
	return f.Implements
}

// RefList is a document reference field. The authoring guide allows a bare
// scalar where a list is meant ("covers: foo.md"), so both spellings
// unmarshal to the same slice.
type RefList []string

// UnmarshalYAML accepts either a scalar or a sequence.
func (r *RefList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*r = RefList{s}
		return nil
	}
	var xs []string
	if err := n.Decode(&xs); err != nil {
		return err
	}
	*r = xs
	return nil
}

// Coverage is a plan's assertion about one spec section. A scalar form on
// disk is shorthand for full coverage; qualified entries use this mapping.
type Coverage struct {
	Spec             string  `yaml:"spec,omitempty"`
	Coverage         string  `yaml:"coverage,omitempty"`
	FullCoverageWith RefList `yaml:"fullCoverageWith,omitempty"`
}

func (c Coverage) isNoSpecScalar() bool {
	return c.Spec == "NO-SPEC" && c.Coverage == "full" && len(c.FullCoverageWith) == 0
}

// MarshalYAML keeps the reserved no-governing-spec sentinel unqualified. It
// has no section or coverage level to express, so the mapping form is invalid
// under spec 033 even when an unrelated frontmatter edit triggers rendering.
func (c Coverage) MarshalYAML() (any, error) {
	if c.isNoSpecScalar() {
		return c.Spec, nil
	}
	type coverageFields Coverage
	return coverageFields(c), nil
}

// CoverageList is the scalar-or-list plan coverage field.
type CoverageList []Coverage

// MarshalYAML preserves canonical `covers: NO-SPEC` for the sentinel's normal
// one-entry form. Other lists retain their authored coverage entry shapes.
func (c CoverageList) MarshalYAML() (any, error) {
	if len(c) == 1 && c[0].isNoSpecScalar() {
		return c[0].Spec, nil
	}
	type coverageEntries CoverageList
	return coverageEntries(c), nil
}

// UnmarshalYAML accepts a bare spec reference as full coverage or a qualified
// mapping. Policy validation belongs to secmeta.py; this parser only preserves
// the authored syntax faithfully.
func (c *Coverage) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		var spec string
		if err := n.Decode(&spec); err != nil {
			return err
		}
		*c = Coverage{Spec: spec, Coverage: "full"}
		return nil
	case yaml.MappingNode:
		type coverageFields struct {
			Spec             string  `yaml:"spec,omitempty"`
			Coverage         string  `yaml:"coverage,omitempty"`
			FullCoverageWith RefList `yaml:"fullCoverageWith,omitempty"`
		}
		var fields coverageFields
		if err := n.Decode(&fields); err != nil {
			return err
		}
		*c = Coverage(fields)
		return nil
	default:
		return fmt.Errorf("coverage: want scalar or mapping, got YAML node kind %d", n.Kind)
	}
}

// CoverageList accepts one coverage entry or a sequence of entries.
func (c *CoverageList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		var entry Coverage
		if err := entry.UnmarshalYAML(n); err != nil {
			return err
		}
		*c = CoverageList{entry}
		return nil
	case yaml.SequenceNode:
		entries := make(CoverageList, len(n.Content))
		for i, item := range n.Content {
			if err := entries[i].UnmarshalYAML(item); err != nil {
				return err
			}
		}
		*c = entries
		return nil
	default:
		return fmt.Errorf("coverage list: want scalar or sequence, got YAML node kind %d", n.Kind)
	}
}

// AnchorMap keys references by the anchor in *this* document they apply to:
// "#sec-3" -> the sections elsewhere that it amends or replaces. Values take
// the same scalar-or-list latitude as RefList.
type AnchorMap map[string]RefList

// splitFrontmatter divides src into the frontmatter block (fences included),
// the YAML between the fences, and the body. An unterminated block is not
// frontmatter: treating it as one would turn the whole document into
// candidate keys.
func splitFrontmatter(src string) (front, inner, body string) {
	if !strings.HasPrefix(src, "---\n") && !strings.HasPrefix(src, "---\r\n") {
		return "", "", src
	}
	lines := splitLines(src)
	for _, ln := range lines[1:] {
		if strings.TrimRight(src[ln.start:ln.textEnd], " \t\r") == "---" {
			return src[:ln.end], src[lines[1].start:ln.start], src[ln.end:]
		}
	}
	return "", "", src
}

// parseFrontmatter decodes the YAML between the fences. KnownFields makes an
// unrecognised key an error rather than a silent drop — the key set is the
// ontology's, so a typo is a defect, not an extension.
func parseFrontmatter(front, inner string) (*Frontmatter, error) {
	fm := &Frontmatter{raw: front, inner: inner}
	dec := yaml.NewDecoder(strings.NewReader(inner))
	dec.KnownFields(true)
	if err := dec.Decode(fm); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	return fm, nil
}

// source returns the frontmatter block: the original bytes while the fields
// still hold what was parsed, and a fresh rendering once any has changed.
// Keeping the untouched case verbatim preserves comments and the authored key
// order, both of which a round-trip through the marshaller would discard.
//
// The comparison re-parses raw rather than holding a snapshot struct, so a
// caller mutating a map or slice in place is still detected — a snapshot would
// share that backing array and see the edit as the original value.
func (f *Frontmatter) source() string {
	if f == nil {
		return ""
	}
	if orig, err := parseFrontmatter(f.raw, f.inner); err == nil && f.equals(orig) {
		return f.raw
	}
	return f.render()
}

// equals compares the ontology fields, ignoring the raw source.
func (f *Frontmatter) equals(other *Frontmatter) bool {
	a, b := *f, *other
	a.raw, a.inner, b.raw, b.inner = "", "", "", ""
	return reflect.DeepEqual(a, b)
}

// render writes the header back as YAML, keys in the order the authoring
// guide documents (lifecycle, covers, dependency, amendment,
// supersession), which is the struct's field order.
func (f *Frontmatter) render() string {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2) // house style; the marshaller defaults to 4
	if err := enc.Encode(f); err != nil {
		// Encoding a struct of strings, slices and maps cannot fail; if it
		// somehow does, the source is still the truthful answer.
		return f.raw
	}
	enc.Close()
	return "---\n" + b.String() + "---\n"
}
