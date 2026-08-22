// Document-reference autolinking (WL-301): plain-text mentions of a design
// document become links to the cockpit's resolving redirect, /docs/ref/<ref>,
// which sends the browser to /docs/<id> and lets it carry the #sec fragment
// across the redirect. Three spellings are linked — the 025 §14.3 shorthand
// (WL-SPEC-42, with an optional #sec-10), the keyword form ("spec 042 §10",
// "ADR 048"), and the bare corpus form ("025 §14.3") — because those are the
// ways the corpus actually writes references. Bare task ids (WL-129) are
// deliberately not linked here: the same token shape spells UTF-8, SHA-256
// and ISO-8601, and a renderer with no store access cannot tell a task from
// an acronym; that linkification is tracked separately.
//
// The transformer runs post-parse, skipping links, autolinks, images and
// code spans, so a reference inside a code fence or an explicit link is left
// exactly as written. Goldmark splits a line of prose into several adjacent
// Text nodes on inline-trigger bytes, so matching runs over each contiguous
// run of sibling text nodes rather than per node — the segments are
// contiguous in the source, which is what makes the run one byte span.

package mdrender

import (
	"regexp"

	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// docRefPrefix is where a linked reference points: the cockpit's resolving
// redirect (internal/api's docRefRedirect).
const docRefPrefix = "/docs/ref/"

// The three reference spellings, tried in order. A trailing sentence dot is
// trimmed off a match in code, not in pattern, so "025 §14.3." links as
// "025 §14.3".
var (
	// WL-SPEC-42, WL-ADR-7, optionally #sec-10 / #sec-3.1a.
	shorthandRef = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-(?:SPEC|ADR)-\d+(?:#sec-[0-9A-Za-z._-]+)?`)
	// spec 042 §10, ADR 048 §2, Spec 25 — keyword, number, optional §.
	keywordRef = regexp.MustCompile(`\b(?:[Ss]pec|ADR|[Aa]dr)\s(\d{1,4})(?:\s?§\s?([0-9][0-9A-Za-z.]*))?`)
	// 025 §14.3 — a bare number only when the § makes it unmistakably a ref.
	bareRef = regexp.MustCompile(`\b(\d{1,4})\s?§\s?([0-9][0-9A-Za-z.]*)`)
)

// docRefLinker is the AST transformer; withDocRefLinks installs it on both
// flavours' parsers.
type docRefLinker struct{}

var withDocRefLinks = parser.WithASTTransformers(util.Prioritized(docRefLinker{}, 900))

func (docRefLinker) Transform(doc *gast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	// Collect the runs first, mutate after: replacing nodes mid-walk would
	// strand the walker on removed nodes' sibling pointers.
	var runs [][]*gast.Text
	_ = gast.Walk(doc, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		switch n.Kind() {
		case gast.KindLink, gast.KindAutoLink, gast.KindImage, gast.KindCodeSpan:
			return gast.WalkSkipChildren, nil
		}
		if n.HasChildren() {
			runs = append(runs, textRuns(n, source)...)
		}
		return gast.WalkContinue, nil
	})
	for _, run := range runs {
		linkifyRun(run, source)
	}
}

// textRuns groups parent's consecutive Text children into contiguous single-
// line runs: adjacent segments with no line break between them. A break, a
// gap, or a non-text sibling ends the run.
func textRuns(parent gast.Node, source []byte) [][]*gast.Text {
	var runs [][]*gast.Text
	var run []*gast.Text
	flush := func() {
		if len(run) > 0 {
			runs = append(runs, run)
			run = nil
		}
	}
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		t, ok := c.(*gast.Text)
		if !ok || c.Kind() != gast.KindText {
			flush()
			continue
		}
		if len(run) > 0 && run[len(run)-1].Segment.Stop != t.Segment.Start {
			flush()
		}
		run = append(run, t)
		if t.SoftLineBreak() || t.HardLineBreak() {
			flush()
		}
	}
	flush()
	return runs
}

// refMatch is one located reference in a run's byte span: its offsets within
// the span and the href it links to.
type refMatch struct {
	start, end int
	href       string
}

// trimDot drops a sentence-final dot from a match end: the section grammar
// ends on an alphanumeric, so "sec-14.3." always means "sec-14.3" plus
// punctuation.
func trimDot(value []byte, end int) int {
	for end > 0 && value[end-1] == '.' {
		end--
	}
	return end
}

// findRefs locates every reference in value, earlier patterns winning where
// matches overlap, returned in document order.
func findRefs(value []byte) []refMatch {
	var out []refMatch
	taken := func(s, e int) bool {
		for _, m := range out {
			if s < m.end && e > m.start {
				return true
			}
		}
		return false
	}
	for _, loc := range shorthandRef.FindAllIndex(value, -1) {
		end := trimDot(value, loc[1])
		out = append(out, refMatch{loc[0], end, docRefPrefix + string(value[loc[0]:end])})
	}
	section := func(loc []int, numStart, numEnd, secStart, secEnd int) refMatch {
		end := loc[1]
		href := docRefPrefix + string(value[numStart:numEnd])
		if secStart >= 0 {
			end = trimDot(value, end)
			href = docRefPrefix + string(value[numStart:numEnd]) + "#sec-" + string(value[secStart:min(secEnd, end)])
		}
		return refMatch{loc[0], end, href}
	}
	for _, loc := range keywordRef.FindAllSubmatchIndex(value, -1) {
		if taken(loc[0], loc[1]) {
			continue
		}
		out = append(out, section(loc, loc[2], loc[3], loc[4], loc[5]))
	}
	for _, loc := range bareRef.FindAllSubmatchIndex(value, -1) {
		if taken(loc[0], loc[1]) {
			continue
		}
		out = append(out, section(loc, loc[2], loc[3], loc[4], loc[5]))
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].start < out[j-1].start; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// linkifyRun splits one contiguous text run around its references, replacing
// the run's nodes with text–link–text siblings. Segment arithmetic keeps
// every new node pointing into the original source, so nothing is copied or
// re-escaped.
func linkifyRun(run []*gast.Text, source []byte) {
	first, last := run[0], run[len(run)-1]
	parent := first.Parent()
	if parent == nil {
		return
	}
	start, stop := first.Segment.Start, last.Segment.Stop
	refs := findRefs(source[start:stop])
	if len(refs) == 0 {
		return
	}

	insert := func(n gast.Node) { parent.InsertBefore(parent, first, n) }
	pos := 0
	for _, m := range refs {
		if m.start > pos {
			insert(gast.NewTextSegment(text.NewSegment(start+pos, start+m.start)))
		}
		link := gast.NewLink()
		link.Destination = []byte(m.href)
		link.AppendChild(link, gast.NewTextSegment(text.NewSegment(start+m.start, start+m.end)))
		insert(link)
		pos = m.end
	}
	// The run's final break flags belong after the last chunk, even when a
	// reference ends the line — an empty tail carries them so the break
	// still renders.
	tail := gast.NewTextSegment(text.NewSegment(start+pos, stop))
	tail.SetSoftLineBreak(last.SoftLineBreak())
	tail.SetHardLineBreak(last.HardLineBreak())
	insert(tail)
	for _, n := range run {
		parent.RemoveChild(parent, n)
	}
}
