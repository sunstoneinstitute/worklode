package mcp

import (
	"sort"
	"strings"
	"unicode"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Candidate limits for project_resolve (WL-REQ-1830).
const (
	matchFloor = 0.4
	matchMax   = 3
)

// candidate is one ranked answer to project_resolve. A list of candidates is
// never a selection, even when it holds one perfect match.
type candidate struct {
	Key        string
	Name       string
	Confidence float64
}

// matchProjects ranks projects against a spoken name. Confidence is the larger
// of the Levenshtein similarity and the Soundex score, taken over both the key
// and the name, on names lower-cased with spaces and punctuation dropped.
// Candidates below matchFloor are dropped, and at most matchMax come back,
// sorted by confidence then key.
func matchProjects(spoken string, projects []model.Project) []candidate {
	s := normalizeName(spoken)
	var out []candidate
	for _, p := range projects {
		c := max(nameScore(s, p.Key), nameScore(s, p.Name))
		if c >= matchFloor {
			out = append(out, candidate{Key: p.Key, Name: p.Name, Confidence: c})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence > out[j].Confidence
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > matchMax {
		out = out[:matchMax]
	}
	return out
}

// exactProject finds the project whose key, id or full name equals s, ignoring
// case. It is for project_use, which takes a ref rather than a spoken name.
func exactProject(s string, projects []model.Project) (model.Project, bool) {
	s = strings.TrimSpace(s)
	for _, p := range projects {
		if strings.EqualFold(s, p.Key) || strings.EqualFold(s, p.ID) || strings.EqualFold(s, p.Name) {
			return p, true
		}
	}
	return model.Project{}, false
}

// nameScore compares an already-normalized spoken name to a project's key or
// name. The Soundex score is 1 on equal codes and 0 otherwise.
func nameScore(spoken, other string) float64 {
	o := normalizeName(other)
	if spoken == "" || o == "" {
		return 0
	}
	score := similarity(spoken, o)
	if soundex(spoken) == soundex(o) {
		score = 1
	}
	return score
}

// normalizeName lower-cases s and drops everything but letters and digits.
func normalizeName(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// soundex returns the four-character American Soundex code of a normalized
// name, or "" when it has no ASCII letter.
func soundex(s string) string {
	const codes = "01230120022455012623010202" // a..z
	var out []byte
	var last byte
	for i := 0; i < len(s) && len(out) < 4; i++ {
		ch := s[i]
		if ch < 'a' || ch > 'z' {
			continue
		}
		code := codes[ch-'a']
		if len(out) == 0 {
			out = append(out, ch-'a'+'A')
			last = code
			continue
		}
		if code != last && code != '0' {
			out = append(out, code)
		}
		if ch != 'h' && ch != 'w' {
			last = code
		}
	}
	if len(out) == 0 {
		return ""
	}
	for len(out) < 4 {
		out = append(out, '0')
	}
	return string(out)
}
