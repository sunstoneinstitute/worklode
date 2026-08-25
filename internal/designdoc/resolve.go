package designdoc

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file holds the vocabulary of document-reference resolution: the
// fragment split every ref form allows, the grammar of the forms themselves,
// and the errors a resolver reports. Resolution itself has two callers with
// different corpora to match against — internal/cmd against the documents the
// backbone serves, internal/store against the rows it holds (spec 026 §3, 025
// §14.3) — so the grammar lives here, once, and each of them applies it.

// SplitFragment separates a trailing "#sec-..." fragment from ref, per 026
// §4's "narrows any of them to an anchor". base is ref with the fragment (and
// its '#') removed; section is the fragment with the '#' stripped, or "" when
// ref carried none.
func SplitFragment(ref string) (base, section string) {
	base, section, _ = strings.Cut(ref, "#")
	return base, section
}

// shorthandPattern is 025 §14.3's <KEY>-<TYPE>-<n> grammar, and
// numberFormPattern is 026 §3's form 2: a document number, with or without
// zero-padding, optionally followed by the rest of a slug. Both are anchored
// end to end and expect a base — a ref with any fragment already removed.
var (
	shorthandPattern  = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-(SPEC|ADR|PLAN)-(\d+)$`)
	numberFormPattern = regexp.MustCompile(`^(\d+)(-.*)?$`)
)

// Shorthand is a parsed <KEY>-<TYPE>-<n> reference, e.g. "WL-SPEC-25".
// Every document kind has one: 025 §14.3 gave plans none, and 029 §4 put them
// on their project's sequence like every other kind.
type Shorthand struct {
	Key    string // the project key the number is scoped to, e.g. "WL"
	Type   string // "SPEC", "ADR" or "PLAN", as written
	Number int
}

// Kind is the document kind Type names, in the spelling documents declare:
// "spec" or "adr".
func (s Shorthand) Kind() string {
	return strings.ToLower(s.Type)
}

// ParseShorthand parses base as the 025 §14.3 shorthand. It reports false when
// base is some other ref form, which includes 026 §4.3's NO-SPEC sentinel:
// that names no document, so it is the caller's case to answer, not a
// shorthand with an absent number.
func ParseShorthand(base string) (Shorthand, bool) {
	m := shorthandPattern.FindStringSubmatch(base)
	if m == nil {
		return Shorthand{}, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return Shorthand{}, false // a number too large to be a document number
	}
	return Shorthand{Key: m[1], Type: m[2], Number: n}, true
}

// NumberForm is a parsed ref form 2: a corpus number and whatever slug text
// followed it.
type NumberForm struct {
	Number int
	Rest   string // slug text after the number ("-design-documents"), "" when base was bare
}

// ParseNumberForm parses base as 026 §3's number form. A caller that resolves
// only *bare* numbers checks Rest == "": a number-prefixed reference is a
// filename, and matching "025-documents-2.md" to spec 025 on the shared prefix
// would name the wrong document rather than none.
func ParseNumberForm(base string) (NumberForm, bool) {
	m := numberFormPattern.FindStringSubmatch(base)
	if m == nil {
		return NumberForm{}, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return NumberForm{}, false // a number too large to be a document number
	}
	return NumberForm{Number: n, Rest: m[2]}, true
}

// AmbiguousRefError reports a ref that matched more than one document. Error
// lists every candidate slug, one per line, so a caller printing it as-is
// gives the reader enough to disambiguate.
type AmbiguousRefError struct {
	Ref        string
	Candidates []string
}

func (e *AmbiguousRefError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ambiguous ref %q:", e.Ref)
	for _, c := range e.Candidates {
		b.WriteByte('\n')
		b.WriteString(c)
	}
	return b.String()
}

// UnresolvedError reports a shorthand ref naming a project this checkout has
// no way to reach (026 §4.2 tier 3). It is deliberately not a defect: nothing
// in the referring repository can repair it.
type UnresolvedError struct {
	Key string
}

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("unresolved: project %s not known here", e.Key)
}

// KindMismatchError reports a ref whose <TYPE> token names one document kind
// (spec or adr) while the document it resolved to is the other (026 §4.2).
// Doc names the target — its slug, the identity a reader can look up.
type KindMismatchError struct {
	Doc  string
	Want string // the kind the ref's <TYPE> token asked for
	Got  string // the kind the document declares
}

func (e *KindMismatchError) Error() string {
	return fmt.Sprintf("%s: ref names %s, document is %s", e.Doc, kindArticle(e.Want), kindArticle(e.Got))
}

// kindArticle renders a kind ("adr" or "spec") with its article and display
// casing: "an ADR" or "a spec".
func kindArticle(kind string) string {
	if kind == "adr" {
		return "an ADR"
	}
	return "a " + kind
}

// ErrNoSpec is returned for the NO-SPEC sentinel ref, or its equivalent
// <KEY>-SPEC-0 (026 §4.3): the ref explicitly means "no governing spec",
// never a document, so the tier table of §4.2 never runs.
var ErrNoSpec = errors.New("no governing spec")

// NoSpecError wraps ErrNoSpec with the ref that triggered it, so a caller
// printing the error as-is gets a self-explanatory message rather than the
// bare sentinel text. errors.Is(err, ErrNoSpec) still holds through the wrap.
func NoSpecError(ref string) error {
	return fmt.Errorf("%s is the no-governing-spec sentinel (026 §4.3), not a document: %w", ref, ErrNoSpec)
}
