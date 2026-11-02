package critjson

import (
	"fmt"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Options carries what ToRound needs beyond the payload. Anchors set means a
// document review: Body is the document text the sidecar was computed over,
// as GET /api/v1/docs/{id} returns it. Anchors nil means a change review, whose
// threads name Repo. Existing are the review's threads; a comment whose
// anchor already has one is left alone.
type Options struct {
	Anchors  *designdoc.AnchorSidecar
	Body     string
	Repo     string
	Focused  bool
	Existing []model.ReviewThread
}

// Report says what ToRound did with the comments, by comment id.
type Report struct {
	Resolved int      // resolved comments skipped
	Matched  []string // comments whose anchor already has a thread
	Trimmed  []string // spans with heading lines trimmed off
	Split    []string // spans split at a section boundary, "<id>: sec-2, sec-3"
	Notes    []string // review, file or heading-only comments folded into the overall note
}

// ToRound maps one crit finish to one reviewer round (WL-REQ-1776): the
// verdict from Approved, client crit, and one thread with no intent per
// unresolved line comment, which lode-server classifies (WL-REQ-1885).
func ToRound(f Finish, o Options) (model.ReviewRoundInput, Report, error) {
	in := model.ReviewRoundInput{Verdict: "changes_requested", Client: "crit", Focused: o.Focused}
	if f.Approved {
		in.Verdict = "approved"
	}
	var rep Report
	var notes []string
	var doc *docLines
	if o.Anchors != nil {
		doc = newDocLines(o.Body, o.Anchors.Lines)
	}
	for _, c := range f.CommentsJSON {
		if c.Resolved {
			rep.Resolved++
			continue
		}
		body := commentBody(c.Comment)
		if c.Scope != "line" || c.StartLine < 1 {
			note := body
			if c.Path != nil && *c.Path != "" {
				note = *c.Path + ": " + body
			}
			notes = append(notes, note)
			rep.Notes = append(rep.Notes, c.ID)
			continue
		}
		var anchors []model.ReviewAnchor
		var sections []string
		if doc == nil {
			anchors = []model.ReviewAnchor{changeAnchor(c, o.Repo)}
			sections = []string{""}
		} else {
			spans, trimmed, err := doc.spans(c.StartLine, c.EndLine)
			if err != nil {
				return in, rep, fmt.Errorf("comment %s: %w", c.ID, err)
			}
			if trimmed {
				rep.Trimmed = append(rep.Trimmed, c.ID)
			}
			if len(spans) == 0 {
				notes = append(notes, body)
				rep.Notes = append(rep.Notes, c.ID)
				continue
			}
			if len(spans) > 1 {
				var secs []string
				for _, sp := range spans {
					secs = append(secs, sp.anchor)
				}
				rep.Split = append(rep.Split, c.ID+": "+strings.Join(secs, ", "))
			}
			for _, sp := range spans {
				a := model.ReviewAnchor{Doc: o.Anchors.Doc, Version: o.Anchors.Version, ByteStart: sp.start, ByteEnd: sp.end}
				a.Quote = o.Body[sp.start:sp.end]
				// ponytail: crit's quote is taken from the rendered page, so it
				// is used only when it occurs verbatim; quote_offset counts
				// whitespace-collapsed characters and is ignored.
				if i := strings.Index(a.Quote, c.Quote); len(spans) == 1 && c.Quote != "" && i >= 0 {
					a.ByteStart, a.ByteEnd, a.Quote = sp.start+i, sp.start+i+len(c.Quote), c.Quote
				}
				anchors = append(anchors, a)
				sections = append(sections, sp.anchor)
			}
		}
		matched := false
		for i, a := range anchors {
			if hasThread(o.Existing, a, sections[i]) {
				matched = true
				continue
			}
			in.Threads = append(in.Threads, model.OpenThreadInput{Anchor: a, Body: body})
		}
		if matched {
			rep.Matched = append(rep.Matched, c.ID)
		}
	}
	in.OverallNote = strings.Join(notes, "\n\n")
	return in, rep, nil
}

// commentBody is the comment's body with its replies folded in after it, so
// the thread's opening message keeps the whole crit conversation.
func commentBody(c Comment) string {
	b := strings.TrimSpace(c.Body)
	for _, r := range c.Replies {
		who := r.Author
		if who == "" {
			who = "reply"
		}
		b += "\n\n" + who + ": " + strings.TrimSpace(r.Body)
	}
	return b
}

// changeAnchor is a change thread's (repo, path, side, line, anchor_text)
// from a line comment; crit's anchor holds the spanned lines, so its first
// line is the text of start_line. The caller fills blob_sha from git.
func changeAnchor(c ListedComment, repo string) model.ReviewAnchor {
	side := "additions"
	if c.Side == "old" {
		side = "deletions"
	}
	path := ""
	if c.Path != nil {
		path = *c.Path
	}
	text, _, _ := strings.Cut(c.Anchor, "\n")
	return model.ReviewAnchor{Repo: repo, Path: path, Side: side, Line: c.StartLine, AnchorText: text}
}

// hasThread reports whether existing already holds a thread at a. A document
// thread the server moved onto a rule no longer carries the rendered range,
// so document anchors match on section and quote.
func hasThread(existing []model.ReviewThread, a model.ReviewAnchor, section string) bool {
	for _, t := range existing {
		e := t.Anchor
		if a.Repo != "" || a.Path != "" {
			if e.Repo == a.Repo && e.Path == a.Path && e.Side == a.Side && e.Line == a.Line {
				return true
			}
			continue
		}
		if e.DocAnchor == section && e.Quote == a.Quote {
			return true
		}
	}
	return false
}

// docLines indexes a document body by line: where each line starts and ends
// (without its newline), its section anchor and whether it is a heading.
type docLines struct {
	start, end []int
	anchor     []string
	heading    []bool
	body       string
}

func newDocLines(body string, runs []designdoc.AnchorRun) *docLines {
	d := &docLines{body: body}
	off := 0
	for _, ln := range strings.SplitAfter(body, "\n") {
		if ln == "" {
			break
		}
		d.start = append(d.start, off)
		d.end = append(d.end, off+len(strings.TrimSuffix(ln, "\n")))
		off += len(ln)
	}
	d.anchor = make([]string, len(d.start))
	d.heading = make([]bool, len(d.start))
	for _, r := range runs {
		for n := r.From; n <= r.To && n <= len(d.start); n++ {
			d.anchor[n-1], d.heading[n-1] = r.Anchor, r.Heading
		}
	}
	return d
}

type span struct {
	start, end int
	anchor     string
}

func (d *docLines) blank(i int) bool { return strings.TrimSpace(d.body[d.start[i]:d.end[i]]) == "" }

// spans maps 1-based lines from..to onto byte ranges: heading lines are cut
// out (trimmed reports one was), blank lines dropped from either end of each
// range, and the span split where its section changes.
func (d *docLines) spans(from, to int) ([]span, bool, error) {
	if to < from {
		to = from
	}
	if to > len(d.start) {
		return nil, false, fmt.Errorf("lines %d-%d are past the document's %d lines", from, to, len(d.start))
	}
	var out []span
	trimmed := false
	i := from - 1
	for i < to {
		if d.heading[i] {
			trimmed = true
			i++
			continue
		}
		j := i
		for j+1 < to && !d.heading[j+1] && d.anchor[j+1] == d.anchor[i] {
			j++
		}
		a, b := i, j
		for a <= b && d.blank(a) {
			a++
		}
		for b >= a && d.blank(b) {
			b--
		}
		if a <= b {
			out = append(out, span{start: d.start[a], end: d.end[b], anchor: d.anchor[i]})
		}
		i = j + 1
	}
	return out, trimmed, nil
}
