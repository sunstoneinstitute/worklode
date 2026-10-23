package cli

import (
	"bytes"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestInlineRefsRender pins the "Referenced by" block (WL-REQ-1852): one line
// per referring field with its lines, and nothing at all when none refer.
func TestInlineRefsRender(t *testing.T) {
	var buf bytes.Buffer
	InlineRefsRender(&buf, model.InlineRefs{
		Out: []model.InlineRef{{ToRef: "WL-9"}}, // outgoing refs are not printed
	})
	if buf.Len() != 0 {
		t.Errorf("no incoming refs printed %q", buf.String())
	}

	InlineRefsRender(&buf, model.InlineRefs{In: []model.InlineRef{
		{FromRef: "WL-REQ-12", Field: "body", Lines: []int{3, 7}},
		{FromRef: "WL-4", Field: "title", Lines: []int{1}},
		{FromRef: "WL-MILE-1", Field: "title"},
	}})
	want := "\nReferenced by:\n" +
		"  WL-REQ-12  body  line 3, 7\n" +
		"  WL-4  title  line 1\n" +
		"  WL-MILE-1  title\n"
	if got := buf.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
