package gate

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// NoneReasons is WL-REQ-7's closed list of "how" changes a Spec: none trailer
// may cite. "fix" is in the list but is refused without a cited ref.
var NoneReasons = []string{"fix", "refactor", "perf", "copy", "tests", "build", "config"}

// ErrNoTrailer is returned by Find when no line starts with the key.
var ErrNoTrailer = errors.New("no trailer")

// Declaration is one parsed trailer line (WL-REQ-7). Exactly one of Rule and
// None is set.
type Declaration struct {
	Rule      *designdoc.RuleRef // Spec: WL-REQ-456
	None      string             // Spec: none <reason>: the reason
	Qualifier string             // "", "amended", or a NoneReasons word on a cited ref
	Line      string             // the line as written, trimmed
}

// SectionError is the refusal of a section value (WL-REQ-7): section numbers
// change with a spec's arrangement, a rule ref does not. Rule is the ref the
// section resolves to when a caller could ask the server; the message falls
// back to the command that reads it.
type SectionError struct {
	Line    string
	Section designdoc.SectionRef
	Rule    string
}

func (e *SectionError) Error() string {
	sh := e.Section.Shorthand
	if e.Rule != "" {
		return fmt.Sprintf("%q names a section; cite the rule instead: Spec: %s", e.Line, e.Rule)
	}
	return fmt.Sprintf("%q names a section; cite its rule ref instead, read it with `lode show %s-%s-%d#%s`", e.Line, sh.Key, sh.Type, sh.Number, e.Section.Anchor)
}

// String renders the declaration in its canonical trailer form, without the
// key. The gate runs offline and cannot see a rule's kind, so a rule ref
// prints as FormatRuleRef does for an unknown kind; the form doubles as the
// reconciler's idempotency key, which a kind change must not move.
func (d Declaration) String() string {
	if d.None != "" {
		return "none " + d.None
	}
	ref := designdoc.FormatRuleRef(d.Rule.Key, d.Rule.Number, "")
	if d.Qualifier != "" {
		ref += " " + d.Qualifier
	}
	return ref
}

// Find scans text line by line and parses the first line that starts with
// key. A later Spec: line is ignored: one declaration per body (WL-REQ-7).
func Find(key, text string) (Declaration, error) {
	for _, line := range strings.Split(text, "\n") {
		d, matched, err := ParseLine(key, line)
		if !matched {
			continue
		}
		return d, err
	}
	return Declaration{}, ErrNoTrailer
}

// ParseLine parses one line. matched reports whether the line starts with
// key followed by a space or the end of the line; err is set when it does
// and the rest is not a valid declaration.
func ParseLine(key, line string) (d Declaration, matched bool, err error) {
	line = strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(line, key)
	if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
		return Declaration{}, false, nil
	}
	d.Line = line
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return d, true, fmt.Errorf("%q names nothing: a rule ref (WL-REQ-<n>) or none <reason>", line)
	}
	if fields[0] == "none" {
		if len(fields) != 2 || !slices.Contains(NoneReasons, fields[1]) {
			return d, true, fmt.Errorf("%q: none takes one reason from %s", line, strings.Join(NoneReasons, ", "))
		}
		if fields[1] == "fix" {
			return d, true, fmt.Errorf("%q: a fix cites the rule it restores, Spec: WL-REQ-<n> fix (WL-REQ-7)", line)
		}
		d.None = fields[1]
		return d, true, nil
	}
	if c, ok := designdoc.ParseRuleRef(fields[0]); ok {
		d.Rule = &c
		return qualified(d, fields[1:])
	}
	base, anchor, hasAnchor := strings.Cut(fields[0], "#")
	sh, ok := designdoc.ParseShorthand(base)
	if !ok {
		return d, true, fmt.Errorf("%q: %q is not a rule ref (WL-REQ-<n>) or none", line, fields[0])
	}
	if !hasAnchor && len(fields) > 1 && strings.HasPrefix(fields[1], "sec-") {
		anchor, hasAnchor = fields[1], true
	}
	if !hasAnchor {
		return d, true, fmt.Errorf("%q: %s names a document, not a rule: cite a rule ref (WL-REQ-<n>)", line, base)
	}
	return d, true, &SectionError{Line: line, Section: designdoc.SectionRef{Shorthand: sh, Anchor: anchor}}
}

// qualified attaches the optional qualifier after a cited ref: "amended", or
// a NoneReasons word such as "fix".
func qualified(d Declaration, fields []string) (Declaration, bool, error) {
	switch {
	case len(fields) == 0:
		return d, true, nil
	case len(fields) == 1 && (fields[0] == "amended" || slices.Contains(NoneReasons, fields[0])):
		d.Qualifier = fields[0]
		return d, true, nil
	}
	return d, true, fmt.Errorf("%q: after the ref only amended or one of %s may follow", d.Line, strings.Join(NoneReasons, ", "))
}

// Input is what the gate decides on: the repo-relative paths a change
// touches, and the texts that may carry the trailer, PR body first, then
// commit messages newest first. ResolveSection, when set, names the rule
// ref a refused section value resolves to, or "" when it cannot.
type Input struct {
	Changed        []string
	Texts          []string
	ResolveSection func(designdoc.SectionRef) string
}

// Verdict is a passing check: which guarded paths changed and, when any
// did, the declaration that covered them.
type Verdict struct {
	Guarded     []string
	Declaration *Declaration
}

// Check applies WL-REQ-8 offline: no guarded path changed, or a valid
// trailer is present. The error names the guarded paths and the reason.
func Check(cfg Config, in Input) (Verdict, error) {
	g, err := cfg.Guards()
	if err != nil {
		return Verdict{}, err
	}
	var v Verdict
	for _, p := range in.Changed {
		if g.Match(p) {
			v.Guarded = append(v.Guarded, p)
		}
	}
	if len(v.Guarded) == 0 {
		return v, nil
	}
	d, err := Find(cfg.Trailer, strings.Join(in.Texts, "\n"))
	switch {
	case errors.Is(err, ErrNoTrailer):
		return v, fmt.Errorf("guarded paths changed (%s) and no %s trailer names the rule this change makes true (WL-REQ-7)",
			strings.Join(v.Guarded, ", "), cfg.Trailer)
	case err != nil:
		var se *SectionError
		if errors.As(err, &se) && in.ResolveSection != nil {
			se.Rule = in.ResolveSection(se.Section)
		}
		return v, fmt.Errorf("guarded paths changed (%s): %w", strings.Join(v.Guarded, ", "), err)
	}
	v.Declaration = &d
	return v, nil
}
