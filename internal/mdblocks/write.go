package mdblocks

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Normalize is Write(Parse(md)), the stored form of a body (WL-REQ-1867).
// It returns md unchanged when md's blocks cannot be written.
func Normalize(md []byte) []byte {
	blocks, err := Parse(md)
	if err != nil {
		return md
	}
	out, err := Write(blocks)
	if err != nil {
		return md
	}
	return out
}

// Write returns the stored spelling of blocks (WL-REQ-1871): each paragraph
// and list item on one line except at a hard line break, a blank line between blocks except between list
// items, children indented to their list item's text. Run text is source
// bytes and is written verbatim, escapes included.
func Write(blocks []model.EditorBlock) ([]byte, error) {
	for _, b := range blocks {
		if err := b.Validate(); err != nil {
			return nil, err
		}
	}
	var w strings.Builder
	writeBlocks(&w, blocks, "")
	if w.Len() == 0 {
		return nil, nil
	}
	w.WriteByte('\n')
	return []byte(w.String()), nil
}

func isItem(b model.EditorBlock) bool {
	return b.Type == "bulletListItem" || b.Type == "numberedListItem" || b.Type == "checkListItem"
}

// writeBlocks writes blocks with every line after the first prefixed by
// indent. The first line's indentation is the caller's.
func writeBlocks(w *strings.Builder, blocks []model.EditorBlock, indent string) {
	number := 0
	for i, b := range blocks {
		if i > 0 {
			w.WriteString(sep(blocks[i-1], b))
			w.WriteString(indent)
		}
		if b.Type == "numberedListItem" {
			var p struct{ Start int }
			_ = json.Unmarshal(b.Props, &p)
			switch {
			case p.Start != 0:
				number = p.Start
			case i == 0 || blocks[i-1].Type != "numberedListItem":
				number = 1
			default:
				number++
			}
		}
		writeBlock(w, b, indent, number)
	}
}

func sep(prev, next model.EditorBlock) string {
	if isItem(prev) && isItem(next) {
		return "\n"
	}
	return "\n\n"
}

func writeBlock(w *strings.Builder, b model.EditorBlock, indent string, number int) {
	switch b.Type {
	case "paragraph":
		w.WriteString(breaks(writeInline(b.Content, false), indent))
	case "heading":
		var p struct {
			Level  int
			Anchor string
		}
		_ = json.Unmarshal(b.Props, &p)
		line := strings.Repeat("#", max(p.Level, 1))
		if t := writeInline(b.Content, false); t != "" {
			line += " " + t
		}
		if p.Anchor != "" {
			line += " {#" + p.Anchor + "}"
		}
		w.WriteString(line)
	case "bulletListItem", "numberedListItem", "checkListItem":
		marker := "- "
		if b.Type == "numberedListItem" {
			marker = strconv.Itoa(number) + ". "
		}
		text := writeInline(b.Content, false)
		if b.Type == "checkListItem" {
			var p struct{ Checked bool }
			_ = json.Unmarshal(b.Props, &p)
			box := "[ ] "
			if p.Checked {
				box = "[x] "
			}
			text = box + text
		}
		w.WriteString(breaks(strings.TrimRight(marker+text, " "), indent+strings.Repeat(" ", len(marker))))
		if len(b.Children) > 0 {
			child := indent + strings.Repeat(" ", len(marker))
			w.WriteString(sep(b, b.Children[0]))
			w.WriteString(child)
			writeBlocks(w, b.Children, child)
		}
	case "codeBlock":
		var p struct{ Language string }
		_ = json.Unmarshal(b.Props, &p)
		var body strings.Builder
		for _, r := range b.Content {
			body.WriteString(r.Text)
		}
		code := body.String()
		fence := strings.Repeat("`", max(3, longestRun(code, '`')+1))
		lang := p.Language
		if lang == "mermaid" {
			lang = "{mermaid}"
		}
		w.WriteString(fence + lang + "\n")
		for _, line := range strings.SplitAfter(code, "\n") {
			if line == "" {
				continue
			}
			if line != "\n" {
				w.WriteString(indent)
			}
			w.WriteString(line)
		}
		if code != "" && !strings.HasSuffix(code, "\n") {
			w.WriteByte('\n')
		}
		w.WriteString(indent + fence)
	case "quote":
		w.WriteString(breaks(strings.TrimRight("> "+writeInline(b.Content, false), " "), indent+"> "))
	case "callout":
		var p struct{ Kind string }
		_ = json.Unmarshal(b.Props, &p)
		w.WriteString("::: {.callout-" + p.Kind + "}\n")
		if len(b.Children) > 0 {
			w.WriteString(indent)
			writeBlocks(w, b.Children, indent)
			w.WriteString("\n")
		}
		w.WriteString(indent + ":::")
	case "image":
		var p struct{ Caption, URL string }
		_ = json.Unmarshal(b.Props, &p)
		w.WriteString("![" + p.Caption + "](" + dest(p.URL) + ")")
	case "table":
		writeTable(w, b.TableContent, indent)
	case "source":
		// Lines after the first keep their source indentation, which is at
		// least the container's.
		var p struct{ Markdown string }
		_ = json.Unmarshal(b.Props, &p)
		w.WriteString(p.Markdown)
	}
}

