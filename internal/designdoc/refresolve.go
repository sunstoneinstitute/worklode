package designdoc

// refresolve.go is the pure document-reference resolver `lode show` and the
// cockpit's /docs/ref/ redirect share (WL-129, WL-301): the 026 §3 ref
// grammar matched against the backbone's document rows. Moved here from
// internal/cmd so both the CLI and internal/api resolve one grammar.

import (
	"cmp"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ResolveRef resolves ref against docs — the documents the backbone
// serves — and returns the one it names plus the section its "#sec-..."
// fragment narrowed to (026 §3, 025 §14.3). projectKey is the current repo's
// key ("WL"), or "" when unknown.
//
// It is a pure function so the whole ref grammar is table-testable without a
// server. Forms are tried in the order 026 §3 documents:
//
//  1. a path — matched by its basename, which is the document's slug;
//  2. a document number, optionally with slug text after it;
//  3. a bare slug — the same name `lode doc get` and the other doc verbs
//     resolve, so the two readers cannot disagree about what a name means;
//  4. the <KEY>-<TYPE>-<n> shorthand, whose <TYPE> token is kind-checked.
//
// A form that matches nothing falls through to the next, which is how a bare
// filename ("014-foo.md") still resolves by number. Plans are never
// candidates: `lode show` renders specs and ADRs only.
func ResolveRef(docs []model.Doc, projectKey, ref string) (model.Doc, string, error) {
	base, section := SplitFragment(ref)
	candidates := showableRefDocs(docs)

	// Form 1: path. The corpus filename a path names is the slug, so an
	// exact slug match comes first, then a prefix match.
	if LooksLikePath(base) {
		base = strings.TrimSuffix(path.Base(base), ".md")
		if m := matchRefSlug(candidates, base); len(m) > 0 {
			return finishRef(ref, m, section)
		}
		if m := matchRefSlugPrefix(candidates, base); len(m) > 0 {
			return finishRef(ref, m, section)
		}
	}

	// Form 2: document number / slug prefix.
	if nf, ok := ParseNumberForm(base); ok {
		if nf.Number == 0 {
			return model.Doc{}, "", NoSpecError(ref)
		}
		matches := matchRefNumber(candidates, nf.Number)
		if nf.Rest != "" {
			matches = append(matches, matchRefSlugPrefix(candidates, base)...)
		}
		return finishRef(ref, matches, section)
	}

	// Form 3: bare slug. Digit-leading refs never reach here — the number
	// form already returned — so this is the letter-leading names the
	// backbone mints (`lode doc new --slug`). Exact match first, so a slug
	// that is a prefix of another still names itself.
	if slugFormRef.MatchString(base) {
		if m := matchRefSlug(candidates, base); len(m) > 0 {
			return finishRef(ref, m, section)
		}
		if m := matchRefSlugPrefix(candidates, base); len(m) > 0 {
			return finishRef(ref, m, section)
		}
	}

	// Form 4: shorthand.
	if base == "NO-SPEC" {
		return model.Doc{}, "", NoSpecError(ref)
	}
	if sh, ok := ParseShorthand(base); ok {
		if sh.Type == "SPEC" && sh.Number == 0 {
			return model.Doc{}, "", NoSpecError(ref)
		}
		if projectKey == "" || sh.Key != projectKey {
			return model.Doc{}, "", &UnresolvedError{Key: sh.Key}
		}
		doc, sec, err := finishRef(ref, matchRefNumber(candidates, sh.Number), section)
		if err != nil {
			return model.Doc{}, "", err
		}
		if err := CheckDocKind(doc, sh.Type); err != nil {
			return model.Doc{}, "", err
		}
		return doc, sec, nil
	}

	return model.Doc{}, "", NotFoundRefError(ref)
}

// CheckDocKind enforces 025 §14.3's <TYPE> token ("SPEC" or "ADR") against
// the document's own kind: a document is an ADR iff Kind is "adr" (026 §4.2).
// It is the one implementation of the mismatch rule — the shorthand form
// calls it, and so does runDocShow for the --spec/--adr flags, which reach a
// document through the number form and would otherwise go unchecked.
func CheckDocKind(doc model.Doc, typ string) error {
	isADR := doc.Kind == "adr"
	switch {
	case typ == "ADR" && !isADR:
		return &KindMismatchError{Doc: doc.Slug, Want: "adr", Got: "spec"}
	case typ == "SPEC" && isADR:
		return &KindMismatchError{Doc: doc.Slug, Want: "spec", Got: "adr"}
	}
	return nil
}

// NotFoundRefError is 026 §4.2's tier-1 miss: a plain, named error.
func NotFoundRefError(ref string) error {
	return fmt.Errorf("ref %q names no spec or ADR in the backbone", ref)
}

// LooksLikePath reports whether base is shaped like ref form 1: it names a
// directory somewhere (contains '/') or names a markdown file directly.
func LooksLikePath(base string) bool {
	return strings.Contains(base, "/") || strings.HasSuffix(base, ".md")
}

// slugFormRef is ref form 3's shape: a letter-leading document slug. It is
// deliberately looser than the slug grammar `lode doc new` enforces — a
// near-miss should resolve to nothing, not fall through to the shorthand
// form's grammar error.
var slugFormRef = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// filterRefDocs returns every document keep accepts. Each ref form narrows the
// candidate set on a one-line predicate; this is the single loop they share.
func filterRefDocs(docs []model.Doc, keep func(model.Doc) bool) []model.Doc {
	var matches []model.Doc
	for _, d := range docs {
		if keep(d) {
			matches = append(matches, d)
		}
	}
	return matches
}

// showableRefDocs drops the documents `lode show` cannot render: plans, which
// carry no number and no sections (025 §9).
func showableRefDocs(docs []model.Doc) []model.Doc {
	return filterRefDocs(docs, func(d model.Doc) bool { return d.Kind != "plan" })
}

// matchRefNumber returns every document whose corpus number equals n.
func matchRefNumber(docs []model.Doc, n int) []model.Doc {
	return filterRefDocs(docs, func(d model.Doc) bool { return d.Number == n })
}

// matchRefSlug returns every document whose slug is exactly slug.
func matchRefSlug(docs []model.Doc, slug string) []model.Doc {
	return filterRefDocs(docs, func(d model.Doc) bool { return d.Slug == slug })
}

// matchRefSlugPrefix returns every document whose slug starts with prefix.
func matchRefSlugPrefix(docs []model.Doc, prefix string) []model.Doc {
	return filterRefDocs(docs, func(d model.Doc) bool { return strings.HasPrefix(d.Slug, prefix) })
}

// finishRef turns a form's candidates into a result: none is a tier-1
// miss, more than one is ambiguous, and exactly one resolves. Candidates are
// deduped by id — a document can match through both criteria of the number
// form — and sorted by slug so an AmbiguousRefError reads the same every run.
func finishRef(ref string, matches []model.Doc, section string) (model.Doc, string, error) {
	matches = uniqueRefDocs(matches)
	switch len(matches) {
	case 0:
		return model.Doc{}, "", NotFoundRefError(ref)
	case 1:
		return matches[0], section, nil
	default:
		slugs := make([]string, len(matches))
		for i, d := range matches {
			slugs[i] = d.Slug
		}
		return model.Doc{}, "", &AmbiguousRefError{Ref: ref, Candidates: slugs}
	}
}

// uniqueRefDocs dedupes by document id and sorts by slug.
func uniqueRefDocs(in []model.Doc) []model.Doc {
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
