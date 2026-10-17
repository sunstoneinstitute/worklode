// mcpspoken.go turns spoken refs into the ref grammar the API reads:
// "spec seventy-seven" becomes WL-SPEC-77, "task five twelve" WL-512,
// "requirement 1300" WL-REQ-1300, against the project key the call works
// in. A ref already in the grammar passes through, upper-cased, when its key
// is a project's; anything this cannot read passes through untouched, for
// the API's own resolver (a slug, a path) to answer.
package api

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// spokenTypedRef is the <KEY>-<TYPE>-<n> grammar, any case, with an
	// optional section fragment.
	spokenTypedRef = regexp.MustCompile(`(?i)^([A-Z][A-Z0-9]{1,9})-(SPEC|PLAN|ADR|REQ|RULE|CL|MILE|DEL)-(\d+)(#sec-[\w.\-]+)?$`)
	// spokenTaskRef is a task id, any case; the prefix must still be a
	// project key.
	spokenTaskRef = regexp.MustCompile(`(?i)^([A-Z][A-Z0-9]{1,9})-\d+$`)
	spokenDigits  = regexp.MustCompile(`^\d+$`)
	// spokenSection is a section number written out: 3, 4.1, 4.1a.
	spokenSection = regexp.MustCompile(`^\d+(\.\d+)*[a-z]?$`)
	// spokenKey is a word that could be a project key.
	spokenKey = regexp.MustCompile(`^[a-z][a-z0-9]{1,9}$`)
)

// spokenKinds maps the words people say for a ref's kind to its <TYPE>
// infix; "" is a task, whose id carries no type.
var spokenKinds = map[string]string{
	"task": "", "ticket": "",
	"spec": "SPEC", "specification": "SPEC",
	"plan":        "PLAN",
	"requirement": "REQ", "req": "REQ",
	"rule": "RULE",
}

// spokenDropped are words that carry nothing in a spoken ref.
var spokenDropped = map[string]bool{"number": true, "no": true, "the": true, "a": true, "num": true}

// spokenRef reads raw as a ref in the project whose key is key. keys are
// every project key the caller can see: a <PREFIX>-<n> or
// <PREFIX>-<TYPE>-<n> is a ref only when PREFIX is one of them or key, so a
// slug like "rollout-2" is never read as a task id.
func spokenRef(raw, key string, keys []string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return s
	}
	key = strings.ToUpper(key)
	known := func(k string) bool {
		k = strings.ToUpper(k)
		if k == key {
			return true
		}
		for _, kk := range keys {
			if strings.EqualFold(kk, k) {
				return true
			}
		}
		return false
	}
	if m := spokenTypedRef.FindStringSubmatch(s); m != nil && known(m[1]) {
		return strings.ToUpper(m[1]) + "-" + strings.ToUpper(m[2]) + "-" + m[3] + strings.ToLower(m[4])
	}
	tokens := spokenTokens(s)
	if len(tokens) == 0 {
		return s
	}
	// A kind word leads: "spec-77" is a spec, never the task SPEC-77.
	if _, isKind := spokenKinds[tokens[0]]; !isKind {
		if m := spokenTaskRef.FindStringSubmatch(s); m != nil {
			if known(m[1]) {
				return strings.ToUpper(s)
			}
			return s
		}
		if spokenDigits.MatchString(s) {
			if key == "" {
				return s
			}
			return key + "-" + s
		}
	}
	// An optional leading project key: "wl spec 77".
	if len(tokens) > 2 && spokenKey.MatchString(tokens[0]) && known(tokens[0]) {
		if _, isKind := spokenKinds[tokens[1]]; isKind {
			key, tokens = strings.ToUpper(tokens[0]), tokens[1:]
		}
	}
	typ, isTask := "", true
	if t, ok := spokenKinds[tokens[0]]; ok {
		typ, isTask, tokens = t, t == "", tokens[1:]
	}
	numTokens, section := tokens, ""
	for i, t := range tokens {
		if t == "section" || t == "sec" {
			numTokens = tokens[:i]
			var ok bool
			if section, ok = spokenSectionNumber(tokens[i+1:]); !ok {
				return s
			}
			break
		}
	}
	n, ok := spokenNumber(numTokens)
	if !ok || key == "" {
		return s
	}
	if isTask {
		if section != "" {
			return s
		}
		return key + "-" + n
	}
	ref := key + "-" + typ + "-" + n
	if section != "" {
		ref += "#sec-" + section
	}
	return ref
}

