package cmd

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// numberFormPattern recognizes ref form 2 (026 §3): a bare document number,
// with or without zero-padding, optionally followed by more of the slug
// ("14", "014", "014-design-documents").
var numberFormPattern = regexp.MustCompile(`^(\d+)(-.*)?$`)

// shorthandPattern is 025 §14.3's <KEY>-<TYPE>-<n> grammar, fragment already
// split off by designdoc.SplitFragment.
var shorthandPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-(SPEC|ADR)-(\d+)$`)

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
	if m := numberFormPattern.FindStringSubmatch(base); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return model.Doc{}, "", fmt.Errorf("ref %q: %w", ref, err)
		}
		if n == 0 {
			return model.Doc{}, "", designdoc.NoSpecError(ref)
		}
		matches := matchNumber(candidates, n)
		if strings.Contains(base, "-") {
			matches = append(matches, matchSlugPrefix(candidates, base)...)
		}
		return finishDocRef(ref, matches, section)
	}

	// Form 3: shorthand.
	if base == "NO-SPEC" {
		return model.Doc{}, "", designdoc.NoSpecError(ref)
	}
	if m := shorthandPattern.FindStringSubmatch(base); m != nil {
		key, typ, numStr := m[1], m[2], m[3]
		num, err := strconv.Atoi(numStr)
		if err != nil {
			return model.Doc{}, "", fmt.Errorf("ref %q: %w", ref, err)
		}
		if typ == "SPEC" && num == 0 {
			return model.Doc{}, "", designdoc.NoSpecError(ref)
		}
		if projectKey == "" || key != projectKey {
			return model.Doc{}, "", &designdoc.UnresolvedError{Key: key}
		}
		doc, sec, err := finishDocRef(ref, matchNumber(candidates, num), section)
		if err != nil {
			return model.Doc{}, "", err
		}
		if err := checkDocKind(doc, typ); err != nil {
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

// showableDocs drops the documents `lode show` cannot render: plans, which
// carry no number and no sections (025 §9).
func showableDocs(docs []model.Doc) []model.Doc {
	out := make([]model.Doc, 0, len(docs))
	for _, d := range docs {
		if d.Kind != "plan" {
			out = append(out, d)
		}
	}
	return out
}

// matchNumber returns every document whose corpus number equals n.
func matchNumber(docs []model.Doc, n int) []model.Doc {
	var matches []model.Doc
	for _, d := range docs {
		if d.Number == n {
			matches = append(matches, d)
		}
	}
	return matches
}

// matchSlug returns every document whose slug is exactly slug.
func matchSlug(docs []model.Doc, slug string) []model.Doc {
	var matches []model.Doc
	for _, d := range docs {
		if d.Slug == slug {
			matches = append(matches, d)
		}
	}
	return matches
}

// matchSlugPrefix returns every document whose slug starts with prefix.
func matchSlugPrefix(docs []model.Doc, prefix string) []model.Doc {
	var matches []model.Doc
	for _, d := range docs {
		if strings.HasPrefix(d.Slug, prefix) {
			matches = append(matches, d)
		}
	}
	return matches
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
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}
