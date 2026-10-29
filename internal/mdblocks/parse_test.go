package mdblocks_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/mdblocks"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestParseRanges(t *testing.T) {
	md := []byte("# T\n\n- a\n- b\n\n```go\nx\n```\n")
	blocks, err := mdblocks.Parse(md)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ typ, text string }{
		{"heading", "# T"}, {"bulletListItem", "- a"}, {"bulletListItem", "- b"},
		{"codeBlock", "```go\nx\n```"},
	}
	if len(blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d", len(blocks), len(want))
	}
	for i, w := range want {
		got := string(md[blocks[i].Range.Start:blocks[i].Range.End])
		if blocks[i].Type != w.typ || strings.TrimRight(got, "\n") != w.text {
			t.Errorf("block %d: got %s %q, want %s %q", i, blocks[i].Type, got, w.typ, w.text)
		}
	}
}

// TestParseConstructs checks one input per construct of WL-REQ-1871: the
// single top-level block's type, its range sliced back out of the input, and
// its props. Out-of-profile constructs become `source` with the verbatim bytes.
func TestParseConstructs(t *testing.T) {
	cases := []struct {
		name, md, typ, text, props string
	}{
		{"paragraph", "Hello world\n", "paragraph", "Hello world", ""},
		{"heading 1", "# One\n", "heading", "# One", `{"level":1}`},
		{"heading 6", "###### Six\n", "heading", "###### Six", `{"level":6}`},
		{"bullet", "- item\n", "bulletListItem", "- item", ""},
		{"numbered", "1. item\n", "numberedListItem", "1. item", ""},
		{"numbered start", "3. item\n", "numberedListItem", "3. item", `{"start":3}`},
		{"check open", "- [ ] todo\n", "checkListItem", "- [ ] todo", `{"checked":false}`},
		{"check done", "- [x] done\n", "checkListItem", "- [x] done", `{"checked":true}`},
		{"table", "| a | b |\n|---|---|\n| 1 | 2 |\n", "table", "| a | b |\n|---|---|\n| 1 | 2 |", ""},
		{"fenced code", "```go\nx := 1\n```\n", "codeBlock", "```go\nx := 1\n```", `{"language":"go"}`},
		{"fenced empty", "```\n```\n", "codeBlock", "```\n```", ""},
		{"tilde fence", "~~~~py\na\n~~~~\n", "codeBlock", "~~~~py\na\n~~~~", `{"language":"py"}`},
		{"mermaid", "```{mermaid}\ngraph TD\n```\n", "codeBlock", "```{mermaid}\ngraph TD\n```", `{"language":"mermaid"}`},
		{"quote", "> quoted\n", "quote", "> quoted", ""},
		{"image", "![A cat](/blob/abc)\n", "image", "![A cat](/blob/abc)", `{"caption":"A cat","url":"/blob/abc"}`},

		{"raw html", "<div>\nhi\n</div>\n", "source", "<div>\nhi\n</div>", ""},
		{"placeholder in prose", "GET /docs/<id> returns it\n", "source", "GET /docs/<id> returns it", ""},
		{"indented code", "    code here\n", "source", "    code here", ""},
		{"multi-line code span", "a `code\nspan` b\n", "source", "a `code\nspan` b", ""},
		{"setext heading", "Title\n=====\n", "source", "Title\n=====", ""},
		{"thematic break", "---\n", "source", "---", ""},
		{"definition list", "Term\n: Definition\n", "source", "Term\n: Definition", ""},
		{"footnote", "[^1]: A note.\n", "source", "[^1]: A note.", ""},
		{"fence in a quote", "> ```\n> x\n> ```\n", "source", "> ```\n> x\n> ```", ""},
		{"no final newline", "<!-- c -->", "source", "<!-- c -->", ""},
		{"quote of two paragraphs", "> a\n>\n> b\n", "source", "> a\n>\n> b", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			md := []byte(c.md)
			blocks, err := mdblocks.Parse(md)
			if err != nil {
				t.Fatal(err)
			}
			if len(blocks) != 1 {
				t.Fatalf("got %d blocks, want 1: %+v", len(blocks), blocks)
			}
			b := blocks[0]
			got := string(md[b.Range.Start:b.Range.End])
			if b.Type != c.typ || got != c.text {
				t.Errorf("got %s %q, want %s %q", b.Type, got, c.typ, c.text)
			}
			if err := b.Validate(); err != nil {
				t.Error(err)
			}
			wantProps := c.props
			if c.typ == "source" {
				p, _ := json.Marshal(map[string]string{"markdown": c.text})
				wantProps = string(p)
			}
			if string(b.Props) != wantProps {
				t.Errorf("props = %s, want %s", b.Props, wantProps)
			}
		})
	}
}

func TestParseFootnoteOrder(t *testing.T) {
	md := []byte("[^a]: one\n\nsee[^b]\n\n[^b]: two\n")
	blocks, err := mdblocks.Parse(md)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range blocks {
		if b.Type != "source" {
			t.Errorf("%q is %s, want source", md[b.Range.Start:b.Range.End], b.Type)
		}
		got = append(got, string(md[b.Range.Start:b.Range.End]))
	}
	if want := []string{"[^a]: one", "see[^b]", "[^b]: two"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseNestedList(t *testing.T) {
	md := []byte("- a\n  - b\n  - c\n- d\n")
	blocks, err := mdblocks.Parse(md)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || len(blocks[0].Children) != 2 {
		t.Fatalf("got %+v", blocks)
	}
	slice := func(b model.EditorBlock) string { return string(md[b.Range.Start:b.Range.End]) }
	for _, w := range []struct {
		b    model.EditorBlock
		text string
	}{
		{blocks[0], "- a\n  - b\n  - c"},
		{blocks[0].Children[0], "- b"},
		{blocks[0].Children[1], "- c"},
		{blocks[1], "- d"},
	} {
		if got := slice(w.b); got != w.text {
			t.Errorf("got %q, want %q", got, w.text)
		}
	}
	item := []byte("- a\n\n  ```\n  x\n  ```\n")
	got, err := mdblocks.Parse(item)
	if err != nil {
		t.Fatal(err)
	}
	if r := got[0].Range; string(item[r.Start:r.End]) != "- a\n\n  ```\n  x\n  ```" || got[0].Children[0].Type != "codeBlock" {
		t.Errorf("item with code = %q %+v", item[r.Start:r.End], got[0].Children)
	}
	if c := blocks[0].Content; len(c) != 1 || c[0].Text != "a" || c[0].Offset != 2 {
		t.Errorf("content = %+v, want one run \"a\" at 2", c)
	}
}

func TestParseTableContent(t *testing.T) {
	md := []byte("| a | b |\n|---|---|\n| 1 | 2 |\n")
	blocks, err := mdblocks.Parse(md)
	if err != nil {
		t.Fatal(err)
	}
	tc := blocks[0].TableContent
	if tc == nil || len(tc.Rows) != 2 || len(tc.Rows[1].Cells) != 2 || len(tc.ColumnWidths) != 2 {
		t.Fatalf("table content = %+v", tc)
	}
	if cell := tc.Rows[1].Cells[1].Content; len(cell) != 1 || cell[0].Text != "2" {
		t.Errorf("cell = %+v, want \"2\"", cell)
	}
	if tc.HeaderRows == nil || *tc.HeaderRows != 1 {
		t.Errorf("headerRows = %v, want 1", tc.HeaderRows)
	}
}
