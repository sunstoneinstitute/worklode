package inlineref

import (
	"fmt"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
	"github.com/sunstoneinstitute/worklode/internal/typesafe"
)

// JevVersion is the jev classifier's classifier_version (WL-REQ-1856). Bump
// it whenever JevQuestions' code, its instructions, its criteria or JevModel
// changes; classify_version_test.go fails on a text change without a bump.
const JevVersion = 1

// JevModel is the TypeSafe model the jev classifier asks unless
// LODE_TYPESAFE_MODEL names another.
const JevModel = "jev-latest"

// jevInstructions asks for reference id's predicate; %[1]s is the id.
const jevInstructions = "Which relation does `node` have to `refs.%[1]s.target`, " +
	"as stated by `refs.%[1]s.line` and `refs.%[1]s.paragraph`?"

// referencesCriterion describes the fallback option (WL-REQ-1857).
const referencesCriterion = "refers to the target with no stronger relation"

// JevNode is the node whose text holds the references.
type JevNode struct {
	Kind    model.NodeKind
	Title   string
	DocKind string // a doc's kind, else ""
	Text    string // the field the references were scanned from
}

// JevRef is one reference to classify.
type JevRef struct {
	Kind    model.NodeKind
	Title   string
	DocKind string // a doc's kind, else ""
	Line    int    // 1-based line of node.Text holding the reference
}

// JevQuestions builds one Choice question per ref whose kind pair has
// candidates, keyed "r<index into refs>", for one TypeSafe request per node
// (WL-REQ-1857). A request carries one state, so the per-reference part (its
// line, paragraph and target) sits in state under the question's id.
func JevQuestions(node JevNode, refs []JevRef) (state map[string]any, questions map[string]typesafe.Question) {
	lines := strings.Split(node.Text, "\n")
	per := map[string]any{}
	questions = map[string]typesafe.Question{}
	for i, r := range refs {
		cands := Candidates(node.Kind, r.Kind, node.DocKind, r.DocKind)
		if cands == nil || r.Line < 1 || r.Line > len(lines) {
			continue
		}
		id := fmt.Sprintf("r%d", i)
		criteria := map[string]string{"references": referencesCriterion}
		for _, c := range cands {
			criteria[c] = ns.EdgeDefinitions[c]
		}
		questions[id] = typesafe.Question{Type: "choice", Instructions: fmt.Sprintf(jevInstructions, id), Criteria: criteria}
		per[id] = map[string]any{
			"line":      lines[r.Line-1],
			"paragraph": paragraph(lines, r.Line-1),
			"target":    map[string]any{"kind": r.Kind, "title": r.Title},
		}
	}
	state = map[string]any{"node": map[string]any{"kind": node.Kind, "title": node.Title}, "refs": per}
	return state, questions
}

// paragraph is the run of non-blank lines around lines[i].
func paragraph(lines []string, i int) string {
	start, end := i, i+1
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
		end++
	}
	return strings.Join(lines[start:end], "\n")
}
