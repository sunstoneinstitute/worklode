package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// Column padding, matched to the tabwriter the tables used before: two spaces
// between columns, none after the last one.
const colPad = 2

// wrapMode says how a column's cell may be broken when the table is wider
// than the terminal.
type wrapMode int

const (
	// wrapNone: the column always gets its natural width. Ids, states and
	// priorities are short and are read as whole tokens.
	wrapNone wrapMode = iota
	// wrapWords: break at spaces (a word longer than the column is split).
	wrapWords
	// wrapChars: break anywhere. For values with no useful word boundaries.
	wrapChars
)

// column is one table column: its header, how it may be broken, and the
// narrowest width it may be squeezed to before the table gives up and lets
// the terminal wrap.
type column struct {
	header string
	wrap   wrapMode
	min    int
}

// Minimum widths for the two flexible column kinds. A title needs enough room
// that word wrapping still produces readable lines; a holder ("actor (until
// <time>)") is char-wrapped, and 16 keeps the actor id on the first line.
const (
	minTitleWidth  = 20
	minHolderWidth = 16
)

func titleColumn(header string) column {
	return column{header: header, wrap: wrapWords, min: minTitleWidth}
}

func holderColumn(header string) column {
	return column{header: header, wrap: wrapChars, min: minHolderWidth}
}

// table accumulates rows and renders them into the terminal's width, wrapping
// the flexible columns instead of letting a long title push the line past the
// right edge — a table whose rows wrap arbitrarily is unreadable.
type table struct {
	cols []column
	rows [][]string
}

func newTable(cols ...column) *table { return &table{cols: cols} }

// add appends one row. Cell count must match the column count.
func (t *table) add(cells ...string) {
	t.rows = append(t.rows, cells)
}

// flush renders the table to w, wrapping to w's terminal width. A writer that
// is not a terminal (a pipe, a test buffer, an agent reading `lode task list`)
// gets unwrapped rows: there is no width to fit, and callers parsing the
// output want one row per line.
func (t *table) flush(w io.Writer) {
	width := 0
	if fd, isTTY := terminalFd(w); isTTY {
		if cols, _, err := term.GetSize(fd); err == nil {
			width = cols
		}
	}
	t.render(w, width)
}

// render writes the table wrapped to width. A width of 0 or less means
// unlimited.
func (t *table) render(w io.Writer, width int) {
	widths := t.layout(width)
	t.writeRow(w, widths, t.headerCells())
	for _, row := range t.rows {
		t.writeRow(w, widths, row)
	}
}

func (t *table) headerCells() []string {
	cells := make([]string, len(t.cols))
	for i, c := range t.cols {
		cells[i] = c.header
	}
	return cells
}

// layout picks a width per column: the natural width when the table fits,
// otherwise the flexible columns are shrunk — widest first, so they converge
// on similar widths — until the table fits or every one of them has reached
// its minimum.
func (t *table) layout(width int) []int {
	widths := make([]int, len(t.cols))
	for i, c := range t.cols {
		widths[i] = displayWidth(c.header)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], displayWidth(cell))
			}
		}
	}
	if width <= 0 {
		return widths
	}
	natural := append([]int(nil), widths...)
	over := total(widths) - width
	for over > 0 {
		i := widestShrinkable(t.cols, widths)
		if i < 0 {
			break // nothing left to give; the terminal will wrap
		}
		if t.cols[i].wrap == wrapChars {
			next := stepBelow(charSteps(natural[i], t.cols[i].min), widths[i])
			over -= widths[i] - next
			widths[i] = next
			continue
		}
		widths[i]--
		over--
	}
	// A char column gives up a whole step at a time, which can free more than
	// was needed; hand the surplus to the word columns rather than render the
	// table narrower than the terminal.
	for over < 0 {
		i := neediestWordColumn(t.cols, widths, natural)
		if i < 0 {
			break
		}
		widths[i]++
		over++
	}
	return widths
}

