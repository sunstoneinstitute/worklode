package designdoc

import "github.com/sunstoneinstitute/worklode/internal/model"

// AnchorRun is a run of consecutive body lines that share one section anchor,
// the unit of the sidecar `lode doc show --anchors` writes (WL-REQ-1776).
// From and To are 1-based and inclusive; ByteOffset is the UTF-8 offset of
// the run's first byte in the body. A section's heading line is a run of its
// own with Heading true; text before the first section has an empty Anchor.
type AnchorRun struct {
	From       int    `json:"from"`
	To         int    `json:"to"`
	ByteOffset int    `json:"byte_offset"`
	Anchor     string `json:"anchor"`
	Heading    bool   `json:"heading"`
}

// AnchorRuns splits body into contiguous runs covering every line. A heading
// line starts a section only when it sits outside a fenced block and carries
// an anchor that sections names, so a "## not a heading" inside a fence, or a
// heading the document does not arrange, stays in the surrounding run.
func AnchorRuns(body string, sections []model.DocSection) []AnchorRun {
	known := make(map[string]bool, len(sections))
	for _, s := range sections {
		known[s.Anchor] = true
	}
	starts := map[int]string{} // byte offset of a section heading line -> anchor
	for _, h := range scanHeadings(body) {
		if known[h.anchor] {
			starts[h.start] = h.anchor
		}
	}
	var runs []AnchorRun
	cur := AnchorRun{From: 1}
	open := false
	flush := func() {
		if open {
			runs = append(runs, cur)
		}
		open = false
	}
	for i, ln := range splitLines(body) {
		n := i + 1
		if a, ok := starts[ln.start]; ok {
			flush()
			runs = append(runs, AnchorRun{From: n, To: n, ByteOffset: ln.start, Anchor: a, Heading: true})
			cur = AnchorRun{From: n + 1, ByteOffset: ln.end, Anchor: a}
			continue
		}
		if !open {
			cur.To = n
			open = true
			cur.From, cur.ByteOffset = n, ln.start
			continue
		}
		cur.To = n
	}
	flush()
	return runs
}
