// Package inlineref finds candidate references to Worklode nodes in Markdown
// text (WL-REQ-1851). Scan is pure: it reports where a reference is spelled
// and what it names, and resolves nothing.
package inlineref

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/ns"
	"github.com/sunstoneinstitute/worklode/internal/refkind"
)

// Form is how a reference is spelled.
type Form string

const (
	FormTask    Form = "task"    // WL-12
	FormNode    Form = "node"    // WL-SPEC-77
	FormKeyword Form = "keyword" // spec 42 §3, ADR 7 (spec-gate:ignore, an example of the form)
	FormURL     Form = "url"     // a Worklode URL, bare or as a Markdown link
)

// Hit is one candidate reference.
type Hit struct {
	Line   int    // 1-based
	Start  int    // byte offset in the line
	End    int    // exclusive
	Raw    string // the matched text, link syntax included
	Form   Form
	Key    string // project key; "" for the keyword form
	Infix  string // "" for a task id, else a refkind infix
	Number int64
	Anchor string // "sec-3" without '#', or ""
	// RuleFragment is n in a doc ref's #rule-<n> fragment, else 0
	// (WL-REQ-1653).
	RuleFragment int64
	LinkText     string // text of [text](url) when the hit is a Markdown link
	// Absolute is set on a URL with scheme and host. A bare path such as
	// /<ref> is a short form: it yields an edge but is never rewritten
	// (WL-REQ-1854).
	Absolute bool
	// Literal is set inside inline code, inside quotation marks, and on a
	// section that starts a range or list. Such a hit yields an edge but is
	// never rewritten (WL-REQ-1854).
	Literal bool
}

var (
	allInfixes = func() string {
		var s []string
		for _, k := range refkind.All {
			s = append(s, k.Infix)
		}
		return strings.Join(s, "|")
	}()

	anchorPat = `sec-[0-9A-Za-z._-]*[0-9A-Za-z]`
	// A trailing anchor is part of the match only when it ends on an
	// alphanumeric; the word boundary keeps "WL-SPEC-12abc" out.
	nodeRef = regexp.MustCompile(`\b(` + refkind.KeyPattern + `)-(` + allInfixes + `)-(\d+)(?:#(` + anchorPat + `)|#rule-(\d+)\b|\b)`)
	taskRef = regexp.MustCompile(`\b(` + refkind.KeyPattern + `)-(\d+)\b`)
	// spec 042 §10, ADR 48, as internal/mdrender/autolink.go reads them. spec-gate:ignore
	keywordRef = regexp.MustCompile(`\b([Ss]pec|ADR|[Aa]dr)\s(\d{1,4})(?:\s?§\s?([0-9][0-9A-Za-z.]*))?`)

	nodeFull = regexp.MustCompile(`^(` + refkind.KeyPattern + `)-(` + allInfixes + `)-(\d+)$`)
	taskFull = regexp.MustCompile(`^(` + refkind.KeyPattern + `)-(\d+)$`)

	mdLink  = regexp.MustCompile(`\[([^\]]*)\]\(\s*([^)\s]+)(?:\s+"[^"]*")?\s*\)`)
	bareURL = regexp.MustCompile(`(?:[A-Za-z][A-Za-z0-9+.-]*://[^/\s]*)?/[^\s<>)\]"']*`)
	// What follows the first section of a range or list: "–§4", ", §5", ", #sec-5". spec-gate:ignore
	sectionMore = regexp.MustCompile(`^\s*(?:[–—-]|,|and|or|to)\s*(?:§|#?sec-)`)
)

// Scan returns every candidate reference in text, in order. keys is the set
// of project keys a bare <KEY>-<n> counts as a task id for.
func Scan(text string, keys map[string]bool) []Hit {
	lines := strings.Split(text, "\n")
	scanned := make([]bool, len(lines))
	fence := ""
	for i, line := range lines {
		if f := fenceOf(line); f != "" {
			if fence == "" {
				fence = f
			} else if f[0] == fence[0] && len(f) >= len(fence) && strings.TrimSpace(line) == f {
				fence = ""
			}
			continue
		}
		scanned[i] = fence == ""
	}
	code := paragraphCodeSpans(lines, scanned)
	var hits []Hit
	for i, line := range lines {
		if !scanned[i] {
			continue
		}
		for _, h := range scanLine(line, keys, code[i]) {
			h.Line = i + 1
			hits = append(hits, h)
		}
	}
	return hits
}

