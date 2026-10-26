package inlineref

import (
	"fmt"
	"sort"
	"strings"
)

// Target is what one Hit resolved to.
type Target struct {
	Ref       string // plain form: WL-7, WL-REQ-12, WL-SPEC-77
	DocAnchor bool   // section with no rule: leave the text as written
}

// rewritable reports whether Normalize may rewrite the hit. A keyword form is
// a short form: the number it names depends on when it was written, so it is
// never rewritten (WL-REQ-1854, WL-REQ-1650). A URL path with no scheme and
// host is a short form too. A doc ref with a #rule-<n> fragment is already
// plain (WL-REQ-1653).
func rewritable(h Hit, t *Target) bool {
	if h.Form == FormURL && !h.Absolute || h.Form == FormNode && h.RuleFragment != 0 {
		return false
	}
	return t != nil && !t.DocAnchor && !h.Literal && h.Form != FormKeyword
}

// Normalize rewrites each rewritable hit into its plain form (WL-REQ-1854).
// targets is indexed like hits; a nil entry is unresolved and left alone.
func Normalize(text string, hits []Hit, targets []*Target) string {
	lines := strings.Split(text, "\n")
	// Rewrite right to left within a line so earlier offsets stay valid.
	for i := len(hits) - 1; i >= 0; i-- {
		h, t := hits[i], targets[i]
		if !rewritable(h, t) {
			continue
		}
		repl := t.Ref
		if h.Form == FormURL && h.LinkText != "" && h.LinkText != t.Ref {
			repl = "[" + h.LinkText + "](" + t.Ref + ")"
		}
		l := lines[h.Line-1]
		lines[h.Line-1] = l[:h.Start] + repl + l[h.End:]
	}
	return strings.Join(lines, "\n")
}

// Verify reports whether newText is a pure normalization of oldText
// (WL-REQ-1855): same (line, target) set, bytes outside rewritten spans equal.
func Verify(oldText, newText string, keys map[string]bool, resolve func(Hit) *Target) error {
	oldLines, newLines := strings.Split(oldText, "\n"), strings.Split(newText, "\n")
	if len(oldLines) != len(newLines) {
		return fmt.Errorf("line count changed from %d to %d", len(oldLines), len(newLines))
	}
	oldHits := Scan(oldText, keys)
	spans := make([][][2]int, len(oldLines))
	for _, h := range oldHits {
		if rewritable(h, resolve(h)) {
			spans[h.Line-1] = append(spans[h.Line-1], [2]int{h.Start, h.End})
		}
	}
	for i, ss := range spans {
		if !matchesOutside(oldLines[i], newLines[i], ss) {
			return fmt.Errorf("line %d changed outside a reference", i+1)
		}
	}
	a, b := targetSet(oldHits, resolve), targetSet(Scan(newText, keys), resolve)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		return fmt.Errorf("referenced targets changed")
	}
	return nil
}

// matchesOutside reports whether new equals old with each span replaced by
// arbitrary text. spans are ordered and disjoint.
func matchesOutside(old, new string, spans [][2]int) bool {
	pos, prev := 0, 0
	for k, sp := range spans {
		seg := old[prev:sp[0]]
		if k == 0 {
			if !strings.HasPrefix(new, seg) {
				return false
			}
			pos = len(seg)
		} else {
			j := strings.Index(new[pos:], seg)
			if j < 0 {
				return false
			}
			pos += j + len(seg)
		}
		prev = sp[1]
	}
	tail := old[prev:]
	if len(spans) == 0 {
		return old == new
	}
	return len(new)-pos >= len(tail) && strings.HasSuffix(new, tail)
}

// targetSet lists "line target" for every resolved non-literal hit, sorted.
func targetSet(hits []Hit, resolve func(Hit) *Target) []string {
	var out []string
	for _, h := range hits {
		t := resolve(h)
		if t == nil || h.Literal {
			continue
		}
		ref := t.Ref
		if t.DocAnchor {
			ref += "#" + h.Anchor
		}
		out = append(out, fmt.Sprintf("%d %s", h.Line, ref))
	}
	sort.Strings(out)
	return out
}
