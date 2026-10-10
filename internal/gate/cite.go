package gate

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// The citation test of WL-REQ-1791: a spec section cited by number, which
// changes when the spec's arrangement does, where a rule ref does not.
// specSection is a spec ref followed by a section sign or a spaced sec- word,
// anchorSection is any #sec- fragment (with the document before it when there
// is one), bareSection is a section sign followed by a digit.
var (
	specSection   = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9}-SPEC-\d+)(?: §| sec-) ?([0-9][0-9A-Za-z.]*)?`)
	anchorSection = regexp.MustCompile(`(\b[A-Z][A-Z0-9]{1,9}-(?:SPEC|ADR|PLAN)-\d+)?#sec-([0-9][0-9A-Za-z.]*)?`)
	bareSection   = regexp.MustCompile(`§ ?([0-9][0-9A-Za-z.]*)`)
	ruleRefBefore = regexp.MustCompile(designdoc.RuleRefText + ` ?$`)
	urlInText     = regexp.MustCompile(`[a-z][a-z0-9+.-]*://\S+`)
)

// Citation is one match of the test. Section is set when the match names its
// document and section, so a resolver can name the rule arranged there. Where
// is the file and line for a match in a diff, empty otherwise.
type Citation struct {
	Text    string
	Section *designdoc.SectionRef
	Where   string
}

// Citations runs the test over prose: lines inside a fenced code block are
// skipped and URLs are cut out of a line before it is tested.
func Citations(text string) []Citation {
	var out []Citation
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		if isFence(line) {
			fenced = !fenced
			continue
		}
		if !fenced {
			out = append(out, lineCitations(line)...)
		}
	}
	return out
}

func isFence(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// lineCitations is the test over one line of prose, matches in order.
func lineCitations(line string) []Citation {
	line = urlInText.ReplaceAllString(line, "")
	type span struct {
		start, end int
		c          Citation
	}
	var spans []span
	covered := func(i int) bool {
		for _, s := range spans {
			if i >= s.start && i < s.end {
				return true
			}
		}
		return false
	}
	add := func(m []int, doc string, num string) {
		c := Citation{Text: strings.TrimRight(line[m[0]:m[1]], ".")}
		num = strings.TrimRight(num, ".")
		if sh, ok := designdoc.ParseShorthand(doc); ok && num != "" {
			c.Section = &designdoc.SectionRef{Shorthand: sh, Anchor: "sec-" + num}
		}
		spans = append(spans, span{m[0], m[1], c})
	}
	sub := func(m []int, i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return line[m[2*i]:m[2*i+1]]
	}
	for _, m := range specSection.FindAllStringSubmatchIndex(line, -1) {
		add(m, sub(m, 1), sub(m, 2))
	}
	for _, m := range anchorSection.FindAllStringSubmatchIndex(line, -1) {
		if !covered(m[0]) {
			add(m, sub(m, 1), sub(m, 2))
		}
	}
	for _, m := range bareSection.FindAllStringSubmatchIndex(line, -1) {
		if !covered(m[0]) && !ruleRefBefore.MatchString(line[:m[0]]) {
			add(m, "", "")
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	out := make([]Citation, len(spans))
	for i, s := range spans {
		out[i] = s.c
	}
	return out
}

// Describe renders each match with the rule ref to cite in its place:
// resolve names the rule arranged at a section, or "" when it cannot, and the
// line then names the `lode show` command that reads it.
func Describe(cs []Citation, resolve func(designdoc.SectionRef) string) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString("  ")
		if c.Where != "" {
			b.WriteString(c.Where + ": ")
		}
		fmt.Fprintf(&b, "%q cites a spec section; ", c.Text)
		switch {
		case c.Section == nil:
			b.WriteString("cite the rule ref, read it with `lode show <doc>#sec-N`")
		default:
			rule := ""
			if resolve != nil {
				rule = resolve(*c.Section)
			}
			sh := c.Section.Shorthand
			if rule != "" {
				b.WriteString("cite " + rule)
			} else {
				fmt.Fprintf(&b, "cite the rule ref, read it with `lode show %s-%s-%d#%s`", sh.Key, sh.Type, sh.Number, c.Section.Anchor)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// hunkHeader reads the new-file start line of a unified diff hunk.
var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// AddedCitations runs the test over the lines a unified diff adds, skipping
// files the [gate] exempt list names. In a Go file only a line's comment is
// prose, so string literals are never tested; elsewhere fenced code blocks
// are skipped, tracked over the hunk's context and added lines.
//
// ponytail: line-level, so a Go block comment or a fence opened outside a
// hunk's context is not seen; widen the diff context if that bites.
func (c Config) AddedCitations(diff string) ([]Citation, error) {
	exempt := make([]*regexp.Regexp, 0, len(c.Exempt))
	for _, p := range c.Exempt {
		exempt = append(exempt, globRegexp(p))
	}
	var (
		out     []Citation
		file    string
		skip    bool
		fenced  bool
		lineNum int
	)
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			skip = file == "/dev/null"
			for _, re := range exempt {
				skip = skip || re.MatchString(file)
			}
			continue
		case strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "diff --git "):
			continue
		case strings.HasPrefix(l, "@@"):
			m := hunkHeader.FindStringSubmatch(l)
			if m == nil {
				return nil, fmt.Errorf("unreadable hunk header %q", l)
			}
			lineNum, _ = strconv.Atoi(m[1])
			fenced = false
			continue
		}
		if l == "" || skip {
			continue
		}
		kind, text := l[0], l[1:]
		if kind == '-' || kind == '\\' {
			continue
		}
		n := lineNum
		lineNum++
		if path.Ext(file) == ".go" {
			if kind == '+' {
				out = appendWhere(out, lineCitations(goComment(text)), file, n)
			}
			continue
		}
		if isFence(text) {
			fenced = !fenced
			continue
		}
		if kind == '+' && !fenced {
			out = appendWhere(out, lineCitations(text), file, n)
		}
	}
	return out, nil
}

func appendWhere(out, cs []Citation, file string, n int) []Citation {
	for _, c := range cs {
		c.Where = fmt.Sprintf("%s:%d", file, n)
		out = append(out, c)
	}
	return out
}

// goComment returns the line comment of one line of Go source, skipping
// string, raw string and rune literals that open and close on the line.
func goComment(line string) string {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"', '\'', '`':
			q := line[i]
			for i++; i < len(line) && line[i] != q; i++ {
				if line[i] == '\\' && q != '`' {
					i++
				}
			}
		case '/':
			if i+1 < len(line) && line[i+1] == '/' {
				return line[i+2:]
			}
		}
	}
	return ""
}
