// Package designdoc parses Quarto-flavoured markdown design documents —
// specs, ADRs and plans under docs/ — into the wl:DesignDoc / wl:Section
// shape defined in ns/ontology.ttl.
//
// Parsing preserves the source exactly: an unedited Parse followed by Bytes
// reproduces the input byte for byte, whatever the file does with line
// endings, tabs or trailing whitespace. Structure is derived from raw source
// spans rather than by normalising the text, so the documents already in
// docs/specs round-trip without anyone having to catalogue their quirks
// first. Only a node the caller has edited is re-rendered from its fields.
package designdoc

import (
	"regexp"
	"strings"
)

// heading matches an ATX heading, optionally numbered and optionally
// carrying a Quarto anchor: "## 4.1a Title {#sec-4.1a}". Ported from
// scripts/secfmt.py, which is what the pre-commit hook enforces — the two
// must agree on what a section is. H1 is excluded deliberately: it is the
// document title, not an addressable section (014 §3).
//
// The trailing dot after the number is optional because the house style is
// "1." at the top level and "1.1" below it; it is not captured either way.
var heading = regexp.MustCompile(
	`^(?P<hashes>#{2,6})[ \t]+` +
		`(?:(?P<num>\d+(?:\.\d+)*[a-z]?)\.?[ \t]+)?` +
		`(?P<text>.*?)` +
		`(?:[ \t]*\{#(?P<anchor>[\w.\-]+)\})?` +
		`[ \t]*$`)

// Document is a parsed design document: a wl:DesignDoc.
type Document struct {
	// Preamble is everything from the end of the frontmatter to the first
	// section heading — conventionally the H1 title and any text under it.
	Preamble string
	// Sections are every section in the document, in document order.
	Sections []*Section
	// Frontmatter is the YAML header, or nil when the document has none.
	Frontmatter *Frontmatter

	src []byte
}

// Section is an addressable part of a document: a wl:Section. It is
// identified by its Anchor, which is stable for the life of the document.
type Section struct {
	// Level is the heading depth, 2 for "##" through 6 for "######".
	Level int
	// Number is the section number without its trailing dot ("4.1a"), or
	// empty for an unnumbered heading.
	Number string
	// Title is the heading text with number and anchor removed.
	Title string
	// Anchor is the Quarto anchor without its "#" ("sec-4.1a"), or empty
	// when the heading carries none.
	Anchor string
	// Body is the source between this heading and the next heading of any
	// level, so a section's Body excludes its subsections' text.
	Body string

	// Index is this section's position in Document.Sections.
	Index int
	// Parent is the enclosing section, nil at the top level.
	Parent *Section
	// Children are the immediately nested sections.
	Children []*Section

	// raw is the heading line exactly as it appeared, including its line
	// terminator; term is that terminator alone. raw is emitted verbatim
	// until a heading field is edited.
	raw  string
	term string
	// orig* are the values parsed out of raw. Bytes re-renders the heading
	// only when a field no longer matches, so callers assign to the exported
	// fields directly and there is no dirty flag to forget to set.
	origLevel                         int
	origNumber, origTitle, origAnchor string
}

// Parse reads a design document from src.
func Parse(src []byte) (*Document, error) {
	d := &Document{src: src}
	front, inner, body := splitFrontmatter(string(src))
	if front != "" {
		fm, err := parseFrontmatter(front, inner)
		if err != nil {
			return nil, err
		}
		d.Frontmatter = fm
	}

	// Offsets of each heading line within body, and the parsed section.
	var starts, ends []int
	for _, h := range scanHeadings(body) {
		raw := body[h.start:h.end]
		sec := &Section{
			Level:      len(h.hashes),
			Number:     h.num,
			Title:      h.text,
			Anchor:     h.anchor,
			Index:      len(d.Sections),
			raw:        raw,
			term:       terminatorOf(raw),
			origLevel:  len(h.hashes),
			origNumber: h.num,
			origTitle:  h.text,
			origAnchor: h.anchor,
		}
		d.Sections = append(d.Sections, sec)
		starts = append(starts, h.start)
		ends = append(ends, h.end)
	}

	if len(d.Sections) == 0 {
		d.Preamble = body
		return d, nil
	}
	d.Preamble = body[:starts[0]]
	for i, sec := range d.Sections {
		if i+1 < len(d.Sections) {
			sec.Body = body[ends[i]:starts[i+1]]
		} else {
			sec.Body = body[ends[i]:]
		}
	}
	link(d.Sections)
	return d, nil
}