// paragraphCodeSpans returns each line's inline code spans. A code span can
// continue onto the next line of its paragraph, so spans are found over each
// run of scanned, non-blank lines and split back into lines.
func paragraphCodeSpans(lines []string, scanned []bool) [][][2]int {
	out := make([][][2]int, len(lines))
	for i := 0; i < len(lines); {
		if !scanned[i] || strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		j := i
		for j < len(lines) && scanned[j] && strings.TrimSpace(lines[j]) != "" {
			j++
		}
		para := strings.Join(lines[i:j], "\n")
		for _, sp := range codeSpans(para) {
			off := 0
			for k := i; k < j; k++ {
				s, e := max(sp[0]-off, 0), min(sp[1]-off, len(lines[k]))
				if s < e {
					out[k] = append(out[k], [2]int{s, e})
				}
				off += len(lines[k]) + 1
			}
		}
		i = j
	}
	return out
}

// fenceOf returns the opening run of a code fence line, "" for other lines.
func fenceOf(line string) string {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 || len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return ""
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 || (s[0] == '`' && strings.Contains(s[n:], "`")) {
		return ""
	}
	return s[:n]
}

func scanLine(line string, keys map[string]bool, code [][2]int) []Hit {
	var hits []Hit
	taken := func(s, e int) bool {
		for _, h := range hits {
			if s < h.End && e > h.Start {
				return true
			}
		}
		return false
	}
	add := func(h Hit, loc []int) {
		h.Start, h.End, h.Raw = loc[0], loc[1], line[loc[0]:loc[1]]
		if !taken(h.Start, h.End) {
			hits = append(hits, h)
		}
	}

	for _, m := range mdLink.FindAllStringSubmatchIndex(line, -1) {
		if h, ok := parseURL(line[m[4]:m[5]]); ok {
			h.LinkText = line[m[2]:m[3]]
			add(h, m[:2])
		}
	}
	for _, m := range bareURL.FindAllStringIndex(line, -1) {
		s, e := m[0], m[1]
		if !strings.Contains(line[s:e], "://") && s > 0 && strings.ContainsRune("/.:-_", rune(line[s-1])) || (s > 0 && isAlnum(line[s-1])) {
			continue
		}
		for e > s && strings.ContainsRune(".,;:!?", rune(line[e-1])) {
			e--
		}
		if h, ok := parseURL(line[s:e]); ok {
			add(h, []int{s, e})
		}
	}
	for _, m := range nodeRef.FindAllStringSubmatchIndex(line, -1) {
		n, err := strconv.ParseInt(line[m[6]:m[7]], 10, 64)
		if err != nil {
			continue
		}
		h := Hit{Form: FormNode, Key: line[m[2]:m[3]], Infix: line[m[4]:m[5]], Number: n}
		end := m[1]
		if m[8] >= 0 || m[10] >= 0 {
			if t, _ := refkind.Lookup(h.Infix); t != refkind.Doc {
				end = m[7] // a fragment is a doc-ref feature
			} else if m[8] >= 0 {
				h.Anchor = line[m[8]:m[9]]
			} else if h.RuleFragment, err = strconv.ParseInt(line[m[10]:m[11]], 10, 64); err != nil {
				continue
			}
		}
		add(h, []int{m[0], end})
	}
	for _, m := range keywordRef.FindAllStringSubmatchIndex(line, -1) {
		n, _ := strconv.ParseInt(line[m[4]:m[5]], 10, 64)
		h := Hit{Form: FormKeyword, Infix: refkind.SPEC, Number: n}
		if strings.EqualFold(line[m[2]:m[3]], "adr") {
			h.Infix = refkind.ADR
		}
		end := m[1]
		if m[6] >= 0 {
			for end > m[6] && line[end-1] == '.' {
				end--
			}
			h.Anchor = "sec-" + line[m[6]:end]
		}
		add(h, []int{m[0], end})
	}
	for _, m := range taskRef.FindAllStringSubmatchIndex(line, -1) {
		s, e := m[0], m[1]
		if !keys[line[m[2]:m[3]]] || (s > 0 && line[s-1] == '-') || (e < len(line) && line[e] == '-') {
			continue
		}
		n, err := strconv.ParseInt(line[m[4]:m[5]], 10, 64)
		if err != nil {
			continue
		}
		add(Hit{Form: FormTask, Key: line[m[2]:m[3]], Number: n}, m[:2])
	}

	slices.SortFunc(hits, func(a, b Hit) int { return a.Start - b.Start })
	literal := slices.Concat(code, quoteSpans(line, code))
	for i, h := range hits {
		if h.Anchor != "" && sectionMore.MatchString(line[h.End:]) {
			hits[i].Literal = true
		}
		for _, sp := range literal {
			if h.Start >= sp[0] && h.End <= sp[1] {
				hits[i].Literal = true
			}
		}
	}
	return hits
}

