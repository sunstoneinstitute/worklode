package mdblocks_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/mdblocks"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestInlineOffsets(t *testing.T) {
	md := []byte("a **b** [c](u)\n")
	blocks, _ := mdblocks.Parse(md)
	runs := blocks[0].Content
	// "a " at 0, "b" at 4 (after "a **"), " " at 7, link text "c" at 9 (after "a **b** [")
	if len(runs) != 4 || runs[3].Type != "link" {
		t.Fatalf("runs = %+v, want text, text, text, link", runs)
	}
	for i, want := range []int{0, 4, 7, 9} {
		if runs[i].Offset != want {
			t.Errorf("run %d offset %d, want %d", i, runs[i].Offset, want)
		}
	}
	if runs[3].Content[0].Offset != 9 {
		t.Errorf("link text offset %d, want 9", runs[3].Content[0].Offset)
	}
}

// TestInlineRuns checks the runs each inline construct of WL-REQ-1871 becomes.
func TestInlineRuns(t *testing.T) {
	cases := []struct{ md, want string }{
		{"*i* _j_\n", `[{"type":"text","text":"i","styles":{"italic":true},"offset":1},{"type":"text","text":" ","offset":3},{"type":"text","text":"j","styles":{"italic":true},"offset":5}]`},
		{"**b**\n", `[{"type":"text","text":"b","styles":{"bold":true},"offset":2}]`},
		{"***bi***\n", `[{"type":"text","text":"bi","styles":{"bold":true,"italic":true},"offset":3}]`},
		{"~~s~~\n", `[{"type":"text","text":"s","styles":{"strike":true},"offset":2}]`},
		{"`c`\n", `[{"type":"text","text":"c","styles":{"code":true},"offset":1}]`},
		{"[**t**](http://x)\n", `[{"type":"link","href":"http://x","content":[{"type":"text","text":"t","styles":{"bold":true},"offset":3}],"offset":3}]`},
		{"<http://x>\n", `[{"type":"link","href":"http://x","content":[{"type":"text","text":"http://x","offset":1}],"offset":1}]`},
		{"see http://x.io now\n", `[{"type":"text","text":"see ","offset":0},{"type":"link","href":"http://x.io","content":[{"type":"text","text":"http://x.io","offset":4}],"offset":4},{"type":"text","text":" now","offset":15}]`},
		{"a :tada: b\n", `[{"type":"text","text":"a ","offset":0},{"type":"text","text":"🎉","offset":2},{"type":"text","text":" b","offset":8}]`},
		{"a\nb\n", `[{"type":"text","text":"a\nb","offset":0}]`},
	}
	for _, c := range cases {
		blocks, err := mdblocks.Parse([]byte(c.md))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := json.Marshal(blocks[0].Content)
		if string(got) != c.want {
			t.Errorf("%q:\n got %s\nwant %s", c.md, got, c.want)
		}
	}
}

// TestInlineSourceMap maps every character of every run back to the source:
// offset + byte length of the run's preceding characters is the character's
// position. An emoji run's one character maps to the shortcode's colon.
func TestInlineSourceMap(t *testing.T) {
	md := []byte("# A *b* `c`\n\n" +
		"Some **bold _nested_** and ~~gone~~ text with [a **link**](http://x) and :tada: end\n" +
		"second line `code` <http://y>  \nhard break\n\n" +
		"- item *one* :rocket:\n- [x] done **two**\n\n" +
		"> quoted [x](y) `z`\n\n" +
		"| h *1* | h2 |\n|---|---|\n| **c** | :tada:x |\n")
	blocks, err := mdblocks.Parse(md)
	if err != nil {
		t.Fatal(err)
	}
	var runs []model.InlineContent
	var collect func(rs []model.InlineContent)
	collect = func(rs []model.InlineContent) {
		for _, r := range rs {
			if r.Type == "link" {
				collect(r.Content)
				continue
			}
			runs = append(runs, r)
		}
	}
	for _, b := range blocks {
		if b.Type == "source" {
			t.Fatalf("unexpected source block %s", md[b.Range.Start:b.Range.End])
		}
		collect(b.Content)
		if b.TableContent != nil {
			for _, row := range b.TableContent.Rows {
				for _, c := range row.Cells {
					collect(c.Content)
				}
			}
		}
	}
	emoji := 0
	for _, r := range runs {
		if r.Text == "🎉" || r.Text == "🚀" {
			emoji++
			if md[r.Offset] != ':' {
				t.Errorf("emoji %q at %d: source %q", r.Text, r.Offset, md[r.Offset:])
			}
			continue
		}
		for i := range len(r.Text) {
			if md[r.Offset+i] != r.Text[i] {
				t.Errorf("run %q at %d: byte %d is %q in source", r.Text, r.Offset, i, md[r.Offset+i])
				break
			}
		}
	}
	if emoji != 3 {
		t.Errorf("found %d emoji runs, want 3", emoji)
	}
	if !strings.Contains(string(md), "  \nhard") {
		t.Fatal("fixture lost its hard break")
	}
}
