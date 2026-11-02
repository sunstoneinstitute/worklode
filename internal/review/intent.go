package review

import (
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/typesafe"
)

// IntentSubject is the review subject a classified comment was made on.
type IntentSubject struct {
	Kind  string // doc, rule or change
	Title string
}

// intentCriteria are the WL-REQ-1318 intents, each by what it means and whom
// it obliges.
var intentCriteria = map[string]string{
	"note": "a remark: an observation, aside or praise that asks for nothing; it obliges nobody",
	"change_request": "asks the author to change the commented text; it obliges the author " +
		"to make the change before the next review round",
	"question": "asks the author to explain the commented text; it obliges the author " +
		"to answer now, without touching the artifact",
}

const intentInstructions = "A reviewer left the comment `threads.%[1]s.body` on the text " +
	"`threads.%[1]s.quote` of `subject`. Which intent does the comment carry?"

// IntentQuestions builds one Jev Choice question per thread with no intent,
// keyed "t<index into threads>", for one TypeSafe request per round
// (WL-REQ-1885). A request carries one state, so each thread's body and quote
// sit in state under its question id; crit replies arrive folded into the
// body by `lode review import`.
func IntentQuestions(subject IntentSubject, threads []model.OpenThreadInput) (state map[string]any, questions map[string]typesafe.Question) {
	per := map[string]any{}
	questions = map[string]typesafe.Question{}
	for i, th := range threads {
		if th.Intent != "" {
			continue
		}
		id := fmt.Sprintf("t%d", i)
		per[id] = map[string]any{"body": th.Body, "quote": th.Anchor.Quote}
		questions[id] = typesafe.Question{Type: "choice", Instructions: fmt.Sprintf(intentInstructions, id), Criteria: intentCriteria}
	}
	state = map[string]any{"subject": map[string]any{"kind": subject.Kind, "title": subject.Title}, "threads": per}
	return state, questions
}

// ApplyIntents sets the intent of every thread with none: Jev's choice with
// classifier jev when answers holds it, else change_request with classifier
// default (WL-REQ-1885). A nil answers is the fallback for every thread.
func ApplyIntents(threads []model.OpenThreadInput, answers map[string]typesafe.Answer) {
	for i := range threads {
		th := &threads[i]
		if th.Intent != "" {
			continue
		}
		a, ok := answers[fmt.Sprintf("t%d", i)]
		if ok && intentCriteria[a.Choice] != "" {
			th.Intent, th.IntentClassifier, th.IntentConfidence = a.Choice, "jev", a.Confidence
		} else {
			th.Intent, th.IntentClassifier, th.IntentConfidence = "change_request", "default", 0
		}
	}
}