// charSteps are the widths a char-wrapped column may take, widest first: its
// longest value laid out over one, two, three or four lines. A width between
// two steps costs budget without saving a line — it only moves where the last
// break falls, which is how a holder ends up spilling a few characters while
// the title beside it holds the room that would have avoided the wrap. Steps
// never go below the column's minimum.
func charSteps(natural, min int) []int {
	steps := make([]int, 0, 4)
	for k := 1; k <= 4; k++ {
		w := max((natural+k-1)/k, min)
		if len(steps) == 0 || w < steps[len(steps)-1] {
			steps = append(steps, w)
		}
	}
	return steps
}

// stepBelow is the widest step narrower than w, or the narrowest step when w
// is already at or below all of them.
func stepBelow(steps []int, w int) int {
	for _, s := range steps {
		if s < w {
			return s
		}
	}
	return steps[len(steps)-1]
}

// neediestWordColumn returns the word-wrapped column furthest below its
// natural width, or -1 when none can use another character. Char columns are
// not grown back: an off-step width is the waste the steps exist to avoid.
func neediestWordColumn(cols []column, widths, natural []int) int {
	best, deficit := -1, 0
	for i, c := range cols {
		if c.wrap != wrapWords || natural[i]-widths[i] <= deficit {
			continue
		}
		best, deficit = i, natural[i]-widths[i]
	}
	return best
}

// widestShrinkable returns the index of the widest column that may still lose
// a character, or -1 when none may.
func widestShrinkable(cols []column, widths []int) int {
	best := -1
	for i, c := range cols {
		if c.wrap == wrapNone || widths[i] <= c.min {
			continue
		}
		if best < 0 || widths[i] > widths[best] {
			best = i
		}
	}
	return best
}

func total(widths []int) int {
	n := colPad * (len(widths) - 1)
	for _, w := range widths {
		n += w
	}
	return n
}

// writeRow prints one row, which becomes several lines when a wrapped cell
// spills. Cells that fit stay on the first line; the rest of their column is
// blank underneath.
func (t *table) writeRow(w io.Writer, widths []int, row []string) {
	lines := make([][]string, len(widths))
	height := 1
	for i := range widths {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		lines[i] = wrapCell(cell, widths[i], t.cols[i].wrap)
		height = max(height, len(lines[i]))
	}
	for l := 0; l < height; l++ {
		var b strings.Builder
		for i := range widths {
			part := ""
			if l < len(lines[i]) {
				part = lines[i][l]
			}
			if i > 0 {
				b.WriteString(strings.Repeat(" ", colPad))
			}
			b.WriteString(part)
			if i < len(widths)-1 {
				b.WriteString(strings.Repeat(" ", max(0, widths[i]-displayWidth(part))))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

// wrapCell breaks one cell into the lines it occupies at the given width.
func wrapCell(s string, width int, mode wrapMode) []string {
	if width <= 0 || displayWidth(s) <= width || mode == wrapNone {
		return []string{s}
	}
	if mode == wrapChars {
		return chunks(s, width)
	}
	return wrapWordsAt(s, width)
}

// wrapWordsAt greedily fills lines with space-separated words. A single word
// wider than the column is hard-split rather than allowed to overflow.
func wrapWordsAt(s string, width int) []string {
	var lines []string
	var cur string
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case displayWidth(cur)+1+displayWidth(word) <= width:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
		if displayWidth(cur) > width {
			parts := chunks(cur, width)
			lines = append(lines, parts[:len(parts)-1]...)
			cur = parts[len(parts)-1]
		}
	}
	if cur != "" || len(lines) == 0 {
		lines = append(lines, cur)
	}
	return lines
}

// chunks splits s into runs of at most width runes.
func chunks(s string, width int) []string {
	var out []string
	runes := []rune(s)
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	return append(out, string(runes))
}

// displayWidth is the column width of s. Rune count, not byte count: task
// titles and board ids ("└ WL-4") carry non-ASCII.
func displayWidth(s string) int { return utf8.RuneCountInString(s) }
