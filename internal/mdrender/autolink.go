// Reference autolinking (WL-301, WL-305): plain-text mentions of a design
// document become links to the cockpit's resolving redirect, /docs/ref/<ref>,
// which sends the browser to /docs/<id> and lets it carry the #sec fragment
// across the redirect. Three spellings are linked — the 025 §14.3 shorthand
// (WL-SPEC-42, with an optional #sec-10), the keyword form ("spec 042 §10",
// "ADR 048"), and the bare corpus form ("025 §14.3") — because those are the
// ways the corpus actually writes references.
//
// A bare task id (WL-129, COW-7) links straight to /tasks/<id>. There is no
// /tasks/ref/ redirect to mirror /docs/ref/: a document reference needs one
// because its several spellings all have to be resolved to a page path that
// is a numeric row id, whereas a task id IS the path segment GET /tasks/{id}
// takes, so a redirect would only add a hop.
//
// WHY THE KEY SET, NOT A DENYLIST (WL-305). The token shape
// [A-Z][A-Z0-9]{1,9}-\d+ also spells UTF-8, SHA-256, ISO-8601 and every other
// acronym-number pair, so shape alone cannot decide. The two candidate fixes
// were a narrowed pattern plus an acronym denylist, and passing the live
// project-key set in from the caller (internal/api, which has the store).
// This is the second: a denylist is open-ended and wrong by default — every
// acronym nobody thought of links to a 404 — while the key set is right by
// construction, because a task id's key is a project key. WL-301's note that
// the renderer "has no store access" described where the keys were not, not a
// reason they could not be handed to it.
//
// The caller's own note on cost is in internal/api's projectKeys. The cache
// concern that made this look expensive — Cache is body-keyed, so a key-set
// change would serve a stale render until restart — is answered by putting
// the key set's fingerprint in the cache key (see keyOf), not by tolerating
// staleness. The keys reach the transformer through the parse context rather
// than through a parser built per key set, because goldmark's parsers are
// package-level values shared by every concurrent render.
//
// The transformer runs post-parse, skipping links, autolinks, images and
// code spans, so a reference inside a code fence or an explicit link is left
// exactly as written. Goldmark splits a line of prose into several adjacent
// Text nodes on inline-trigger bytes, so matching runs over each contiguous
// run of sibling text nodes rather than per node — the segments are
// contiguous in the source, which is what makes the run one byte span.

package mdrender

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"

	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// docRefPrefix is where a linked reference points: the cockpit's resolving
// redirect (internal/api's docRefRedirect).
const docRefPrefix = "/docs/ref/"

// taskRefPrefix is where a linked bare task id points: the task's own cockpit
// page, GET /tasks/{id}.
const taskRefPrefix = "/tasks/"

