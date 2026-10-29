package mdblocks_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/mdblocks"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"paragraph joins lines", "Hello\nworld\n", "Hello world\n"},
		{"blocks", "# T\n\npara\n", "# T\n\npara\n"},
		{"list is tight", "* a\n\n* b\n\npara\n", "- a\n- b\n\npara\n"},
		{"numbered", "1. a\n1. b\n", "1. a\n2. b\n"},
		{"numbered start", "3. a\n4. b\n", "3. a\n4. b\n"},
		{"check", "- [ ] a\n- [x] b\n", "- [ ] a\n- [x] b\n"},
		{"nested", "- a\n    - b\n- c\n", "- a\n  - b\n- c\n"},
		{"nested under numbered", "1. a\n   - b\n", "1. a\n   - b\n"},
		{"item with code", "- a\n\n  ```\n  x\n\n  y\n  ```\n", "- a\n\n  ```\n  x\n\n  y\n  ```\n"},
		{"emphasis", "_i_ and __b__ and ~~s~~ and `c`\n", "*i* and **b** and ~~s~~ and `c`\n"},
		{"nested emphasis", "**bold _nested_ text** ***bi*** *a **b***\n", "**bold *nested* text** ***bi*** *a **b***\n"},
		{"escapes kept", "a \\* b &amp; c\n", "a \\* b &amp; c\n"},
		{"code span with backtick", "`` a`b ``\n", "``a`b``\n"},
		{"link", "[a **b**](http://x) <http://y> http://z.io\n", "[a **b**](http://x) <http://y> <http://z.io>\n"},
		{"emoji", "a :tada: b\n", "a 🎉 b\n"},
		{"fence", "~~~\na\n```\n~~~\n", "````\na\n```\n````\n"},
		{"mermaid", "```{mermaid}\ngraph TD\n```\n", "```{mermaid}\ngraph TD\n```\n"},
		{"quote", "> q\nr\n", "> q r\n"},
		{"image", "![A](u)\n", "![A](u)\n"},
		{"table", "|a|b|\n|:-|-:|\n|1|2|\n", "| a | b |\n|---|---|\n| 1 | 2 |\n"},
		{"callout", "::: {.callout-note}\nSee\n*this*.\n:::\n", "::: {.callout-note}\nSee *this*.\n:::\n"},
		{"source", "<div>\nhi\n</div>\n\nx\n", "<div>\nhi\n</div>\n\nx\n"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(mdblocks.Normalize([]byte(c.in))); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestWriteAnchorAndErrors(t *testing.T) {
	blocks, _ := mdblocks.Parse([]byte("## Title {#purpose}\n"), mdblocks.Subject("doc"))
	got, err := mdblocks.Write(blocks)
	if err != nil || string(got) != "## Title {#purpose}\n" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := mdblocks.Write([]model.EditorBlock{{Type: "video"}}); err == nil {
		t.Error("unknown block type: want an error")
	}
}

func corpus(t *testing.T) []string {
	files, _ := filepath.Glob("testdata/corpus/*.md")
	if len(files) == 0 {
		t.Fatal("corpus fixture missing")
	}
	return files
}

// TestCorpusRoundTrip is WL-REQ-1867's check: normalising a normalised body
// changes nothing.
func TestCorpusRoundTrip(t *testing.T) {
	for _, f := range corpus(t) {
		src, _ := os.ReadFile(f)
		once := mdblocks.Normalize(src)
		twice := mdblocks.Normalize(once)
		if !bytes.Equal(once, twice) {
			t.Errorf("%s: second round trip differs:\n%s", f, lineDiff(string(once), string(twice)))
		}
	}
}

// TestCorpusSameBlocks checks normalising keeps a body's blocks. Ids, ranges
// and run offsets shift with the rewrite, so they are ignored. So are the
// run boundaries and soft line breaks the writer removes: adjacent runs with
// the same styles are merged and "\n" in a run compares as a space.
func TestCorpusSameBlocks(t *testing.T) {
	sources := 0
	for _, f := range corpus(t) {
		src, _ := os.ReadFile(f)
		a, _ := mdblocks.Parse(src)
		b, _ := mdblocks.Parse(mdblocks.Normalize(src))
		ja, jb := comparable(a), comparable(b)
		if ja != jb {
			t.Errorf("%s: blocks differ:\n%s", f, lineDiff(ja, jb))
		}
		if hasSource(a) {
			sources++
		}
	}
	t.Logf("%d corpus bodies hold a source block", sources)
}

func hasSource(blocks []model.EditorBlock) bool {
	for _, b := range blocks {
		if b.Type == "source" || hasSource(b.Children) {
			return true
		}
	}
	return false
}

func comparable(blocks []model.EditorBlock) string {
	b, _ := json.MarshalIndent(strip(blocks), "", " ")
	return string(b)
}

func strip(blocks []model.EditorBlock) []model.EditorBlock {
	out := make([]model.EditorBlock, len(blocks))
	for i, b := range blocks {
		b.ID, b.Range = "", model.ByteRange{}
		if b.Type != "codeBlock" {
			b.Content = runs(b.Content)
		} else {
			b.Content = append([]model.InlineContent(nil), b.Content...)
			for j := range b.Content {
				b.Content[j].Offset = 0
			}
		}
		if b.TableContent != nil {
			tc := *b.TableContent
			tc.Rows = nil
			for _, r := range b.TableContent.Rows {
				var nr model.TableRow
				for _, c := range r.Cells {
					c.Content = runs(c.Content)
					nr.Cells = append(nr.Cells, c)
				}
				tc.Rows = append(tc.Rows, nr)
			}
			b.TableContent = &tc
		}
		b.Children = strip(b.Children)
		out[i] = b
	}
	return out
}

func runs(rs []model.InlineContent) []model.InlineContent {
	var out []model.InlineContent
	for _, r := range rs {
		r.Offset = 0
		r.Text = strings.ReplaceAll(r.Text, "\n", " ")
		r.Content = runs(r.Content)
		if l := len(out) - 1; l >= 0 && r.Type == "text" && out[l].Type == "text" && reflect.DeepEqual(out[l].Styles, r.Styles) {
			out[l].Text += r.Text
			continue
		}
		out = append(out, r)
	}
	return out
}

// lineDiff names the first line that differs.
func lineDiff(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(la), len(lb)) {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			return "line " + itoa(i+1) + ":\n- " + x + "\n+ " + y
		}
	}
	return ""
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }
