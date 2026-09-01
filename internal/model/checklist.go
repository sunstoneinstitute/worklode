package model

import (
	"regexp"
	"strings"
)

// checklistLine matches a markdown checklist item: `- [ ] title` or
// `* [x] title`, with any of the marks this codebase recognizes as a valid
// (checked or unchecked) checkbox state.
var checklistLine = regexp.MustCompile(`^\s*[-*]\s+\[([\sXxv-])\]\s*(.*)$`)

// ChecklistItem is one checklist line parsed out of a task's body. Ordinal is
// its 0-based index of appearance among checklist lines in the body — the
// canonical way to address an item, since titles need not be unique.
type ChecklistItem struct {
	Ordinal int    `json:"ordinal"`
	Title   string `json:"title"`
	Checked bool   `json:"checked"`
}

// ParseChecklist finds every checklist line in body, in order of appearance.
// Any mark but a bare space (x, X, v, -) reads as checked — the mark itself
// isn't preserved, so re-checking an item always writes a plain "x".
func ParseChecklist(body string) []ChecklistItem {
	items := []ChecklistItem{}
	for _, line := range strings.Split(body, "\n") {
		m := checklistLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		items = append(items, ChecklistItem{
			Ordinal: len(items),
			Title:   m[2],
			Checked: m[1] != " " && m[1] != "",
		})
	}
	return items
}

// SetChecklistMark rewrites the mark of the ordinal-th checklist line in
// body (0-based, among checklist lines only) to reflect checked, leaving
// every other character of the body untouched. ok is false, and body is
// returned unchanged, when body has no checklist line at that ordinal.
func SetChecklistMark(body string, ordinal int, checked bool) (newBody string, item ChecklistItem, ok bool) {
	mark := " "
	if checked {
		mark = "x"
	}
	lines := strings.Split(body, "\n")
	count := 0
	for i, line := range lines {
		loc := checklistLine.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		if count == ordinal {
			lines[i] = line[:loc[2]] + mark + line[loc[3]:]
			item = ChecklistItem{Ordinal: ordinal, Title: line[loc[4]:loc[5]], Checked: checked}
			return strings.Join(lines, "\n"), item, true
		}
		count++
	}
	return body, ChecklistItem{}, false
}

// SetChecklistItemInput is the request body for POST
// /api/v1/tasks/{id}/checklist: sets one item's checked state, identified by
// Ordinal (canonical) or Title. Exactly one of Ordinal or Title must be set.
type SetChecklistItemInput struct {
	Ordinal *int    `json:"ordinal,omitempty"`
	Title   *string `json:"title,omitempty"`
	Checked bool    `json:"checked"`
}
