package critjson_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/review/critjson"
)

// ingestBody puts sec-2's text on lines 412..414 behind 409 lines of
// template preamble, so the WL-REQ-1776 example span is real.
func ingestBody() (body string, lineStart []int) {
	var b strings.Builder
	b.WriteString("# Spec\n")
	for i := 2; i <= 409; i++ {
		b.WriteString("preamble\n")
	}
	b.WriteString("## 1. One {#sec-1}\n")   // 410
	b.WriteString("## 2. Two {#sec-2}\n")   // 411
	b.WriteString("alpha line\n")           // 412
	b.WriteString("beta line\n")            // 413
	b.WriteString("gamma line\n")           // 414
	b.WriteString("## 3. Three {#sec-3}\n") // 415
	b.WriteString("delta line\n")           // 416
	body = b.String()
	off := 0
	lineStart = []int{0} // 1-based
	for _, ln := range strings.SplitAfter(body, "\n") {
		lineStart = append(lineStart, off)
		off += len(ln)
	}
	return body, lineStart
}

func lineComment(id string, from, to int, quote string) critjson.ListedComment {
	path := "spec.md"
	return critjson.ListedComment{Scope: "line", Path: &path,
		Comment: critjson.Comment{ID: id, StartLine: from, EndLine: to, Body: "body of " + id, Quote: quote}}
}

func TestIngestMapsFixture(t *testing.T) {
	body, ls := ingestBody()
	sc := &designdoc.AnchorSidecar{Doc: 7, Version: 3, Lines: designdoc.AnchorRuns(body,
		[]model.DocSection{{Anchor: "sec-1"}, {Anchor: "sec-2"}, {Anchor: "sec-3"}})}
	f := critjson.Finish{Approved: false, CommentsJSON: []critjson.ListedComment{
		lineComment("span", 412, 414, ""),
		lineComment("quoted", 413, 413, "beta"),
		lineComment("heading", 411, 412, ""),
		lineComment("cross", 414, 416, ""),
		{Scope: "review", Comment: critjson.Comment{ID: "overall", Body: "Overall fine."}},
		{Scope: "line", Comment: critjson.Comment{ID: "done", StartLine: 412, EndLine: 412, Body: "x", Resolved: true}},
	}}
	in, rep, err := critjson.ToRound(f, critjson.Options{Anchors: sc, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if in.Verdict != "changes_requested" || in.Client != "crit" || in.OverallNote != "Overall fine." {
		t.Errorf("round = %+v", in)
	}
	endOf := func(n int) int { return ls[n+1] - 1 }
	want := []model.ReviewAnchor{
		{ByteStart: ls[412], ByteEnd: endOf(414), Quote: "alpha line\nbeta line\ngamma line"},
		{ByteStart: ls[413], ByteEnd: ls[413] + 4, Quote: "beta"},
		{ByteStart: ls[412], ByteEnd: endOf(412), Quote: "alpha line"},
		{ByteStart: ls[414], ByteEnd: endOf(414), Quote: "gamma line"},
		{ByteStart: ls[416], ByteEnd: endOf(416), Quote: "delta line"},
	}
	if len(in.Threads) != len(want) {
		t.Fatalf("threads = %+v, want %d", in.Threads, len(want))
	}
	for i, w := range want {
		th := in.Threads[i]
		w.Doc, w.Version = 7, 3
		if th.Anchor != w {
			t.Errorf("thread %d anchor = %+v, want %+v", i, th.Anchor, w)
		}
		if th.Intent != "" || th.Body == "" {
			t.Errorf("thread %d intent %q body %q: want no intent and a body", i, th.Intent, th.Body)
		}
		if got := body[th.Anchor.ByteStart:th.Anchor.ByteEnd]; got != th.Anchor.Quote {
			t.Errorf("thread %d quote %q is not its bytes %q", i, th.Anchor.Quote, got)
		}
	}
	if rep.Resolved != 1 || strings.Join(rep.Trimmed, ",") != "heading,cross" ||
		strings.Join(rep.Split, ";") != "cross: sec-2, sec-3" || strings.Join(rep.Notes, ",") != "overall" {
		t.Errorf("report = %+v", rep)
	}

	// Re-running against the threads the server opened opens nothing.
	var existing []model.ReviewThread
	for _, th := range in.Threads {
		a := th.Anchor
		a.DocAnchor = "sec-2"
		if a.Quote == "delta line" {
			a.DocAnchor = "sec-3"
		}
		existing = append(existing, model.ReviewThread{Anchor: a})
	}
	again, rep, err := critjson.ToRound(f, critjson.Options{Anchors: sc, Body: body, Existing: existing})
	if err != nil || len(again.Threads) != 0 || len(rep.Matched) != 4 {
		t.Errorf("re-run: threads %+v, report %+v, err %v", again.Threads, rep, err)
	}

	if _, _, err := critjson.ToRound(critjson.Finish{CommentsJSON: []critjson.ListedComment{lineComment("far", 500, 501, "")}},
		critjson.Options{Anchors: sc, Body: body}); err == nil || !strings.Contains(err.Error(), "far") {
		t.Errorf("out-of-range comment: err = %v, want it named", err)
	}
}

func TestIngestChangeAndApproved(t *testing.T) {
	in, _, err := critjson.ToRound(decode(t, "finish_unresolved.json"), critjson.Options{Repo: "demo", Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	want := model.ReviewAnchor{Repo: "demo", Path: "main.go", Side: "additions", Line: 6, AnchorText: "\tfmt.Println(\"hello\")"}
	if in.Verdict != "changes_requested" || !in.Focused || len(in.Threads) != 1 || in.Threads[0].Anchor != want ||
		in.OverallNote != "Overall fine, one change." {
		t.Errorf("change round = %+v", in)
	}
	in, rep, err := critjson.ToRound(decode(t, "finish_approved.json"), critjson.Options{Repo: "demo"})
	if err != nil || in.Verdict != "approved" || len(in.Threads) != 0 || rep.Resolved != 1 {
		t.Errorf("approved round = %+v, report %+v, err %v", in, rep, err)
	}
}
