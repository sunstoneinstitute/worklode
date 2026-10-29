package review

import (
	"strings"
	"unicode"
)

// fuzzyMinLen is the minimum trimmed quote length the fuzzy stage accepts
// (WL-REQ-1773's minimum-length guard).
const fuzzyMinLen = 8

// fuzzyWindow bounds the fuzzy stage to lines this close to the predicted
// line; a quote further away is left to the scan stage.
const fuzzyWindow = 10

// Relocate finds where the quote anchored at old[start:end] now sits in new,
// climbing crit's four-stage ladder (WL-REQ-1773). ok=false means drifted:
// the caller marks the thread obsolete (WL-REQ-1317). Offsets are UTF-8
// bytes; a returned range always lies on character boundaries.
//
// Every stage matches whitespace-insensitively: a whitespace run in the
// quote matches any whitespace run in new, and the returned range covers
// new's own bytes.
func Relocate(old, new string, start, end int, quote string) (newStart, newEnd int, stage string, ok bool) {
	if quote == "" || start < 0 || start > end || end > len(old) {
		return 0, 0, "drifted", false
	}

	// exact: the quote still sits at the old offset.
	if e, hit := matchAt(new, start, quote); hit {
		return start, e, "exact", true
	}

	oldLines, newLines := lineStarts(old), lineStarts(new)
	oldLine := lineOf(oldLines, start)
	col := start - oldLines[oldLine]
	pairs := lcsLines(splitLines(old), splitLines(new))

	// lcs: the anchored line survived; the quote sits at the same column.
	if j, hit := pairs[oldLine]; hit {
		p := newLines[j] + col
		if e, hit := matchAt(new, p, quote); hit {
			return p, e, "lcs", true
		}
	}

	// The predicted line is the nearest surviving line above, shifted by
	// the distance to it; the predicted offset keeps the column.
	predLine := oldLine
	for i := oldLine - 1; i >= 0; i-- {
		if j, hit := pairs[i]; hit {
			predLine = j + oldLine - i
			break
		}
	}
	predLine = min(predLine, len(newLines)-1)
	pred := min(newLines[predLine]+col, len(new))

	// fuzzy: the nearest line whose trimmed text equals the trimmed quote.
	want := collapse(strings.TrimSpace(quote))
	if len(strings.TrimSpace(quote)) >= fuzzyMinLen {
		for d := 0; d <= fuzzyWindow; d++ {
			cand := []int{predLine - d, predLine + d}
			if d == 0 {
				cand = cand[:1]
			}
			for _, j := range cand {
				if j < 0 || j >= len(newLines) {
					continue
				}
				line := lineText(new, newLines, j)
				trimmed := strings.TrimSpace(line)
				if trimmed != "" && collapse(trimmed) == want {
					s := newLines[j] + strings.Index(line, trimmed)
					return s, s + len(trimmed), "fuzzy", true
				}
			}
		}
	}

	// scan: the hit anywhere in new nearest the predicted offset.
	// ponytail: O(len(new)*len(quote)); index candidates first if bodies grow large.
	best, bestEnd := -1, 0
	for p := 0; p < len(new); p++ {
		if e, hit := matchAt(new, p, quote); hit && (best < 0 || abs(p-pred) < abs(best-pred)) {
			best, bestEnd = p, e
		}
	}
	if best >= 0 {
		return best, bestEnd, "scan", true
	}
	return 0, 0, "drifted", false
}

// matchAt reports whether quote matches text starting at p, treating any
// whitespace run in quote as matching a non-empty whitespace run in text,
// and returns the end of the match in text.
func matchAt(text string, p int, quote string) (int, bool) {
	if p < 0 || p > len(text) {
		return 0, false
	}
	i, j := p, 0
	for j < len(quote) {
		if isSpace(quote[j]) {
			if i >= len(text) || !isSpace(text[i]) {
				return 0, false
			}
			for j < len(quote) && isSpace(quote[j]) {
				j++
			}
			for i < len(text) && isSpace(text[i]) {
				i++
			}
			continue
		}
		if i >= len(text) || text[i] != quote[j] {
			return 0, false
		}
		i++
		j++
	}
	return i, true
}

func isSpace(b byte) bool { return b < 0x80 && unicode.IsSpace(rune(b)) }

// collapse replaces every whitespace run with one space.
func collapse(s string) string {
	var b strings.Builder
	inSpace := false
	for i := 0; i < len(s); i++ {
		if isSpace(s[i]) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteByte(s[i])
	}
	return b.String()
}

func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = collapse(l)
	}
	return lines
}

// lineStarts returns the byte offset of each line's first byte.
func lineStarts(s string) []int {
	starts := []int{0}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func lineOf(starts []int, off int) int {
	l := 0
	for l+1 < len(starts) && starts[l+1] <= off {
		l++
	}
	return l
}

func lineText(s string, starts []int, l int) string {
	end := len(s)
	if l+1 < len(starts) {
		end = starts[l+1] - 1
	}
	return s[starts[l]:end]
}

// lcsLines maps each old line index in the longest common subsequence of
// lines to its new line index. On a tie the backtrack drops the new line,
// so a line that moved far down stays unmatched and falls to a later stage.
func lcsLines(a, b []string) map[int]int {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	pairs := map[int]int{}
	for i, j := n, m; i > 0 && j > 0; {
		switch {
		case a[i-1] == b[j-1]:
			pairs[i-1] = j - 1
			i--
			j--
		case dp[i][j-1] >= dp[i-1][j]:
			j--
		default:
			i--
		}
	}
	return pairs
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
