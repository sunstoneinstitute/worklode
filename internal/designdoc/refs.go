package designdoc

import (
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
	// without its leading '#'. Every relation Refs walks is document-level,
	// so it is "" today.
	SrcAnchor string
	// Rel is the relation asserted, as an ontology property local name
	// ("covers", "requires", …).
	Rel string
	// Ref is the reference text as authored, trailing "#sec-…" fragment
	// included — split it with SplitFragment.
	Ref string
	// Coverage is the covers entry this reference came from, non-nil only
	// when Rel is "covers": that relation alone carries a level and, for a
	// partial entry, its fullCoverageWith closure (026 §5.1). It is a copy,
	// so writing through it does not reach the frontmatter.
	Coverage *Coverage
	// Deferral is the defers entry this reference came from, non-nil only
	// when Rel is "defers": that relation alone carries a named owner
	// (026 §5.3). It is a copy, so writing through it does not reach the
	// frontmatter.
	Deferral *Deferral
}

// ActingRels is the acting-direction relation set: the spellings that assert a
// relation rather than restate its inverse. A consumer recording one row per
// fact keeps these and drops the rest — writing both directions would double
// every edge and let the two disagree (025 §14).
var ActingRels = []string{"covers", "defers", "requires", "blocks", "wasDerivedFrom"}

// StoredRels is what a consumer recording (or reporting) the rows a
// frontmatter writes actually reads: ActingRels plus `blockedBy`, the one
// inverse spelling that is not merely a restatement. It writes no row of its
// own — it writes the *same* `blocks` row with its two ends swapped, so plan
// ordering can be authored from either end (025 §5). Still one row, still one
// direction stored; only who typed it moves.
var StoredRels = append(slices.Clone(ActingRels), "blockedBy")

// InverseOf maps each inverse-only spelling — the ones StoredRels excludes
// because they merely restate an acting relation the other end is expected
// to declare (025 §14.2) — to the acting relation it restates. `blockedBy`
// is not here: unlike these, it writes a real row of its own
// (StoredRels), rather than depending on the other end declaring anything.
//
// A consumer checking "did the other end actually declare this back" reads
// this map once rather than special-casing keys (WL-375); an inverse-only
// field added later (refListRelOrder growing a row) is added here in the same
// change, and that check inherits it.
var InverseOf = map[string]string{
	"isRequiredBy": "requires",
}

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

// Refs enumerates every reference the frontmatter declares, in a deterministic
// order — coverage, the dependency lists, then provenance — so a caller's
// output is stable run to run. The retired amendment and supersession keys
// (RetiredRelKeys) are never walked: a stored body may still carry them, and
// they mean nothing (WL-SPEC-77 §7).
//
// A reference is trimmed of surrounding whitespace, and one that is then empty
// is dropped: a coverage entry qualified with a level but no `spec:`, say,
// names no target at all.
func (f *Frontmatter) Refs() []Ref {
	if f == nil {
		return nil
	}
	var out []Ref
	add := func(anchor, rel, ref string, cov *Coverage, def *Deferral) {
		if ref = strings.TrimSpace(ref); ref != "" {
			out = append(out, Ref{SrcAnchor: anchor, Rel: rel, Ref: ref, Coverage: cov, Deferral: def})
		}
	}
	// covers reads the retired `implements` spelling too (026 §5.1).
	for _, entry := range f.CoverageEntries() {
		add("", "covers", entry.Spec, &entry, nil)
	}
	// defers, like covers, carries its qualifier (here the owner) with the
	// reference rather than as a separate field (026 §5.3).
	for _, entry := range f.Defers {
		add("", "defers", entry.Spec, nil, &entry)
	}
	for _, r := range refListRelOrder {
		for _, ref := range r.get(f) {
			add("", r.rel, ref, nil, nil)
		}
	}
	add("", "wasDerivedFrom", f.WasDerivedFrom, nil, nil)
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
