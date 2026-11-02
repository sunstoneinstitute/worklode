package designdoc

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

const anchorRunsBody = "Template intro.\n" +
	"\n" +
	"## 1. First (WL-REQ-1) {#sec-1}\n" +
	"\n" +
	"Rule one, with é.\n" +
	"\n" +
	"```md\n" +
	"## not a heading\n" +
	"```\n" +
	"\n" +
	"## 2. Second (WL-REQ-2) {#sec-2}\n" +
	"Rule two.\n" +
	"\n" +
	"Closing template text, no final newline"

func TestAnchorRuns(t *testing.T) {
	secs := []model.DocSection{{Anchor: "sec-1"}, {Anchor: "sec-2"}}
	runs := AnchorRuns(anchorRunsBody, secs)
	lines := strings.SplitAfter(anchorRunsBody, "\n")

	want := []AnchorRun{
		{From: 1, To: 2, Anchor: ""},
		{From: 3, To: 3, Anchor: "sec-1", Heading: true},
		{From: 4, To: 10, Anchor: "sec-1"},
		{From: 11, To: 11, Anchor: "sec-2", Heading: true},
		{From: 12, To: 14, Anchor: "sec-2"},
	}
	if len(runs) != len(want) {
		t.Fatalf("got %d runs %+v, want %d", len(runs), runs, len(want))
	}
	off := 0
	for i, r := range runs {
		w := want[i]
		if r.From != w.From || r.To != w.To || r.Anchor != w.Anchor || r.Heading != w.Heading {
			t.Errorf("run %d = %+v, want %+v", i, r, w)
		}
		if r.ByteOffset != off {
			t.Errorf("run %d byte_offset = %d, want %d", i, r.ByteOffset, off)
		}
		for _, l := range lines[r.From-1 : r.To] {
			off += len(l)
		}
	}
	if off != len(anchorRunsBody) {
		t.Errorf("runs cover %d bytes, body has %d", off, len(anchorRunsBody))
	}
}
