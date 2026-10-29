package mdblocks_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/mdblocks"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

var update = flag.Bool("update", false, "rewrite testdata/golden")

// goldenSources are the bodies whose blocks web/app/src/editor/contract.test.ts
// loads into BlockNote 0.56 (WL-REQ-1867). profile.md carries heading, callout
// and image, which the corpus lacks; the corpus files add a quote, a table
// with code, source blocks and nested lists.
var goldenSources = map[string]string{
	"profile":     "testdata/profile.md",
	"WL-REQ-2":    "testdata/corpus/WL-REQ-2.md",
	"WL-REQ-95":   "testdata/corpus/WL-REQ-95.md",
	"WL-REQ-187":  "testdata/corpus/WL-REQ-187.md",
	"WL-REQ-211":  "testdata/corpus/WL-REQ-211.md",
	"WL-REQ-1782": "testdata/corpus/WL-REQ-1782.md",
	"WL-1374":     "testdata/corpus/WL-1374.md",
	"WL-1376":     "testdata/corpus/WL-1376.md",
	"WL-1379":     "testdata/corpus/WL-1379.md",
	"WL-1386":     "testdata/corpus/WL-1386.md",
	"WL-1402":     "testdata/corpus/WL-1402.md",
}

// subject picks the body's subject from its fixture name.
func subject(name string) string {
	switch {
	case name == "profile":
		return "doc"
	case strings.HasPrefix(name, "WL-REQ-"):
		return "rule"
	default:
		return "task"
	}
}

// TestGolden writes, under -update, the blocks of each golden source as
// served (normalised, then parsed), and otherwise fails on a stale file.
func TestGolden(t *testing.T) {
	seen := map[string]bool{}
	for name, path := range goldenSources {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		blocks, err := mdblocks.Parse(mdblocks.Normalize(src), mdblocks.Subject(subject(name)))
		if err != nil {
			t.Fatal(err)
		}
		markTypes(blocks, seen)
		got, err := json.MarshalIndent(model.BlocksBody{Blocks: blocks}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		file := filepath.Join("testdata", "golden", name+".blocks.json")
		if *update {
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%v (run go test -trimpath ./internal/mdblocks -run TestGolden -update)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale (run go test -trimpath ./internal/mdblocks -run TestGolden -update):\n%s", file, lineDiff(string(want), string(got)))
		}
	}
	for _, typ := range []string{"paragraph", "heading", "bulletListItem", "numberedListItem", "checkListItem",
		"table", "codeBlock", "quote", "callout", "image", "source"} {
		if !seen[typ] {
			t.Errorf("no golden source has a %s block", typ)
		}
	}
}

func markTypes(blocks []model.EditorBlock, seen map[string]bool) {
	for _, b := range blocks {
		seen[b.Type] = true
		markTypes(b.Children, seen)
	}
}
