package corpusindex

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// docRef mirrors internal/cli/render.go's DocRef (025 §14.3 shorthand:
// <PROJECTKEY>-<KIND>-<N>). Duplicated rather than imported: internal/cli
// pulls in net/http, which this package must stay clear of. Keep the two in
// sync if the shorthand ever changes.
func docRef(d model.Doc) string {
	if d.Number == 0 {
		return d.Kind
	}
	ref := strings.ToUpper(d.Kind) + "-" + strconv.Itoa(d.Number)
	if d.ProjectKey == "" {
		return ref
	}
	return d.ProjectKey + "-" + ref
}

// DocHeader is a doc chunk's context header (040 §4.3):
//
//	WL-SPEC-025 "Documents in the backbone" — §15.2 The ordered log
//
// number and heading name the chunk's section. Both "" renders the document
// reference and title alone, for a whole-document or unstructured chunk;
// heading alone (a plan's headings carry no number) drops the "§".
func DocHeader(doc model.Doc, number, heading string) string {
	h := fmt.Sprintf("%s %q", docRef(doc), doc.Title)
	switch {
	case number != "":
		h += fmt.Sprintf(" — §%s %s", number, heading)
	case heading != "":
		h += " — " + heading
	}
	return h
}

// TaskHeader is a task chunk's context header (040 §4.3):
//
//	WL-142 [feature/in_progress] Fix the thing
func TaskHeader(task model.Task) string {
	return fmt.Sprintf("%s [%s/%s] %s", task.ID, task.Kind, task.State, task.Title)
}

// SkillHeader is a skill chunk's context header (040 §4.3):
//
//	skill: test-driven-development — <description>
func SkillHeader(skill model.Skill) string {
	return fmt.Sprintf("skill: %s — %s", skill.Name, skill.Description)
}
