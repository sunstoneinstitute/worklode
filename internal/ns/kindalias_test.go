package ns_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// TestKindAliases spot-checks the spoken kind words scripts/nsgen.py
// generates from the prefLabels and altLabels of ns/concept.ttl.
func TestKindAliases(t *testing.T) {
	for word, want := range map[string]ns.KindAlias{
		"bug":                 {Entity: "task", Kind: "bug"},
		"issue":               {Entity: "task", Kind: "bug"},
		"enhancement":         {Entity: "task", Kind: "feature"},
		"question":            {Entity: "task", Kind: "decision"},
		"decision":            {Entity: "task", Kind: "decision"},
		"design task":         {Entity: "task", Kind: "design"},
		"spec":                {Entity: "doc", Kind: "spec"},
		"adr":                 {Entity: "doc", Kind: "spec"},
		"design doc":          {Entity: "doc", Kind: "spec"},
		"implementation plan": {Entity: "doc", Kind: "plan"},
		"requirement":         {Entity: "rule", Kind: "requirement"},
		"rule":                {Entity: "rule", Kind: "requirement"},
		"catalogue":           {Entity: "rule", Kind: "catalogue"},
	} {
		if got, ok := ns.KindAliases[word]; !ok || got != want {
			t.Errorf("KindAliases[%q] = %+v, %v; want %+v", word, got, ok, want)
		}
	}
	// "design" alone may mean a spec or a design task, so it names neither;
	// "informative" is a retired rule kind and gets no words.
	for _, word := range []string{"design", "informative"} {
		if got, ok := ns.KindAliases[word]; ok {
			t.Errorf("KindAliases[%q] = %+v, want no entry", word, got)
		}
	}
}

// TestKindAliasesNameRealKinds holds every alias to a lower-case key and to a
// kind that its entity's scheme actually has.
func TestKindAliasesNameRealKinds(t *testing.T) {
	scheme := map[string]string{"task": "TaskKind", "doc": "DocKind", "rule": "RuleKind"}
	for word, alias := range ns.KindAliases {
		if word != strings.ToLower(word) {
			t.Errorf("KindAliases key %q is not lower-case", word)
		}
		name, ok := scheme[alias.Entity]
		if !ok {
			t.Errorf("KindAliases[%q].Entity = %q, want task, doc or rule", word, alias.Entity)
			continue
		}
		if !ns.Set(ns.Schemes[name])[alias.Kind] {
			t.Errorf("KindAliases[%q] = %+v, but wlc:%s has no %q", word, alias, name, alias.Kind)
		}
	}
}
