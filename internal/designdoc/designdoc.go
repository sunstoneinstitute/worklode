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
	"bytes"
	"regexp"
	"strings"
)

// heading matches an ATX heading, optionally numbered and optionally
// carrying a Quarto anchor: "## 4.1a Title {#sec-4.1a}". Ported from
// scripts/secfmt.py, which is what the pre-commit hook enforces — the two
// must agree on what a section is. H1 is excluded deliberately: it is the
// document title, not an addressable section (025 §3).
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
	d := &Document{}
	front, inner, body := splitFrontmatter(string(src))
	if front != "" {
		fm, err := parseFrontmatter(front, inner)
		if err != nil {
			return nil, err
		}
		d.Frontmatter = fm
	}

	// hits[i] carries section i's byte span, so the section bodies below are
	// cut straight from it rather than from parallel offset slices.
	hits := scanHeadings(body)
	for _, h := range hits {
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
	}

	if len(d.Sections) == 0 {
		d.Preamble = body
		return d, nil
	}
	d.Preamble = body[:hits[0].start]
	for i, sec := range d.Sections {
		if i+1 < len(hits) {
			sec.Body = body[hits[i].end:hits[i+1].start]
		} else {
			sec.Body = body[hits[i].end:]
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
	// bytes.Buffer rather than strings.Builder: Builder.String() would have to
	// be copied again to satisfy the []byte return.
	var b bytes.Buffer
	b.WriteString(d.Frontmatter.source())
	b.WriteString(d.Preamble)
	for _, sec := range d.Sections {
		b.WriteString(sec.headingSource())
		b.WriteString(sec.Body)
	}
	return b.Bytes()
}

// Subtree returns the source text of the section anchored anchor, together
// with every section nested under it, and reports whether that anchor exists.
// It is the cut `lode show --section` prints (026 §3: a section is always its
// whole subtree).
//
// The same round-trip guarantee Bytes rests on applies here: for an unedited
// document the result is the exact bytes of that span of the source, so no
// caller needs a second scan to recover heading offsets Parse discarded.
func (d *Document) Subtree(anchor string) (string, bool) {
	for _, sec := range d.Sections {
		if sec.Anchor == anchor {
			return sec.Source(), true
		}
	}
	return "", false
}

// Source returns the section's own heading and body followed by those of its
// descendants, in document order. Children is what bounds it, so a subtree
// ends where the next heading of the same or shallower level begins — and an
// anchorless subsection, legal at H5/H6, is included like any other child.
func (s *Section) Source() string {
	var b strings.Builder
	s.writeSource(&b)
	return b.String()
}

func (s *Section) writeSource(b *strings.Builder) {
	b.WriteString(s.headingSource())
	b.WriteString(s.Body)
	for _, child := range s.Children {
		child.writeSource(b)
	}
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
	start, end                int // span of the heading line, terminator included
	hashes, num, text, anchor string
}

// scanHeadings finds every ATX heading outside fenced code, in order.
func scanHeadings(body string) []hit {
	var hits []hit
	var fence string
	for _, ln := range splitLines(body) {
		line := strings.TrimRight(ln.text(body), "\r")
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

// text returns the line's text out of the string it was located in, without
// its terminator.
func (sp span) text(s string) string { return s[sp.start:sp.textEnd] }

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
