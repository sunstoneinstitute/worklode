package cli

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sunstoneinstitute/worklode/internal/mdfence"
	"github.com/sunstoneinstitute/worklode/internal/refkind"
)

// Terminal markdown styling, in-repo rather than through a library.
//
// The CLI's only markdown consumer is a human reading a task or document body
// on a TTY (see Markdown), yet a library that renders it pulls a syntax
// highlighter's lexer and style registries into the binary, and Go runs every
// linked package's init before main. `lode-statusline`, which a coding-agent
// harness re-runs on every assistant message and which renders no markdown at
// all, paid ~11 ms of that on every invocation. The subset below covers what
// the corpus actually contains — headings, lists, block quotes, fenced code,
// tables, rules, and inline emphasis, code, links and images — and costs
// nothing before it is called.
//
// Rendering is line-based on purpose. Bodies here are authored hard-wrapped,
// so preserving the source's own line structure and only re-wrapping what
// overflows keeps output closest to what the author wrote.

// ANSI SGR sequences. Colours stay in the 8-colour range so they inherit the
// terminal's palette instead of fighting it.
const (
	sgrReset     = "\x1b[0m"
	sgrBold      = "\x1b[1m"
	sgrDim       = "\x1b[2m"
	sgrItalic    = "\x1b[3m"
	sgrUnderline = "\x1b[4m"
	sgrStrike    = "\x1b[9m"
	sgrCyan      = "\x1b[36m"
)

// linker says how renderStyled emits links. The zero value prints a
// markdown link's URL after its text and links no references.
type linker struct {
	osc8   bool   // the terminal renders OSC 8: links become clickable text
	server string // cockpit base URL references link to; "" links none
	key    string // project key a bare task id (WL-7) must carry to link
}

// refInText matches a typed reference (WL-SPEC-4, WL-REQ-12, WL-MILE-2) or a
// bare task id (WL-7). A bare id links only under linker.key, which is what
// keeps UTF-8 and SHA-256 from linking.
var refInText = regexp.MustCompile(`\b(` + refkind.KeyPattern + `)-(?:(` + refkind.Alternation(refkind.Targets...) + `)-)?\d+\b`)

// refRuns splits plain text into runs, each reference becoming a run linked
// to its cockpit page.
func (lk linker) refRuns(s string) []styledRun {
	if !lk.osc8 || lk.server == "" {
		return []styledRun{{text: s}}
	}
	var runs []styledRun
	last := 0
	for _, m := range refInText.FindAllStringSubmatchIndex(s, -1) {
		if m[4] < 0 && s[m[2]:m[3]] != lk.key {
			continue
		}
		if m[0] > last {
			runs = append(runs, styledRun{text: s[last:m[0]]})
		}
		runs = append(runs, styledRun{text: s[m[0]:m[1]], link: lk.server + "/" + s[m[0]:m[1]]})
		last = m[1]
	}
	if last < len(s) {
		runs = append(runs, styledRun{text: s[last:]})
	}
	return runs
}

// text links every reference in a plain line.
func (lk linker) text(s string) string {
	var b strings.Builder
	for _, r := range lk.refRuns(s) {
		if r.link != "" {
			b.WriteString(Hyperlink(r.link, r.text))
		} else {
			b.WriteString(r.text)
		}
	}
	return b.String()
}

// printf is fmt.Fprintf with every reference in the output linked, for the
// metadata lines printed around a markdown body.
func (lk linker) printf(w io.Writer, format string, a ...any) {
	fmt.Fprint(w, lk.text(fmt.Sprintf(format, a...)))
}

// renderStyled renders body as ANSI-styled markdown wrapped at width. Callers
// gate on the writer being an interactive terminal with colour enabled;
// renderStyled itself always styles.
func renderStyled(body string, width int, lk linker) string {
	var out strings.Builder
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " ")
		switch {
		case openingFence(line) != "":
			i = writeCodeBlock(&out, lines, i, openingFence(line))
		case trimmed == "":
			out.WriteString("\n")
		case strings.HasPrefix(trimmed, "|"):
			// A table's columns are already aligned in the source; wrapping
			// or re-flowing it would destroy the one thing making it
			// readable.
			out.WriteString(line + "\n")
		case isThematicBreak(trimmed):
			out.WriteString(sgrDim + strings.Repeat("─", width) + sgrReset + "\n")
		case headingLevel(trimmed) > 0:
			writeHeading(&out, line, width, lk)
		default:
			// A paragraph or list item is every following line that does not
			// itself open a block. Bodies here are authored hard-wrapped at
			// their own width, so re-wrapping each source line separately
			// would leave a two-word orphan after every one of them.
			end := i + 1
			for end < len(lines) && isContinuation(lines[end]) {
				end++
			}
			writeFlowed(&out, lines[i:end], width, lk)
			i = end - 1
		}
	}
	return strings.TrimRight(out.String(), "\n")
}

// isContinuation reports whether the line continues the paragraph or list item
// above it rather than opening a block of its own.
func isContinuation(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if trimmed == "" || openingFence(line) != "" || strings.HasPrefix(trimmed, "|") {
		return false
	}
	if strings.HasPrefix(trimmed, ">") || headingLevel(trimmed) > 0 || isThematicBreak(trimmed) {
		return false
	}
	_, _, isList := listMarker(trimmed)
	return !isList
}

