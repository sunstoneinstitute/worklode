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
// backbone serves, internal/store against the rows it holds (WL-REQ-1357) — so the grammar lives here, once, and each of them applies it.

// SplitFragment separates a trailing "#sec-..." fragment from ref, per 026
// §4's "narrows any of them to an anchor". base is ref with the fragment (and
// its '#') removed; section is the fragment with the '#' stripped, or "" when
// ref carried none.
func SplitFragment(ref string) (base, section string) {
	base, section, _ = strings.Cut(ref, "#")
	return base, section
}

// shorthandPattern is WL-REQ-168's <KEY>-<TYPE>-<n> grammar, and
// numberFormPattern is WL-REQ-192's form 2: a document number, with or without
// zero-padding, optionally followed by the rest of a slug. Both are anchored
// end to end and expect a base — a ref with any fragment already removed.
var (
	shorthandPattern  = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-(SPEC|ADR|PLAN)-(\d+)$`)
	numberFormPattern = regexp.MustCompile(`^(\d+)(-.*)?$`)
)

// Shorthand is a parsed <KEY>-<TYPE>-<n> reference, e.g. "WL-SPEC-77".
// Every document kind has one: WL-REQ-168 gave plans none, and WL-REQ-118 put them
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

// ParseShorthand parses base as the WL-REQ-168 shorthand. It reports false when
// base is some other ref form, which includes WL-REQ-198's NO-SPEC sentinel:
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

// ParseNumberForm parses base as WL-REQ-192's number form. A caller that resolves
// only *bare* numbers checks Rest == "": a number-prefixed reference is a
// filename, and matching "025-documents-2.md" to WL-SPEC-77 on the shared prefix
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
// lists every candidate's citable id (WL-REQ-168), one per line, so a caller
// printing it as-is hands the reader refs they can cite straight back.
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
// no way to reach (WL-REQ-198 tier 3). It is deliberately not a defect: nothing
// in the referring repository can repair it.
type UnresolvedError struct {
	Key string
}

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("unresolved: project %s not known here", e.Key)
}

// KindMismatchError reports a ref whose <TYPE> token names one document kind
// (spec or adr) while the document it resolved to is the other (WL-REQ-198).
// Doc names the target by its citable id (WL-REQ-168), the identity a reader
// cites back.
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

// RuleRefText is the rule arm of WL-REQ-168's <KEY>-<TYPE>-<n> grammar
// (12-spec-refactoring-design-tree.md S20, S64; WL-REQ-165): a design
// rule's citable ref. Every kind shares one counter, so any infix resolves
// any rule by its number: REQ, RULE, and CL, the pre-S64 spelling that
// accepted document text still carries. FormatRuleRef writes the kind's own.
const RuleRefText = `([A-Z][A-Z0-9]{1,9})-(?:REQ|RULE|CL)-(\d+)`

// The rule kinds (WL-REQ-165), ns.Schemes["RuleKind"]. The store mints a
// requirement by default. RuleKindInformative is retired and reads as a
// principle until its rows are reclassified (§19.7).
const (
	RuleKindRequirement = "requirement"
	RuleKindCatalogue   = "catalogue"
	RuleKindInvariant   = "invariant"
	RuleKindDefinition  = "definition"
	RuleKindPrinciple   = "principle"
	RuleKindInformative = "informative"
)

// RuleKinds are the kinds a rule may be given, in §4's order; the retired
// informative is not one of them.
var RuleKinds = []string{RuleKindRequirement, RuleKindCatalogue, RuleKindInvariant, RuleKindDefinition, RuleKindPrinciple}

// RuleKindCovered reports whether a plan covers rules of this kind: a
// requirement or a catalogue. Every other kind is never covered and never a
// gap (WL-REQ-165).
func RuleKindCovered(kind string) bool {
	return kind == RuleKindRequirement || kind == RuleKindCatalogue
}

// FormatRuleRef prints a rule's ref with the infix of its kind: REQ for a
// covered kind, RULE for any other (WL-REQ-165). Every printed rule ref
// goes through here, or through the store's SQL mirror of it.
func FormatRuleRef(key string, number int64, kind string) string {
	infix := "RULE"
	if RuleKindCovered(kind) {
		infix = "REQ"
	}
	return fmt.Sprintf("%s-%s-%d", key, infix, number)
}

var ruleRefPattern = regexp.MustCompile(`^` + RuleRefText + `$`)

// RuleRef is a parsed rule ref, e.g. "WL-REQ-12". It carries no kind: the
// number alone names the rule.
type RuleRef struct {
	Key    string
	Number int64
}

// ParseRuleRef parses base as a rule ref. It reports false for every
// other ref form, including document shorthand and a ref carrying a fragment.
func ParseRuleRef(base string) (RuleRef, bool) {
	m := ruleRefPattern.FindStringSubmatch(base)
	if m == nil {
		return RuleRef{}, false
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return RuleRef{}, false
	}
	return RuleRef{Key: m[1], Number: n}, true
}

// ruleRefInText and sectionRefInText are the ref grammars of
// ParseRuleRef and ParseShorthand loosened to find refs inside prose
// (S26). A section ref must carry an anchor: a bare document ref names an
// arrangement, and edges run between rules.
var (
	ruleRefInText    = regexp.MustCompile(`\b` + RuleRefText + `\b`)
	sectionRefInText = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9}-(?:SPEC|ADR|PLAN)-\d+)#(sec-[0-9A-Za-z._-]+)\b`)
)

// SectionRef is a document shorthand plus a section anchor found in prose.
type SectionRef struct {
	Shorthand Shorthand
	Anchor    string
}

// FindRuleRefs returns every distinct rule ref in text, in order of
// first appearance. WL-REQ-12, WL-RULE-12 and WL-CL-12 are the same rule.
func FindRuleRefs(text string) []RuleRef {
	var out []RuleRef
	seen := map[RuleRef]bool{}
	for _, m := range ruleRefInText.FindAllString(text, -1) {
		r, ok := ParseRuleRef(m)
		if !ok || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// FindSectionRefs returns every distinct anchored document ref in text, in
// order of first appearance.
func FindSectionRefs(text string) []SectionRef {
	var out []SectionRef
	seen := map[string]bool{}
	for _, m := range sectionRefInText.FindAllStringSubmatch(text, -1) {
		if seen[m[0]] {
			continue
		}
		seen[m[0]] = true
		if sh, ok := ParseShorthand(m[1]); ok {
			out = append(out, SectionRef{Shorthand: sh, Anchor: m[2]})
		}
	}
	return out
}

// ErrNoSpec is returned for the NO-SPEC sentinel ref, or its equivalent
// <KEY>-SPEC-0 (WL-REQ-198): the ref explicitly means "no governing spec",
// never a document, so the tier table of WL-REQ-204 never runs.
var ErrNoSpec = errors.New("no governing spec")

// NoSpecError wraps ErrNoSpec with the ref that triggered it, so a caller
// printing the error as-is gets a self-explanatory message rather than the
// bare sentinel text. errors.Is(err, ErrNoSpec) still holds through the wrap.
func NoSpecError(ref string) error {
	return fmt.Errorf("%s is the no-governing-spec sentinel (026 §4.3), not a document: %w", ref, ErrNoSpec)
}