// quoteSpans returns the [start,end) of each double-quoted run on the line,
// straight or curly, ignoring quote marks inside code.
func quoteSpans(line string, code [][2]int) [][2]int {
	var out [][2]int
	open := -1
	for i, r := range line {
		if slices.ContainsFunc(code, func(sp [2]int) bool { return i >= sp[0] && i < sp[1] }) {
			continue
		}
		switch {
		case r == '“' || (r == '"' && open < 0):
			open = i
		case (r == '”' || r == '"') && open >= 0:
			out = append(out, [2]int{open, i + len(string(r))})
			open = -1
		}
	}
	return out
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// codeSpans returns the [start,end) of each inline code span on the line,
// backticks included. A run of n backticks closes on the next run of n.
func codeSpans(line string) [][2]int {
	var out [][2]int
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(line) && line[j] == '`' {
			j++
		}
		run := line[i:j]
		if k := strings.Index(line[j:], run); k >= 0 {
			// the closing run must be exactly n long; skip longer runs
			end := j + k + len(run)
			if end >= len(line) || line[end] != '`' {
				out = append(out, [2]int{i, end})
				i = end
				continue
			}
		}
		i = j
	}
	return out
}

// parseURL reads a Worklode node URL in any shape internal/api/router.go
// serves. Scheme and host are ignored.
func parseURL(u string) (Hit, bool) {
	absolute := false
	if i := strings.Index(u, "://"); i >= 0 {
		absolute = true
		u = u[i+3:]
		j := strings.Index(u, "/")
		if j < 0 {
			return Hit{}, false
		}
		u = u[j:]
	}
	anchor := ""
	if i := strings.IndexByte(u, '#'); i >= 0 {
		anchor, u = u[i+1:], u[:i]
	}
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	u = strings.TrimSuffix(u, "/")
	if !strings.HasPrefix(u, "/") {
		return Hit{}, false
	}
	p := strings.Split(u[1:], "/")
	h := Hit{Form: FormURL, Absolute: absolute}
	switch {
	case len(p) == 1:
		return ref(h, p[0], anchor)
	case len(p) == 2 && p[0] == "tasks":
		return ref(h, p[1], "")
	case len(p) == 2 && p[0] == "rules":
		return ref(h, p[1], "")
	case len(p) == 3 && p[0] == "docs" && p[1] == "ref":
		return ref(h, p[2], anchor)
	case (len(p) == 4 || len(p) == 5) && p[0] == "projects":
		n, err := strconv.ParseInt(p[3], 10, 64)
		if err != nil || (len(p) == 5 && !allDigits(p[4])) {
			return Hit{}, false
		}
		h.Key, h.Number = p[1], n
		switch kind := p[2]; {
		case kind == "rule":
			h.Infix = refkind.RULE
		case kind == "spec":
			h.Infix = refkind.SPEC
		case kind == "plan":
			h.Infix = refkind.PLAN
		case slices.Contains(ns.TaskKinds, kind):
		default:
			return Hit{}, false
		}
		docFragment(&h, anchor)
		return h, true
	}
	return Hit{}, false
}

// docFragment sets h's section or rule fragment when h is a doc ref.
func docFragment(h *Hit, fragment string) {
	if t, _ := refkind.Lookup(h.Infix); t != refkind.Doc {
		return
	}
	if strings.HasPrefix(fragment, "sec-") {
		h.Anchor = fragment
	} else if n, ok := strings.CutPrefix(fragment, "rule-"); ok && allDigits(n) {
		h.RuleFragment, _ = strconv.ParseInt(n, 10, 64)
	}
}

func allDigits(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// ref fills h from a task id or a node ref, as a URL path segment spells it.
func ref(h Hit, s, anchor string) (Hit, bool) {
	if m := nodeFull.FindStringSubmatch(s); m != nil {
		n, err := strconv.ParseInt(m[3], 10, 64)
		if err != nil {
			return Hit{}, false
		}
		h.Key, h.Infix, h.Number = m[1], m[2], n
		docFragment(&h, anchor)
		return h, true
	}
	if m := taskFull.FindStringSubmatch(s); m != nil {
		n, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			return Hit{}, false
		}
		h.Key, h.Number = m[1], n
		return h, true
	}
	return Hit{}, false
}
