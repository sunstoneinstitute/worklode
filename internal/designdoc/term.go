package designdoc

import (
	"strings"
	"unicode"
)

// TermSlug is a definition rule's term slug (WL-REQ-1368): its heading
// lowercased, each run of characters other than letters and digits turned
// into one hyphen, with no hyphen at either end. "Edge Agent" is
// "edge-agent".
func TermSlug(heading string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(heading) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			hyphen = false
		} else {
			hyphen = true
		}
	}
	return b.String()
}
