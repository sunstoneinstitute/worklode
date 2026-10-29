// Package mdblocks converts Markdown to the body editor's blocks
// (WL-REQ-1867). A construct in Worklode Markdown (WL-REQ-1871) becomes its
// block; any other construct becomes a `source` block holding its bytes.
package mdblocks

import (
	"bytes"
	"cmp"
	"encoding/json"
	"slices"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	gparser "github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/sunstoneinstitute/worklode/internal/mdfence"
	"github.com/sunstoneinstitute/worklode/internal/mdrender"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Definition lists and footnotes are parsed only so they become `source`
// blocks. The footnote transformer is left out: it drops unreferenced
// definitions.
var parser = goldmark.New(
	goldmark.WithExtensions(
		extension.Table, extension.Strikethrough, extension.Linkify, extension.TaskList,
		extension.DefinitionList, mdrender.Emoji,
	),
	goldmark.WithParserOptions(
		gparser.WithBlockParsers(util.Prioritized(extension.NewFootnoteBlockParser(), 999)),
		gparser.WithInlineParsers(util.Prioritized(extension.NewFootnoteParser(), 101)),
	),
).Parser()

// Parse returns md's top-level blocks. Each block's Range is the byte span of
// its node, including list markers and fence lines, without the final newline.
func Parse(md []byte) ([]model.EditorBlock, error) {
	doc := parser.Parse(text.NewReader(md))
	out := blocks(md, doc.FirstChild())
	// The footnote parser gathers every definition at the first one's place.
	slices.SortStableFunc(out, func(a, b model.EditorBlock) int { return cmp.Compare(a.Range.Start, b.Range.Start) })
	return out, nil
}

// blocks converts n and its following siblings. A list contributes its items.
func blocks(src []byte, n ast.Node) []model.EditorBlock {
	var out []model.EditorBlock
	for ; n != nil; n = n.NextSibling() {
		if fl, ok := n.(*east.FootnoteList); ok {
			for fn := fl.FirstChild(); fn != nil; fn = fn.NextSibling() {
				out = append(out, source(src, span(src, fn)))
			}
			continue
		}
		l, ok := n.(*ast.List)
		if !ok {
			out = append(out, block(src, n))
			continue
		}
		for i, it := 0, l.FirstChild(); it != nil; i, it = i+1, it.NextSibling() {
			out = append(out, listItem(src, l, it, i))
		}
	}
	return out
}

func block(src []byte, n ast.Node) model.EditorBlock {
	r := span(src, n)
	switch n := n.(type) {
	case *ast.Paragraph:
		if !inProfile(src, n) {
			break
		}
		if img, ok := soleImage(n); ok {
			return model.EditorBlock{Type: "image", Range: r, Props: props(struct {
				Caption string `json:"caption"`
				URL     string `json:"url"`
			}{plainText(inline(src, img)), string(img.Destination)})}
		}
		return model.EditorBlock{Type: "paragraph", Range: r, Content: inline(src, n)}
	case *ast.Heading:
		if src[n.Pos()] != '#' || !inProfile(src, n) {
			break
		}
		return model.EditorBlock{Type: "heading", Range: r, Content: inline(src, n), Props: props(struct {
			Level int `json:"level"`
		}{n.Level})}
	case *ast.FencedCodeBlock:
		b := model.EditorBlock{Type: "codeBlock", Range: r}
		if lang := strings.TrimPrefix(strings.Trim(string(n.Language(src)), "{}"), "."); lang != "" {
			b.Props = props(struct {
				Language string `json:"language"`
			}{lang})
		}
		for i := 0; i < n.Lines().Len(); i++ {
			s := n.Lines().At(i)
			b.Content = append(b.Content, model.InlineContent{Type: "text", Text: string(s.Value(src)), Offset: s.Start})
		}
		return b
	case *ast.Blockquote:
		// A quote block holds inline content only, so a quote of anything but
		// one paragraph is source.
		p, ok := n.FirstChild().(*ast.Paragraph)
		if !ok || p.NextSibling() != nil || !inProfile(src, p) {
			break
		}
		return model.EditorBlock{Type: "quote", Range: r, Content: inline(src, p)}
	case *east.Table:
		return table(src, n, r)
	}
	return source(src, r)
}

func listItem(src []byte, l *ast.List, it ast.Node, index int) model.EditorBlock {
	r := span(src, it)
	b := model.EditorBlock{Type: "bulletListItem", Range: r}
	if l.IsOrdered() {
		b.Type = "numberedListItem"
		if index == 0 && l.Start != 1 {
			b.Props = props(struct {
				Start int `json:"start"`
			}{l.Start})
		}
	}
	rest := it.FirstChild()
	if rest != nil && (rest.Kind() == ast.KindTextBlock || rest.Kind() == ast.KindParagraph) {
		if !inProfile(src, rest) {
			return source(src, r)
		}
		if cb, ok := rest.FirstChild().(*east.TaskCheckBox); ok {
			b.Type = "checkListItem"
			b.Props = props(struct {
				Checked bool `json:"checked"`
			}{cb.IsChecked})
		}
		b.Content = inline(src, rest)
		rest = rest.NextSibling()
	}
	b.Children = blocks(src, rest)
	return b
}

func table(src []byte, t *east.Table, r model.ByteRange) model.EditorBlock {
	one := 1
	tc := &model.TableContent{Type: "tableContent", ColumnWidths: make([]*int, len(t.Alignments)), HeaderRows: &one}
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		var tr model.TableRow
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			if !inProfile(src, cell) {
				return source(src, r)
			}
			tr.Cells = append(tr.Cells, model.TableCell{Content: inline(src, cell)})
		}
		tc.Rows = append(tc.Rows, tr)
	}
	return model.EditorBlock{Type: "table", Range: r, TableContent: tc}
}