// link fills in Parent and Children from heading depth. A section nests under
// the nearest preceding section of shallower level, so a document that jumps
// from ## to #### still nests rather than losing the deeper heading.
func link(sections []*Section) {
	var stack []*Section
	for _, sec := range sections {
		for len(stack) > 0 && stack[len(stack)-1].Level >= sec.Level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			sec.Parent = stack[len(stack)-1]
			sec.Parent.Children = append(sec.Parent.Children, sec)
		}
		stack = append(stack, sec)
	}
}

// Bytes renders the document. For a document that has not been edited it
// returns the source unchanged.
func (d *Document) Bytes() []byte {
	var b strings.Builder
	b.WriteString(d.Frontmatter.source())
	b.WriteString(d.Preamble)
	for _, sec := range d.Sections {
		b.WriteString(sec.headingSource())
		b.WriteString(sec.Body)
	}
	return []byte(b.String())
}

// headingSource returns the section's heading line: the original bytes while
// every heading field still holds what was parsed, and a fresh rendering once
// any of them has been assigned. Keeping the untouched case verbatim means
// editing one section never reformats its neighbours.
func (s *Section) headingSource() string {
	if s.Level == s.origLevel && s.Number == s.origNumber &&
		s.Title == s.origTitle && s.Anchor == s.origAnchor {
		return s.raw
	}
	return s.renderHeading()
}

// renderHeading writes the heading in house style: "1." at the top level and
// "1.1" below it (secfmt.py's label rule), anchor last, original line
// terminator preserved.
func (s *Section) renderHeading() string {
	var b strings.Builder
	b.WriteString(strings.Repeat("#", s.Level))
	b.WriteByte(' ')
	if s.Number != "" {
		b.WriteString(s.Number)
		if s.Level == 2 {
			b.WriteByte('.')
		}
		b.WriteByte(' ')
	}
	b.WriteString(s.Title)
	if s.Anchor != "" {
		b.WriteString(" {#")
		b.WriteString(s.Anchor)
		b.WriteByte('}')
	}
	b.WriteString(s.term)
	return b.String()
}

// terminatorOf returns the line ending a raw line carries, empty at EOF.
func terminatorOf(raw string) string {
	switch {
	case strings.HasSuffix(raw, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(raw, "\n"):
		return "\n"
	}
	return ""
}

// hit is one heading found in the source, with its byte span.
type hit struct {
	start, end                int // span of the heading line, excluding terminator
	hashes, num, text, anchor string
}

// scanHeadings finds every ATX heading outside fenced code, in order.
func scanHeadings(body string) []hit {
	var hits []hit
	var fence string
	for _, ln := range splitLines(body) {
		line := strings.TrimRight(body[ln.start:ln.textEnd], "\r")
		stripped := strings.TrimLeft(line, " \t")
		if fence != "" {
			if strings.HasPrefix(stripped, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(stripped, "```") || strings.HasPrefix(stripped, "~~~") {
			fence = stripped[:3]
			continue
		}
		m := heading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		hits = append(hits, hit{
			start:  ln.start,
			end:    ln.end,
			hashes: m[1],
			num:    m[2],
			text:   m[3],
			anchor: m[4],
		})
	}
	return hits
}

// span locates one line: start, the offset just past its text (before any
// terminator), and end, the offset just past the terminator.
type span struct {
	start, textEnd, end int
}

// splitLines returns every line in s, terminators included in end. A final
// line with no terminator is returned too, so concatenating the spans
// reproduces s.
func splitLines(s string) []span {
	var out []span
	for i := 0; i < len(s); {
		nl := strings.IndexByte(s[i:], '\n')
		if nl < 0 {
			out = append(out, span{start: i, textEnd: len(s), end: len(s)})
			break
		}
		out = append(out, span{start: i, textEnd: i + nl, end: i + nl + 1})
		i += nl + 1
	}
	return out
}