// writeHeading renders one ATX heading. Level 1 gets a rule under it as well
// as bold, so the top of a long document body is findable when scrolling.
func writeHeading(out *strings.Builder, line string, width int, lk linker) {
	trimmed := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(trimmed)]
	style := sgrBold
	if headingLevel(trimmed) == 1 {
		style = sgrBold + sgrUnderline
	}
	text := strings.TrimSpace(trimmed[headingLevel(trimmed):])
	pre := prefix{text: indent}
	writeWrapped(out, tokenize(inline(text, lk), style), width, pre, pre)
}

// writeFlowed renders one paragraph, list item or block quote, joining its
// continuation lines and re-wrapping the whole thing to width.
func writeFlowed(out *strings.Builder, block []string, width int, lk linker) {
	trimmed := strings.TrimLeft(block[0], " ")
	indent := block[0][:len(block[0])-len(trimmed)]
	text := trimmed
	for _, cont := range block[1:] {
		text += " " + strings.TrimSpace(cont)
	}
	switch {
	case strings.HasPrefix(text, ">"):
		quote := prefix{text: indent + "│ ", style: sgrDim}
		body := strings.TrimPrefix(strings.TrimPrefix(text, ">"), " ")
		writeWrapped(out, tokenize(inline(body, lk), sgrDim), width, quote, quote)
	default:
		first, item := prefix{text: indent}, text
		if marker, rest, ok := listMarker(text); ok {
			first, item = prefix{text: indent + marker + " "}, rest
		}
		cont := prefix{text: strings.Repeat(" ", first.width())}
		writeWrapped(out, tokenize(inline(item, lk), ""), width, first, cont)
	}
}

// writeCodeBlock renders the fenced block opening at lines[i] and returns the
// index of its closing fence, or of the last line when the fence is never
// closed. The fence lines themselves are dropped; the indent replaces them.
func writeCodeBlock(out *strings.Builder, lines []string, i int, fence string) int {
	for j := i + 1; j < len(lines); j++ {
		if mdfence.Closes(lines[j], fence) {
			return j
		}
		out.WriteString(sgrDim + "  " + lines[j] + sgrReset + "\n")
	}
	return len(lines) - 1
}

// openingFence reports the fence that opens a code block on this line, or "".
func openingFence(line string) string {
	fence, _ := mdfence.Open(line)
	return fence
}

// isThematicBreak reports whether the line is a horizontal rule: three or more
// of one break character, and nothing else but spaces.
func isThematicBreak(trimmed string) bool {
	for _, c := range []string{"-", "*", "_"} {
		stripped := strings.ReplaceAll(strings.ReplaceAll(trimmed, c, ""), " ", "")
		if stripped == "" && strings.Count(trimmed, c) >= 3 {
			return true
		}
	}
	return false
}

// headingLevel reports the ATX heading level of the line, or 0.
func headingLevel(trimmed string) int {
	level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
	if level < 1 || level > 6 || level == len(trimmed) || trimmed[level] != ' ' {
		return 0
	}
	return level
}

