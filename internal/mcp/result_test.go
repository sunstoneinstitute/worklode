package mcp

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ints(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

func TestPageOf(t *testing.T) {
	for _, c := range []struct {
		n, first int
		more     bool
	}{{0, 0, false}, {10, 10, false}, {11, 10, true}, {25, 10, true}} {
		got, next, err := pageOf(ints(c.n), "", "s")
		if err != nil || len(got) != c.first || (next != "") != c.more {
			t.Errorf("n=%d: len=%d next=%q err=%v", c.n, len(got), next, err)
		}
	}
}

func TestPageCursorRoundTrip(t *testing.T) {
	items := ints(25)
	var all []int
	cur := ""
	for i := 0; i < 5; i++ {
		got, next, err := pageOf(items, cur, "s")
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, got...)
		if next == "" {
			break
		}
		cur = next
	}
	if len(all) != 25 || all[24] != 24 {
		t.Fatalf("got %v", all)
	}
}

func TestPageForgedCursor(t *testing.T) {
	_, next, _ := pageOf(ints(25), "", "s")
	for _, c := range []string{"10", "10.", "10.deadbeef", "x.y", "-1.aa", "20" + next[2:], next + "0"} {
		if _, _, err := pageOf(ints(25), c, "s"); err != errInvalidCursor {
			t.Errorf("%q: err=%v", c, err)
		}
	}
	if _, _, err := pageOf(ints(25), next, "other"); err != errInvalidCursor {
		t.Errorf("other secret: err=%v", err)
	}
}

func TestSay(t *testing.T) {
	text := func(r *mcp.CallToolResult, i int) string { return r.Content[i].(*mcp.TextContent).Text }
	r := say("Done.", nil)
	if len(r.Content) != 1 || text(r, 0) != "Done." {
		t.Fatalf("%+v", r)
	}
	if r = say("Two tasks.", "table"); len(r.Content) != 2 || text(r, 1) != "table" {
		t.Fatalf("%+v", r)
	}
	if r = say("One.", map[string]int{"a": 1}); text(r, 1) != "{\n  \"a\": 1\n}" {
		t.Fatalf("%q", text(r, 1))
	}
}
