package model_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// markdownStructs are the entity and write-input structs of the eight kinds
// WL-RULE-1850 covers. Read-only views are out of its scope.
var markdownStructs = []any{
	model.Doc{}, model.CreateDocInput{}, model.UpdateDocBodyInput{}, model.DocColumnsInput{},
	model.PatchDocInput{}, model.DocNote{}, model.AddDocNoteInput{},
	model.Rule{}, model.AddRuleInput{}, model.EditRuleInput{},
	model.Task{}, model.CreateTaskInput{}, model.EditTaskInput{},
	model.Milestone{}, model.CreateMilestoneInput{},
	model.Deliverable{}, model.CreateDeliverableInput{}, model.EditDeliverableInput{},
	model.Idea{}, model.CreateIdeaInput{}, model.PatchIdeaInput{},
	model.Decision{}, model.DecisionInput{},
	model.Approval{}, model.RequireApprovalInput{}, model.DecideApprovalInput{},
}

// markdownFields are the fields WL-RULE-1850 lists, as "Type.Field".
var markdownFields = []string{
	"Doc.Title", "Doc.Body", "CreateDocInput.Body", "UpdateDocBodyInput.Body",
	"DocColumnsInput.Title", "PatchDocInput.Body", "PatchDocInput.Note",
	"DocNote.Body", "AddDocNoteInput.Body",
	"Rule.Heading", "Rule.Body", "AddRuleInput.Heading", "AddRuleInput.Body",
	"EditRuleInput.Heading", "EditRuleInput.Body",
	"Task.Title", "Task.Body", "CreateTaskInput.Title", "CreateTaskInput.Body",
	"EditTaskInput.Title", "EditTaskInput.Body",
	"Milestone.Title", "CreateMilestoneInput.Title",
	"Deliverable.Description", "CreateDeliverableInput.Description",
	"Idea.Title", "Idea.Body", "CreateIdeaInput.Title", "CreateIdeaInput.Body",
	"PatchIdeaInput.Title", "PatchIdeaInput.Body",
	"Decision.Question", "Decision.Context", "DecisionInput.Question", "DecisionInput.Context",
	"Approval.Note",
}

// TestMarkdownFields enforces WL-RULE-1850: the listed fields are
// model.Markdown, and every model.Markdown field has an extracting write path
// in internal/store's markdownWriters.
func TestMarkdownFields(t *testing.T) {
	writers := markdownWriterKeys(t)
	md := reflect.TypeOf(model.Markdown(""))
	isMD := func(ft reflect.Type) bool {
		return ft == md || (ft.Kind() == reflect.Pointer && ft.Elem() == md)
	}
	found := map[string]bool{}
	for _, v := range markdownStructs {
		st := reflect.TypeOf(v)
		for i := 0; i < st.NumField(); i++ {
			f := st.Field(i)
			name := st.Name() + "." + f.Name
			if !isMD(f.Type) {
				continue
			}
			found[name] = true
			if !writers[name] {
				t.Errorf("%s is model.Markdown but internal/store's markdownWriters names no write path for it", name)
			}
		}
	}
	for _, name := range markdownFields {
		if !found[name] {
			t.Errorf("%s must be model.Markdown (WL-RULE-1850)", name)
		}
	}
	var stale []string
	for name := range writers {
		if !found[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("markdownWriters names %s, which is no model.Markdown field of a listed struct", name)
	}
}

// markdownWriterKeys reads the keys of internal/store's markdownWriters map
// literal. It parses the source because internal/model imports stdlib only.
func markdownWriterKeys(t *testing.T) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../store/inlinerefs.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "markdownWriters" {
			return true
		}
		for _, e := range vs.Values[0].(*ast.CompositeLit).Elts {
			k, err := strconv.Unquote(e.(*ast.KeyValueExpr).Key.(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			keys[k] = true
		}
		return false
	})
	if len(keys) == 0 {
		t.Fatal("markdownWriters not found in internal/store/inlinerefs.go")
	}
	return keys
}
