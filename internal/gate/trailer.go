package gate

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// NoneReasons is 11 §4's closed list of "how" changes a Spec: none trailer
// may cite. "fix" is in the list but is refused without a cited ref.
var NoneReasons = []string{"fix", "refactor", "perf", "copy", "tests", "build", "config"}

// ErrNoTrailer is returned by Find when no line starts with the key.
var ErrNoTrailer = errors.New("no trailer")

// Declaration is one parsed trailer line (11 §4). Exactly one of Rule,
// Section and None is set.
type Declaration struct {
	Rule      *designdoc.RuleRef    // Spec: WL-RULE-456
	Section   *designdoc.SectionRef // Spec: WL-SPEC-4 sec-5 (transitional, S52)
	None      string                // Spec: none <reason>: the reason
	Qualifier string                // "", "amended", or a NoneReasons word on a cited ref
	Line      string                // the line as written, trimmed
}

// String renders the declaration in its canonical trailer form, without the key.
func (d Declaration) String() string {
	var ref string
	switch {
	case d.None != "":
		return "none " + d.None
	case d.Rule != nil:
		ref = fmt.Sprintf("%s-RULE-%d", d.Rule.Key, d.Rule.Number)
	case d.Section != nil:
		ref = fmt.Sprintf("%s-%s-%d %s", d.Section.Shorthand.Key, d.Section.Shorthand.Type, d.Section.Shorthand.Number, d.Section.Anchor)
	}
	if d.Qualifier != "" {
		ref += " " + d.Qualifier
	}
	return ref
}

// Find scans text line by line and parses the first line that starts with
// key. A later Spec: line is ignored: one declaration per body (11 §4).
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
		return d, true, fmt.Errorf("%q names nothing: a rule ref, a section ref, or none <reason>", line)
	}
	if fields[0] == "none" {
		if len(fields) != 2 || !slices.Contains(NoneReasons, fields[1]) {
			return d, true, fmt.Errorf("%q: none takes one reason from %s", line, strings.Join(NoneReasons, ", "))
		}
		if fields[1] == "fix" {
			return d, true, fmt.Errorf("%q: a fix cites the section it restores (11 §4)", line)
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
		return d, true, fmt.Errorf("%q: %q is not a rule ref, a section ref or none", line, fields[0])
	}
	fields = fields[1:]
	if !hasAnchor {
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "sec-") {
			return d, true, fmt.Errorf("%q: a section ref needs its anchor, %s-%s-%d sec-N", line, sh.Key, sh.Type, sh.Number)
		}
		anchor, fields = fields[0], fields[1:]
	}
	d.Section = &designdoc.SectionRef{Shorthand: sh, Anchor: anchor}
	return qualified(d, fields)
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
// commit messages newest first.
type Input struct {
	Changed []string
	Texts   []string
}

// Verdict is a passing check: which guarded paths changed and, when any
// did, the declaration that covered them.
type Verdict struct {
	Guarded     []string
	Declaration *Declaration
}

// Check applies 11 §3 and §4 offline: no guarded path changed, or a valid
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
		return v, fmt.Errorf("guarded paths changed (%s) and no %s trailer names the design rule this change makes true (11 §4)",
			strings.Join(v.Guarded, ", "), cfg.Trailer)
	case err != nil:
		return v, fmt.Errorf("guarded paths changed (%s): %w", strings.Join(v.Guarded, ", "), err)
	}
	v.Declaration = &d
	return v, nil
}
