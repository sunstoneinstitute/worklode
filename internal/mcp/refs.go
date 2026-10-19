package mcp

import (
	"fmt"
	"strconv"
	"strings"
)

// kindFormat maps a leading kind word to its ref shape (WL-REQ-1831).
var kindFormat = map[string]string{
	"spec":        "%s-SPEC-%d",
	"plan":        "%s-PLAN-%d",
	"task":        "%s-%d",
	"rule":        "%s-RULE-%d",
	"requirement": "%s-REQ-%d",
	"req":         "%s-REQ-%d",
}

var numberWords = map[string]int{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
	"seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
	"thirteen": 13, "fourteen": 14, "fifteen": 15, "sixteen": 16,
	"seventeen": 17, "eighteen": 18, "nineteen": 19,
	"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60,
	"seventy": 70, "eighty": 80, "ninety": 90,
}

// parseNumber reads digits or number words up to 999 from tokens.
func parseNumber(tokens []string) (int, bool) {
	if len(tokens) == 0 {
		return 0, false
	}
	if len(tokens) == 1 {
		if n, err := strconv.Atoi(tokens[0]); err == nil && n >= 0 {
			return n, true
		}
	}
	total, seen := 0, false
	for _, t := range tokens {
		switch {
		case t == "and":
			continue
		case t == "hundred":
			if !seen || total == 0 || total > 9 {
				return 0, false
			}
			total *= 100
		default:
			v, ok := numberWords[t]
			if !ok {
				return 0, false
			}
			total += v
		}
		seen = true
	}
	return total, seen && total <= 999
}

// parseRef turns a spoken ref into a canonical one. A leading kind word wins
// over the task-id shape; PREFIX-n is a task id only when PREFIX is in
// projectKeys; anything else comes back as written (WL-REQ-1831).
func parseRef(spoken, projectKey string, projectKeys []string) string {
	s := strings.TrimSpace(spoken)
	tokens := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '\t'
	})
	if len(tokens) > 1 {
		if f, ok := kindFormat[tokens[0]]; ok {
			if n, ok := parseNumber(tokens[1:]); ok {
				return fmt.Sprintf(f, projectKey, n)
			}
		}
	}
	if i := strings.LastIndex(s, "-"); i > 0 {
		prefix := s[:i]
		if _, err := strconv.Atoi(s[i+1:]); err == nil {
			for _, k := range projectKeys {
				if strings.EqualFold(k, prefix) {
					return k + s[i:]
				}
			}
		}
	}
	return s
}