func writeTable(w *strings.Builder, tc *model.TableContent, indent string) {
	if tc == nil {
		return
	}
	for i, row := range tc.Rows {
		if i > 0 {
			w.WriteString("\n" + indent)
		}
		cells := make([]string, len(row.Cells))
		for j, c := range row.Cells {
			cells[j] = writeInline(c.Content, true)
		}
		w.WriteString(strings.ReplaceAll("| "+strings.Join(cells, " | ")+" |", "  |", " |"))
		if i == 0 {
			w.WriteString("\n" + indent + strings.Repeat("|---", len(row.Cells)) + "|")
		}
	}
}

var styleOrder = []string{"bold", "italic", "strike"}

var delim = map[string]string{"bold": "**", "italic": "*", "strike": "~~"}

// breaks writes each hard line break in line as a backslash before the
// newline (WL-1423), the next line prefixed by indent. A break at the end has
// no line to break before and is dropped.
func breaks(line, indent string) string {
	line = strings.TrimRight(line, "\n")
	return strings.ReplaceAll(line, "\n", "\\\n"+indent)
}

// writeInline writes runs on one line, except a hard line break, which stays
// "\n" outside a code span and a table cell. Emphasis delimiters nest: a style
// that runs on longer is opened first, so it closes last. Consecutive code
// runs are one code span split at an escape. In a table cell an unescaped
// pipe is escaped: the table parser drops the backslash inside a code span.
func writeInline(runs []model.InlineContent, cell bool) string {
	var b strings.Builder
	var open []string
	closeTo := func(keep map[string]bool) {
		for len(open) > 0 && !keepAll(open, keep) {
			b.WriteString(delim[open[len(open)-1]])
			open = open[:len(open)-1]
		}
	}
	for i := 0; i < len(runs); i++ {
		r := runs[i]
		if r.Type == "link" {
			closeTo(nil)
			b.WriteString(writeLink(r, cell))
			continue
		}
		closeTo(r.Styles)
		var add []string
		for _, s := range styleOrder {
			if r.Styles[s] && !slices.Contains(open, s) {
				add = append(add, s)
			}
		}
		slices.SortStableFunc(add, func(x, y string) int { return lasts(runs[i:], y) - lasts(runs[i:], x) })
		for _, s := range add {
			b.WriteString(delim[s])
			open = append(open, s)
		}
		text := r.Text
		for r.Styles["code"] && i+1 < len(runs) && runs[i+1].Type == "text" && maps.Equal(runs[i+1].Styles, r.Styles) {
			i++
			text += runs[i].Text
		}
		if cell || r.Styles["code"] {
			text = strings.ReplaceAll(text, "\n", " ")
		}
		if cell {
			text = escapePipes(text)
		}
		if r.Styles["code"] {
			text = codeSpan(text)
		}
		b.WriteString(text)
	}
	closeTo(nil)
	return b.String()
}

func escapePipes(t string) string {
	var b strings.Builder
	for i := 0; i < len(t); i++ {
		if t[i] == '|' && (i == 0 || t[i-1] != '\\') {
			b.WriteByte('\\')
		}
		b.WriteByte(t[i])
	}
	return b.String()
}

func keepAll(open []string, keep map[string]bool) bool {
	for _, s := range open {
		if !keep[s] {
			return false
		}
	}
	return true
}

// lasts counts the leading runs carrying style s.
func lasts(runs []model.InlineContent, s string) int {
	n := 0
	for _, r := range runs {
		if r.Type == "link" || !r.Styles[s] {
			break
		}
		n++
	}
	return n
}

func writeLink(l model.InlineContent, cell bool) string {
	text := writeInline(l.Content, cell)
	if len(l.Content) == 1 && len(l.Content[0].Styles) == 0 {
		if t := l.Content[0].Text; t == l.Href || "mailto:"+t == l.Href {
			return "<" + t + ">"
		}
	}
	return "[" + text + "](" + dest(l.Href) + ")"
}

func dest(u string) string {
	if strings.ContainsAny(u, " ()<>") {
		return "<" + u + ">"
	}
	return u
}

func codeSpan(t string) string {
	fence := strings.Repeat("`", longestRun(t, '`')+1)
	if strings.HasPrefix(t, "`") || strings.HasSuffix(t, "`") ||
		(strings.HasPrefix(t, " ") && strings.HasSuffix(t, " ") && strings.Trim(t, " ") != "") {
		t = " " + t + " "
	}
	return fence + t + fence
}

func longestRun(s string, c byte) int {
	best, n := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			n++
			best = max(best, n)
		} else {
			n = 0
		}
	}
	return best
}