// spokenTokens lower-cases s and splits it into words, hyphens included as
// separators ("seventy-seven"), dropping filler words.
func spokenTokens(s string) []string {
	s = strings.NewReplacer("-", " ", "_", " ", ",", " ", ":", " ", "#", " ").Replace(strings.ToLower(s))
	var out []string
	for _, t := range strings.Fields(s) {
		if !spokenDropped[t] {
			out = append(out, t)
		}
	}
	return out
}

// spokenNumberWord is one number word's value and grammatical class.
type spokenNumberWord struct {
	v   int
	cls string // zero, unit, teen, tens, hundred, thousand, and
}

var spokenNumberWords = func() map[string]spokenNumberWord {
	m := map[string]spokenNumberWord{
		"zero": {0, "zero"}, "oh": {0, "zero"}, "o": {0, "zero"},
		"hundred": {100, "hundred"}, "thousand": {1000, "thousand"}, "and": {0, "and"},
	}
	for i, w := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine"} {
		m[w] = spokenNumberWord{i + 1, "unit"}
	}
	for i, w := range []string{"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen",
		"seventeen", "eighteen", "nineteen"} {
		m[w] = spokenNumberWord{i + 10, "teen"}
	}
	for i, w := range []string{"twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"} {
		m[w] = spokenNumberWord{(i + 2) * 10, "tens"}
	}
	return m
}()

// spokenNumber reads tokens as a number and returns its digits. Cardinal
// words combine ("seventy seven" is 77, "one thousand three hundred" and
// "thirteen hundred" are 1300); a word that cannot continue the number
// before it starts a new group, and the groups' digits are read one after
// another, the way ids are said aloud ("five twelve" is 512, "five one two"
// and "three oh five" are what they look like).
func spokenNumber(tokens []string) (string, bool) {
	if len(tokens) == 0 {
		return "", false
	}
	var out strings.Builder
	type group struct {
		total, cur int
		last       string
		thousand   bool
	}
	var g *group
	flush := func() {
		if g != nil {
			out.WriteString(strconv.Itoa(g.total + g.cur))
			g = nil
		}
	}
	after := func(classes ...string) bool {
		if g == nil {
			return false
		}
		for _, c := range classes {
			if g.last == c {
				return true
			}
		}
		return false
	}
	for _, t := range tokens {
		if spokenDigits.MatchString(t) {
			flush()
			out.WriteString(t)
			continue
		}
		w, ok := spokenNumberWords[t]
		if !ok {
			return "", false
		}
		switch w.cls {
		case "zero":
			flush()
			out.WriteString("0")
		case "and":
			if !after("hundred", "thousand") {
				return "", false
			}
			g.last = "and"
		case "hundred":
			if !after("unit", "teen", "tens") || g.cur >= 100 {
				return "", false
			}
			g.cur *= 100
			g.last = "hundred"
		case "thousand":
			if g == nil || g.thousand || g.total+g.cur == 0 {
				return "", false
			}
			g.total = (g.total + g.cur) * 1000
			g.cur, g.thousand, g.last = 0, true, "thousand"
		case "unit":
			if after("hundred", "thousand", "and") || (after("tens") && g.cur%10 == 0) {
				g.cur += w.v
				g.last = "unit"
				continue
			}
			flush()
			g = &group{cur: w.v, last: "unit"}
		default: // teen, tens
			if after("hundred", "thousand", "and") {
				g.cur += w.v
				g.last = w.cls
				continue
			}
			flush()
			g = &group{cur: w.v, last: w.cls}
		}
	}
	flush()
	return out.String(), out.Len() > 0
}

// spokenSectionNumber reads a section number: "3.2" as written, or words
// with "point"/"dot" between the parts ("three point two").
func spokenSectionNumber(tokens []string) (string, bool) {
	if len(tokens) == 1 && spokenSection.MatchString(tokens[0]) {
		return tokens[0], true
	}
	var parts []string
	var cur []string
	for _, t := range append(tokens, "point") {
		if t == "point" || t == "dot" {
			n, ok := spokenNumber(cur)
			if !ok {
				return "", false
			}
			parts, cur = append(parts, n), nil
			continue
		}
		cur = append(cur, t)
	}
	return strings.Join(parts, "."), true
}
