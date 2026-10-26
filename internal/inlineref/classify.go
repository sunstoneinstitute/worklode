package inlineref

import (
	"cmp"
	"regexp"
	"slices"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// CueVersion is the cue classifier's classifier_version (WL-REQ-1856). Bump it
// whenever Cue's code or ns.EdgeLabels changes; classify_version_test.go fails
// on a label change without a bump.
const CueVersion = 1

// Candidates lists the predicates a node kind pair allows (WL-REQ-1857).
// fromDocKind and toDocKind matter only for doc → doc, where blockedBy needs
// two plans. Every other pair returns nil.
func Candidates(from, to model.NodeKind, fromDocKind, toDocKind string) []string {
	switch {
	case from == "task" && to == "task":
		return []string{"dependsOn", "childOf", "followUpTo", "duplicateOf"}
	case from == "rule" && to == "rule":
		return []string{"refines", "needs", "constrains", "conflictsWith", "amends", "wasDerivedFrom"}
	case from == "doc" && to == "doc":
		if fromDocKind == "plan" && toDocKind == "plan" {
			return []string{"requires", "wasDerivedFrom", "blockedBy"}
		}
		return []string{"requires", "wasDerivedFrom"}
	case from == "task" && to == "rule":
		return []string{"governedBy"}
	}
	return nil
}

// cuePhrase is one ns.EdgeLabels phrase, matched case-insensitively on word
// boundaries.
type cuePhrase struct {
	predicate string
	re        *regexp.Regexp
}

// cuePhrases holds every label, longest first, so "requires task" is tried
// before "requires".
var cuePhrases = func() []cuePhrase {
	labels := make([]string, 0, len(ns.EdgeLabels))
	for l := range ns.EdgeLabels {
		labels = append(labels, l)
	}
	slices.SortFunc(labels, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), cmp.Compare(a, b))
	})
	out := make([]cuePhrase, len(labels))
	for i, l := range labels {
		out[i] = cuePhrase{ns.EdgeLabels[l], regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(l) + `\b`)}
	}
	return out
}()

// Cue returns the predicate a label phrase on line names, or "" (WL-REQ-1857).
// Only phrases of a candidate predicate count. A phrase naming an inverse
// property is not a hit: ns.EdgeLabels carries no inverse labels, so none
// can match.
func Cue(line string, candidates []string) string {
	for _, p := range cuePhrases {
		if slices.Contains(candidates, p.predicate) && p.re.MatchString(line) {
			return p.predicate
		}
	}
	return ""
}