// The reference spellings, tried in order. A trailing sentence dot is
// trimmed off a match in code, not in pattern, so "025 §14.3." links as
// "025 §14.3".
var (
	// WL-SPEC-42, WL-ADR-7, optionally #sec-10 / #sec-3.1a.
	shorthandRef = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-(?:SPEC|ADR|PLAN)-\d+(?:#sec-[0-9A-Za-z._-]+)?`)
	// spec 042 §10, ADR 048 §2, Spec 25 — keyword, number, optional §.
	keywordRef = regexp.MustCompile(`\b(?:[Ss]pec|ADR|[Aa]dr)\s(\d{1,4})(?:\s?§\s?([0-9][0-9A-Za-z.]*))?`)
	// 025 §14.3 — a bare number only when the § makes it unmistakably a ref.
	bareRef = regexp.MustCompile(`\b(\d{1,4})\s?§\s?([0-9][0-9A-Za-z.]*)`)
	// WL-129, COW-7 — the key is captured so it can be checked against the
	// live project-key set, which is the only thing separating a task id from
	// UTF-8. The key subpattern is projects_key_format's, so a token this
	// matches with a known key is always a well-formed task id.
	taskRef = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9})-(\d+)\b`)
)

// ProjectKeys is the set of live project keys a render links bare task ids
// against. The zero value links none, which is what a caller with no store —
// a test, or a store read that failed — should get: no links beats wrong
// ones.
//
// It is immutable once built and safe to share across renders. fingerprint is
// a stable digest of the set, mixed into the cache key so a render made under
// one key set is never served under another.
type ProjectKeys struct {
	set         map[string]struct{}
	fingerprint string
}

// NewProjectKeys builds the set from the keys of the live projects, ignoring
// empties and duplicates. Order does not affect the fingerprint.
func NewProjectKeys(keys []string) ProjectKeys {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k != "" {
			set[k] = struct{}{}
		}
	}
	if len(set) == 0 {
		return ProjectKeys{}
	}
	sorted := make([]string, 0, len(set))
	for k := range set {
		sorted = append(sorted, k)
	}
	slices.Sort(sorted)
	// Hex, not the raw digest: the fingerprint is concatenated into the cache
	// key under a NUL separator, and raw digest bytes can contain a NUL.
	sum := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	return ProjectKeys{set: set, fingerprint: hex.EncodeToString(sum[:])}
}

func (k ProjectKeys) has(key string) bool {
	_, ok := k.set[key]
	return ok
}

// projectKeysKey carries the ProjectKeys for one Convert call. Registered at
// init, before any parser.NewContext, so every context's store is wide enough
// to hold it.
var projectKeysKey = parser.NewContextKey()

// withProjectKeys is the parse option render passes on every Convert.
func withProjectKeys(keys ProjectKeys) parser.ParseOption {
	pc := parser.NewContext()
	pc.Set(projectKeysKey, keys)
	return parser.WithContext(pc)
}

// docRefLinker is the AST transformer; withDocRefLinks installs it on both
// flavours' parsers.
type docRefLinker struct{}

var withDocRefLinks = parser.WithASTTransformers(util.Prioritized(docRefLinker{}, 900))

func (docRefLinker) Transform(doc *gast.Document, reader text.Reader, pc parser.Context) {
	var keys ProjectKeys
	if pc != nil {
		keys, _ = pc.Get(projectKeysKey).(ProjectKeys)
	}
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
		linkifyRun(run, source, keys)
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

// hyphenAdjacent reports whether the token at [start,end) is glued to a wider
// hyphenated compound. It is what keeps a branch name out of the link text:
// "WL-129-fix-the-thing" is one identifier, not a mention of WL-129, and the
// same reasoning drops the "WL-129" inside "WL-SPEC-129" before the overlap
// check has to.
func hyphenAdjacent(value []byte, start, end int) bool {
	return (start > 0 && value[start-1] == '-') || (end < len(value) && value[end] == '-')
}

// findRefs locates every reference in value, earlier patterns winning where
// matches overlap, returned in document order. keys decides which bare
// <KEY>-<n> tokens are task ids; an empty set links none.
func findRefs(value []byte, keys ProjectKeys) []refMatch {
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
	// Task ids last: a document shorthand (WL-SPEC-42) contains a token this
	// pattern also matches, and running last lets the shorthand win.
	for _, loc := range taskRef.FindAllSubmatchIndex(value, -1) {
		if !keys.has(string(value[loc[2]:loc[3]])) || hyphenAdjacent(value, loc[0], loc[1]) {
			continue
		}
		if taken(loc[0], loc[1]) {
			continue
		}
		out = append(out, refMatch{loc[0], loc[1], taskRefPrefix + string(value[loc[0]:loc[1]])})
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
func linkifyRun(run []*gast.Text, source []byte, keys ProjectKeys) {
	first, last := run[0], run[len(run)-1]
	parent := first.Parent()
	if parent == nil {
		return
	}
	start, stop := first.Segment.Start, last.Segment.Stop
	refs := findRefs(source[start:stop], keys)
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
