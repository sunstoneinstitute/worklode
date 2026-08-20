package designdoc

import (
	"maps"
	"slices"
	"strings"
)

// This file is the one walk of a frontmatter's relation fields. Three
// consumers write edges from a header — the corpus builder here, the store's
// doc_edges rebuild, and `lode doc import`'s dry run — and they legitimately
// differ in *which* relations they record. They must not differ in how the
// header is read, so the traversal is here and the rel set is theirs.

// Ref is one reference a frontmatter declares.
type Ref struct {
	// SrcAnchor is the anchor in *this* document the reference hangs off,
	// without its leading '#'; "" is the document-level subject, which
	// AnchorMap spells ".".
	SrcAnchor string
	// Rel is the relation asserted, as an ontology property local name
	// ("covers", "amends", …).
	Rel string
	// Ref is the reference text as authored, trailing "#sec-…" fragment
	// included — split it with SplitFragment.
	Ref string
	// Coverage is the covers entry this reference came from, non-nil only
	// when Rel is "covers": that relation alone carries a level and, for a
	// partial entry, its fullCoverageWith closure (026 §5.1). It is a copy,
	// so writing through it does not reach the frontmatter.
	Coverage *Coverage
}

// ActingRels is the acting-direction relation set: the spellings that assert a
// relation rather than restate its inverse. A consumer recording one row per
// fact keeps these and drops the rest — writing both directions would double
// every edge and let the two disagree (025 §14).
var ActingRels = []string{"covers", "requires", "blocks", "wasDerivedFrom", "amends", "replaces"}

// refListRelOrder is the fixed order Refs walks the RefList fields, acting
// spelling before its inverse.
var refListRelOrder = []struct {
	rel string
	get func(*Frontmatter) RefList
}{
	{"requires", func(f *Frontmatter) RefList { return f.Requires }},
	{"isRequiredBy", func(f *Frontmatter) RefList { return f.IsRequiredBy }},
	{"blocks", func(f *Frontmatter) RefList { return f.Blocks }},
	{"blockedBy", func(f *Frontmatter) RefList { return f.BlockedBy }},
}

// anchorRelOrder is the fixed order Refs walks the four AnchorMap fields
// (025 §5.1), acting spelling before its inverse.
var anchorRelOrder = []struct {
	rel string
	get func(*Frontmatter) AnchorMap
}{
	{"amends", func(f *Frontmatter) AnchorMap { return f.Amends }},
	{"amendedBy", func(f *Frontmatter) AnchorMap { return f.AmendedBy }},
	{"replaces", func(f *Frontmatter) AnchorMap { return f.Replaces }},
	{"isReplacedBy", func(f *Frontmatter) AnchorMap { return f.IsReplacedBy }},
}

// Refs enumerates every reference the frontmatter declares, in a deterministic
// order — coverage, the dependency lists, provenance, then the anchor maps with
// their keys sorted — so a caller's output is stable run to run.
//
// A reference is trimmed of surrounding whitespace, and one that is then empty
// is dropped: a coverage entry qualified with a level but no `spec:`, say,
// names no target at all.
func (f *Frontmatter) Refs() []Ref {
	if f == nil {
		return nil
	}
	var out []Ref
	add := func(anchor, rel, ref string, cov *Coverage) {
		if ref = strings.TrimSpace(ref); ref != "" {
			out = append(out, Ref{SrcAnchor: anchor, Rel: rel, Ref: ref, Coverage: cov})
		}
	}
	// covers reads the retired `implements` spelling too (026 §5.1).
	for _, entry := range f.CoverageEntries() {
		add("", "covers", entry.Spec, &entry)
	}
	for _, r := range refListRelOrder {
		for _, ref := range r.get(f) {
			add("", r.rel, ref, nil)
		}
	}
	add("", "wasDerivedFrom", f.WasDerivedFrom, nil)
	for _, r := range anchorRelOrder {
		m := r.get(f)
		for _, k := range slices.Sorted(maps.Keys(m)) {
			anchor := anchorMapSrcAnchor(k)
			for _, ref := range m[k] {
				add(anchor, r.rel, ref, nil)
			}
		}
	}
	return out
}

// RefsFor is Refs narrowed to the given relations, in Refs order.
func (f *Frontmatter) RefsFor(rels ...string) []Ref {
	var out []Ref
	for _, r := range f.Refs() {
		if slices.Contains(rels, r.Rel) {
			out = append(out, r)
		}
	}
	return out
}

// anchorMapSrcAnchor converts an AnchorMap key to a SectionMeta-shaped
// anchor: "." (document-level) is "", "#sec-3" is "sec-3".
func anchorMapSrcAnchor(key string) string {
	if key == "." {
		return ""
	}
	return strings.TrimPrefix(key, "#")
}
