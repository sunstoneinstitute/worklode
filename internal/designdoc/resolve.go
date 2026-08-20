package designdoc

import (
	"errors"
	"fmt"
	"strings"
)

// This file holds the vocabulary of document-reference resolution: the
// fragment split every ref form allows, and the errors a resolver reports.
// Resolution itself lives in internal/cmd, which matches a ref against the
// documents the backbone serves (spec 026 §3, 025 §14.3) rather than against
// files on disk.

// SplitFragment separates a trailing "#sec-..." fragment from ref, per 026
// §4's "narrows any of them to an anchor". base is ref with the fragment (and
// its '#') removed; section is the fragment with the '#' stripped, or "" when
// ref carried none.
func SplitFragment(ref string) (base, section string) {
	base, section, found := strings.Cut(ref, "#")
	if !found {
		return ref, ""
	}
	return base, section
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
