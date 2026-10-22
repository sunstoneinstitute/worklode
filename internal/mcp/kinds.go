package mcp

import (
	"sort"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// KindTarget is what a spoken kind word files as (WL-REQ-1832).
type KindTarget struct {
	Entity string // "task", "doc" or "rule"
	Kind   string
}

// KindAliases maps each word the table in WL-REQ-1832 lists to its target.
// "design" alone is deliberately absent: it is ambiguous.
var KindAliases = map[string]KindTarget{}

func init() {
	add := func(entity, kind string, words ...string) {
		for _, w := range words {
			KindAliases[w] = KindTarget{Entity: entity, Kind: kind}
		}
	}
	// Task words come from ns/task-kinds.ttl's skos:altLabel.
	for w, kind := range ns.TaskKindAliases {
		add("task", kind, w)
	}
	add("doc", "spec", "spec", "specification", "design doc", "design document", "adr")
	add("doc", "plan", "plan", "implementation plan")
	add("rule", "requirement", "requirement", "rule")
	add("rule", "invariant", "invariant")
	add("rule", "definition", "definition")
	add("rule", "principle", "principle")
	add("rule", "catalogue", "catalogue")
}

func normalizeKindWord(w string) string {
	return strings.Join(strings.Fields(strings.ToLower(w)), " ")
}

// resolveKind turns a spoken word into a kind. "design" alone is ambiguous.
// An unknown word returns no kind and the three alias words closest to it, for
// the caller to read back without creating anything (WL-REQ-1833).
func resolveKind(word string) (kind string, ambiguous bool, closest []string) {
	w := normalizeKindWord(word)
	if w == "design" {
		return "", true, nil
	}
	if t, ok := KindAliases[w]; ok {
		return t.Kind, false, nil
	}
	words := make([]string, 0, len(KindAliases))
	for a := range KindAliases {
		words = append(words, a)
	}
	sort.Strings(words)
	sort.SliceStable(words, func(i, j int) bool { return similarity(w, words[i]) > similarity(w, words[j]) })
	return "", false, words[:3]
}