// listMarker splits a list item into the marker to print and the item text.
// Bullets are normalised to one glyph; ordered markers are kept verbatim so
// the author's numbering survives.
func listMarker(trimmed string) (marker, rest string, ok bool) {
	if len(trimmed) > 1 && strings.ContainsRune("-*+", rune(trimmed[0])) && trimmed[1] == ' ' {
		return "•", strings.TrimLeft(trimmed[2:], " "), true
	}
	digits := 0
	for digits < len(trimmed) && trimmed[digits] >= '0' && trimmed[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits+1 >= len(trimmed) {
		return "", "", false
	}
	if (trimmed[digits] != '.' && trimmed[digits] != ')') || trimmed[digits+1] != ' ' {
		return "", "", false
	}
	return trimmed[:digits+1], strings.TrimLeft(trimmed[digits+2:], " "), true
}

// prefix is the literal text that opens a rendered line — indentation, a list
// bullet, a block-quote bar — carrying its own style so its width can be
// measured separately from its escape codes.
type prefix struct {
	text  string
	style string
}

func (p prefix) render() string {
	if p.style == "" || p.text == "" {
		return p.text
	}
	return p.style + p.text + sgrReset
}

func (p prefix) width() int { return utf8.RuneCountInString(p.text) }

// styledRun is a stretch of visible text sharing one style, and the URL it
// links to when it is an OSC 8 hyperlink.
type styledRun struct {
	text  string
	style string
	link  string
}

// token is one wrappable word: its rendered bytes and its visible width. The
// two differ whenever the word carries escape codes, which is why wrapping
// cannot measure the rendered string.
type token struct {
	out   string
	width int
}

// tokenize splits runs into space-separated words, ANSI-rendering each. base
// is the style applied to runs that carry none of their own, so a heading or
// block quote styles its plain text without the inline parser knowing about
// the block it sits in.
func tokenize(runs []styledRun, base string) []token {
	var toks []token
	var cur token
	flush := func() {
		if cur.width > 0 {
			toks = append(toks, cur)
			cur = token{}
		}
	}
	for _, r := range runs {
		style := r.style
		if style == "" {
			style = base
		}
		for k, word := range strings.Split(r.text, " ") {
			if k > 0 {
				flush()
			}
			if word == "" {
				continue
			}
			out := word
			if style != "" {
				out = style + word + sgrReset
			}
			if r.link != "" {
				out = Hyperlink(r.link, out)
			}
			cur.out += out
			cur.width += utf8.RuneCountInString(word)
		}
	}
	flush()
	return toks
}

// writeWrapped emits toks greedily wrapped to width, opening the first line
// with first and every continuation line with cont.
func writeWrapped(out *strings.Builder, toks []token, width int, first, cont prefix) {
	if len(toks) == 0 {
		out.WriteString(strings.TrimRight(first.render(), " ") + "\n")
		return
	}
	out.WriteString(first.render())
	col, atLineStart := first.width(), true
	for _, t := range toks {
		if !atLineStart && col+1+t.width > width {
			out.WriteString("\n" + cont.render())
			col, atLineStart = cont.width(), true
		}
		if !atLineStart {
			out.WriteString(" ")
			col++
		}
		out.WriteString(t.out)
		col += t.width
		atLineStart = false
	}
	out.WriteString("\n")
}

// inline splits one line of markdown into styled runs. Spans are matched
// left to right and do not nest: `**a *b* c**` is bold throughout, with the
// inner markers left visible, which is rarer in this corpus than the cost of
// a real inline parser.
func inline(s string, lk linker) []styledRun {
	var runs []styledRun
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			runs = append(runs, lk.refRuns(plain.String())...)
			plain.Reset()
		}
	}
	for i := 0; i < len(s); {
		if span, n, ok := inlineSpan(s, i, lk); ok {
			flush()
			runs = append(runs, span...)
			i += n
			continue
		}
		plain.WriteByte(s[i])
		i++
	}
	flush()
	return runs
}

// inlineSpan matches an inline construct starting at s[i], returning the runs
// it renders to and how many bytes it consumed.
func inlineSpan(s string, i int, lk linker) ([]styledRun, int, bool) {
	rest := s[i:]
	switch {
	case rest[0] == '`':
		if j := strings.IndexByte(rest[1:], '`'); j > 0 {
			return []styledRun{{text: rest[1 : 1+j], style: sgrCyan}}, j + 2, true
		}
	case strings.HasPrefix(rest, "!["):
		// An image degrades to its alt text plus the URL: no terminal image
		// protocol is assumed, and the URL stays visible so it is clickable.
		if text, url, n, ok := parseLink(rest[1:]); ok {
			return []styledRun{{text: text}, {text: " " + url, style: sgrDim}}, n + 1, true
		}
	case rest[0] == '[':
		if text, url, n, ok := parseLink(rest); ok {
			if lk.osc8 && strings.Contains(url, "://") {
				return []styledRun{{text: text, style: sgrUnderline, link: url}}, n, true
			}
			return []styledRun{{text: text, style: sgrUnderline}, {text: " " + url, style: sgrDim}}, n, true
		}
	case strings.HasPrefix(rest, "**"), strings.HasPrefix(rest, "__"):
		if text, n, ok := parseDelimited(rest, rest[:2]); ok {
			return []styledRun{{text: text, style: sgrBold}}, n, true
		}
	case strings.HasPrefix(rest, "~~"):
		if text, n, ok := parseDelimited(rest, "~~"); ok {
			return []styledRun{{text: text, style: sgrStrike}}, n, true
		}
	case rest[0] == '*', rest[0] == '_':
		// `_` opens emphasis only at a word boundary, so snake_case
		// identifiers in prose are not mangled into italics.
		if rest[0] == '_' && i > 0 && isWordByte(s[i-1]) {
			return nil, 0, false
		}
		if text, n, ok := parseDelimited(rest, rest[:1]); ok {
			return []styledRun{{text: text, style: sgrItalic}}, n, true
		}
	}
	return nil, 0, false
}

func isWordByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// parseDelimited matches delim...delim at the start of s. The span must not
// open on whitespace, which is what separates emphasis from a bare `*` or a
// multiplication sign in prose.
func parseDelimited(s, delim string) (text string, n int, ok bool) {
	body := s[len(delim):]
	if body == "" || body[0] == ' ' {
		return "", 0, false
	}
	j := strings.Index(body, delim)
	if j <= 0 || body[j-1] == ' ' {
		return "", 0, false
	}
	return body[:j], len(delim)*2 + j, true
}

// parseLink matches `[text](url)` at the start of s.
func parseLink(s string) (text, url string, n int, ok bool) {
	label := strings.IndexByte(s, ']')
	if label < 0 || label+1 >= len(s) || s[label+1] != '(' {
		return "", "", 0, false
	}
	end := strings.IndexByte(s[label+2:], ')')
	if end < 0 {
		return "", "", 0, false
	}
	return s[1:label], s[label+2 : label+2+end], label + 3 + end, true
}