func source(src []byte, r model.ByteRange) model.EditorBlock {
	return model.EditorBlock{Type: "source", Range: r, Props: props(struct {
		Markdown string `json:"markdown"`
	}{string(src[r.Start:r.End])})}
}

func props(v any) json.RawMessage {
	b, _ := json.Marshal(v) // props are flat structs of strings, ints and bools
	return b
}

// inProfile reports whether n's inline content is all Worklode Markdown: no
// raw HTML, no footnote reference and no code span over more than one line
// (WL-REQ-1871).
func inProfile(src []byte, n ast.Node) bool {
	ok := true
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		switch c := c.(type) {
		case *ast.RawHTML, *east.FootnoteLink:
			ok = false
		case *ast.CodeSpan:
			first, last := c.FirstChild(), c.LastChild()
			if first != nil && bytes.ContainsAny(src[first.(*ast.Text).Segment.Start:last.(*ast.Text).Segment.Stop], "\n") {
				ok = false
			}
		}
		if !ok {
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return ok
}

// soleImage returns p's image when it is p's only content.
func soleImage(p ast.Node) (*ast.Image, bool) {
	img, ok := p.FirstChild().(*ast.Image)
	return img, ok && img.NextSibling() == nil
}

func plainText(runs []model.InlineContent) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

// span is n's byte range: from its first byte to the end of the line holding
// its last byte.
func span(src []byte, n ast.Node) model.ByteRange {
	start := first(n)
	if _, ok := n.(*ast.CodeBlock); ok {
		start = lineStart(src, start) // indented code keeps its indentation
	}
	return model.ByteRange{Start: start, End: lineEnd(src, last(src, n))}
}

// first is the lowest offset n or a descendant starts at. A definition list's
// node starts at its first definition, after its term.
func first(n ast.Node) int {
	m := n.Pos()
	if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 {
		m = minPos(m, n.Lines().At(0).Start)
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Type() == ast.TypeBlock {
			m = minPos(m, first(c))
		}
	}
	return m
}

func minPos(a, b int) int {
	if a < 0 || (b >= 0 && b < a) {
		return b
	}
	return a
}

// last is an offset on the last line of n: its last segment, a descendant's,
// or the line after it that closes a fence or underlines a setext heading.
func last(src []byte, n ast.Node) int {
	m := n.Pos()
	if n.Type() == ast.TypeBlock {
		if l := n.Lines(); l.Len() > 0 {
			s := l.At(l.Len() - 1)
			m = max(m, s.Start, s.Stop-1)
		}
	}
	if t, ok := n.(*ast.Text); ok {
		m = max(m, t.Segment.Stop-1)
	}
	if h, ok := n.(*ast.HTMLBlock); ok && h.HasClosure() {
		m = max(m, h.ClosureLine.Start)
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		m = max(m, last(src, c))
	}
	next := lineEnd(src, m) + 1
	if next >= len(src) {
		return m
	}
	nextLine := string(src[next:lineEnd(src, next)])
	switch n := n.(type) {
	case *ast.FencedCodeBlock:
		marker, _ := mdfence.Open(string(src[n.Pos():lineEnd(src, n.Pos())]))
		// A fence in a quote closes on a "> ```" line.
		if marker != "" && mdfence.Closes(strings.TrimLeft(nextLine, " >"), marker) {
			return next
		}
	case *ast.Heading:
		if src[n.Pos()] != '#' {
			return next
		}
	}
	return m
}

func lineStart(src []byte, i int) int {
	return bytes.LastIndexByte(src[:i], '\n') + 1
}

func lineEnd(src []byte, i int) int {
	if j := bytes.IndexByte(src[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(src)
}
