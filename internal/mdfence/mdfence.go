// Package mdfence recognises CommonMark fenced code block markers. It is a
// stdlib-only leaf so the hook binaries can use it without pulling in
// internal/designdoc.
package mdfence

import "strings"

// Open returns the marker a line opens a fenced code block with ("```",
// "~~~~") and its info string, or "" when the line opens none. As in
// CommonMark, the marker is a run of three or more backticks or tildes, and a
// backtick fence's info string holds no backtick: "```` ```x ````" is an
// inline code span. Leading indentation is not limited, so a fence nested in a
// list item still counts.
func Open(line string) (marker, info string) {
	s := strings.TrimLeft(line, " \t")
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return "", ""
	}
	n := len(s) - len(strings.TrimLeft(s, s[:1]))
	info = strings.TrimSpace(s[n:])
	if n < 3 || (s[0] == '`' && strings.Contains(info, "`")) {
		return "", ""
	}
	return s[:n], info
}

// Closes reports whether line closes a fence opened with marker: a run of the
// same character at least as long, followed only by whitespace.
func Closes(line, marker string) bool {
	s := strings.TrimLeft(line, " \t")
	n := len(s) - len(strings.TrimLeft(s, marker[:1]))
	return n >= len(marker) && strings.TrimSpace(s[n:]) == ""
}

// Tracker follows fence state over a run of lines.
type Tracker struct{ marker string }

// Fence reports whether line is a fence marker or inside a fenced block, and
// advances the state past it.
func (t *Tracker) Fence(line string) bool {
	if t.marker != "" {
		if Closes(line, t.marker) {
			t.marker = ""
		}
		return true
	}
	t.marker, _ = Open(line)
	return t.marker != ""
}
