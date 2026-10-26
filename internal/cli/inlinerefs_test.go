package cli

import (
	"bytes"
	"flag"
	"os"
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

// TestRefStatsTable pins the quality report's layout against a golden file
// (WL-REQ-1859); regenerate with -update-ref-stats.
func TestRefStatsTable(t *testing.T) {
	var buf bytes.Buffer
	RefStatsTable(&buf, model.RefStats{Rows: []model.RefStatsRow{
		{Classifier: "cue", ClassifierVersion: 1, Predicate: "references", ConfidenceBucket: 0.9, Kept: 3, KeptRate: 1},
		{Classifier: "jev", ClassifierVersion: 2, Predicate: "blockedBy", ConfidenceBucket: 0.7, Kept: 2, Rejected: 1, KeptRate: 2.0 / 3, RejectedRate: 1.0 / 3},
	}})
	const golden = "testdata/refstats.golden"
	if *updateRefStats {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != string(want) {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
	buf.Reset()
	RefStatsTable(&buf, model.RefStats{})
	if buf.String() != "No classifications.\n" {
		t.Errorf("empty = %q", buf.String())
	}
}

var updateRefStats = flag.Bool("update-ref-stats", false, "regenerate testdata/refstats.golden")
