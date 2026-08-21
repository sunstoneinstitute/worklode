package cmd

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// resolveDocRef resolves ref against docs — the documents the backbone
// serves — and returns the one it names plus the section its "#sec-..."
// fragment narrowed to (026 §3, 025 §14.3). projectKey is the current repo's
// key ("WL"), or "" when unknown.
//
// It is a pure function so the whole ref grammar is table-testable without a
// server. Forms are tried in the order 026 §3 documents:
//
//  1. a path — matched by its basename, which is the document's slug;
//  2. a document number, optionally with slug text after it;
//  3. the <KEY>-<TYPE>-<n> shorthand, whose <TYPE> token is kind-checked.
//
// A form that matches nothing falls through to the next, which is how a bare
// filename ("014-foo.md") still resolves by number. Plans are never
// candidates: `lode show` renders specs and ADRs only.
func resolveDocRef(docs []model.Doc, projectKey, ref string) (model.Doc, string, error) {
	base, section := designdoc.SplitFragment(ref)
	candidates := showableDocs(docs)

	// Form 1: path. The corpus filename a path names is the slug, so an
	// exact slug match comes first, then a prefix match.
	if looksLikePath(base) {
		base = strings.TrimSuffix(path.Base(base), ".md")
		if m := matchSlug(candidates, base); len(m) > 0 {
			return finishDocRef(ref, m, section)
		}
		if m := matchSlugPrefix(candidates, base); len(m) > 0 {
			return finishDocRef(ref, m, section)
		}
	}

	// Form 2: document number / slug prefix.
	if nf, ok := designdoc.ParseNumberForm(base); ok {
		if nf.Number == 0 {
			return model.Doc{}, "", designdoc.NoSpecError(ref)
		}
		matches := matchNumber(candidates, nf.Number)
		if nf.Rest != "" {
			matches = append(matches, matchSlugPrefix(candidates, base)...)
		}
		return finishDocRef(ref, matches, section)
	}

	// Form 3: shorthand.
	if base == "NO-SPEC" {
		return model.Doc{}, "", designdoc.NoSpecError(ref)
	}
	if sh, ok := designdoc.ParseShorthand(base); ok {
		if sh.Type == "SPEC" && sh.Number == 0 {
			return model.Doc{}, "", designdoc.NoSpecError(ref)
		}
		if projectKey == "" || sh.Key != projectKey {
			return model.Doc{}, "", &designdoc.UnresolvedError{Key: sh.Key}
		}
		doc, sec, err := finishDocRef(ref, matchNumber(candidates, sh.Number), section)
		if err != nil {
			return model.Doc{}, "", err
		}
		if err := checkDocKind(doc, sh.Type); err != nil {
			return model.Doc{}, "", err
		}
		return doc, sec, nil
	}

	return model.Doc{}, "", notFoundRefError(ref)
}

// checkDocKind enforces 025 §14.3's <TYPE> token ("SPEC" or "ADR") against
// the document's own kind: a document is an ADR iff Kind is "adr" (026 §4.2).
// It is the one implementation of the mismatch rule — the shorthand form
// calls it, and so does runDocShow for the --spec/--adr flags, which reach a
// document through the number form and would otherwise go unchecked.
func checkDocKind(doc model.Doc, typ string) error {
	isADR := doc.Kind == "adr"
	switch {
	case typ == "ADR" && !isADR:
		return &designdoc.KindMismatchError{Doc: doc.Slug, Want: "adr", Got: "spec"}
	case typ == "SPEC" && isADR:
		return &designdoc.KindMismatchError{Doc: doc.Slug, Want: "spec", Got: "adr"}
	}
	return nil
}

// notFoundRefError is 026 §4.2's tier-1 miss: a plain, named error.
func notFoundRefError(ref string) error {
	return fmt.Errorf("ref %q names no spec or ADR in the backbone", ref)
}

// looksLikePath reports whether base is shaped like ref form 1: it names a
// directory somewhere (contains '/') or names a markdown file directly.
func looksLikePath(base string) bool {
	return strings.Contains(base, "/") || strings.HasSuffix(base, ".md")
}

// filterDocs returns every document keep accepts. Each ref form narrows the
// candidate set on a one-line predicate; this is the single loop they share.
func filterDocs(docs []model.Doc, keep func(model.Doc) bool) []model.Doc {
	var matches []model.Doc
	for _, d := range docs {
		if keep(d) {
			matches = append(matches, d)
		}
	}
	return matches
}

// showableDocs drops the documents `lode show` cannot render: plans, which
// carry no number and no sections (025 §9).
func showableDocs(docs []model.Doc) []model.Doc {
	return filterDocs(docs, func(d model.Doc) bool { return d.Kind != "plan" })
}

// matchNumber returns every document whose corpus number equals n.
func matchNumber(docs []model.Doc, n int) []model.Doc {
	return filterDocs(docs, func(d model.Doc) bool { return d.Number == n })
}

// matchSlug returns every document whose slug is exactly slug.
func matchSlug(docs []model.Doc, slug string) []model.Doc {
	return filterDocs(docs, func(d model.Doc) bool { return d.Slug == slug })
}

// matchSlugPrefix returns every document whose slug starts with prefix.
func matchSlugPrefix(docs []model.Doc, prefix string) []model.Doc {
	return filterDocs(docs, func(d model.Doc) bool { return strings.HasPrefix(d.Slug, prefix) })
}

// finishDocRef turns a form's candidates into a result: none is a tier-1
// miss, more than one is ambiguous, and exactly one resolves. Candidates are
// deduped by id — a document can match through both criteria of the number
// form — and sorted by slug so an AmbiguousRefError reads the same every run.
func finishDocRef(ref string, matches []model.Doc, section string) (model.Doc, string, error) {
	matches = uniqueDocs(matches)
	switch len(matches) {
	case 0:
		return model.Doc{}, "", notFoundRefError(ref)
	case 1:
		return matches[0], section, nil
	default:
		slugs := make([]string, len(matches))
		for i, d := range matches {
			slugs[i] = d.Slug
		}
		return model.Doc{}, "", &designdoc.AmbiguousRefError{Ref: ref, Candidates: slugs}
	}
}

// uniqueDocs dedupes by document id and sorts by slug.
func uniqueDocs(in []model.Doc) []model.Doc {
	seen := make(map[int64]bool, len(in))
	out := make([]model.Doc, 0, len(in))
	for _, d := range in {
		if !seen[d.ID] {
			seen[d.ID] = true
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b model.Doc) int { return cmp.Compare(a.Slug, b.Slug) })
	return out
}
