// mcptasks.go holds the MCP task tools — task_list, task_add, task_update,
// task_set_state — and file, which turns the word the user said ("file this
// as a bug") into a task, a document or a rule through ns.KindAliases.
package api

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
)

func (s *server) addMCPTaskTools(srv *mcp.Server) {
	addMCPTool(s, srv, "file", "File something",
		"File a new item from the user's own word for it: \"file this as a bug\" is what=bug. The word "+
			"decides whether a task, a spec or plan, or a rule is created (bug, feature, chore, decision, "+
			"spec, plan, requirement, ...). An unknown word answers the closest kinds to read back."+mcpProjectHint,
		false, mcpFile)
	addMCPTool(s, srv, "task_list", "List tasks",
		"List tasks, open ones by default, ten at a time. kind takes a task kind or a word for one "+
			"(\"bugs\"); state takes one task state, or all."+mcpProjectHint,
		true, mcpTaskList)
	addMCPTool(s, srv, "task_add", "Add a task",
		"Create a task of a given kind (feature, bug, chore, design, review, spike, decision, rally), "+
			"optionally under a parent, blocked by other tasks, or governed by rules."+mcpProjectHint,
		false, mcpTaskAdd)
	addMCPTool(s, srv, "task_update", "Update a task",
		"Change a task's title, body or priority. Fields left out are unchanged."+mcpProjectHint,
		false, mcpTaskUpdate)
	addMCPTool(s, srv, "task_set_state", "Move a task",
		"Move a task to another state: ready (publish a draft, or reopen a delivered or abandoned task), "+
			"in_progress, in_review, merged, deployed_dev, deployed_prod, released, or abandoned. Whether "+
			"the move is allowed from where the task stands is Worklode's call."+mcpProjectHint,
		false, mcpTaskSetState)
}

// mcpOpenStates is task_list's default filter: the states not yet
// delivered, the same default `lode task list` uses.
var mcpOpenStates = []string{"draft", "ready", "in_progress", "in_review"}

func mcpTaskList(call *mcpCall, in model.MCPTaskListInput) (string, model.MCPTaskListOutput, error) {
	var out model.MCPTaskListOutput
	p, err := call.project(in.Project)
	if err != nil {
		return "", out, err
	}
	kind, err := aliasKind(in.Kind, "task")
	if err != nil {
		return "", out, err
	}
	states := mcpOpenStates
	switch st := strings.TrimSpace(in.State); st {
	case "":
	case "all":
		states = nil
	default:
		states = []string{st}
	}
	resp, _, err := call.c.ListTasks(call.ctx, model.TaskListParams{
		Project: p.ID, Kind: kind, States: states, Assignee: in.Assignee,
	})
	if err != nil {
		return "", out, err
	}
	page, next, err := mcpPage(resp.Tasks, in.Cursor)
	if err != nil {
		return "", out, err
	}
	out.Tasks = make([]model.MCPTaskItem, 0, len(page))
	for _, t := range page {
		out.Tasks = append(out.Tasks, mcpTaskItem(t))
	}
	out.NextCursor = next
	what := "task"
	if kind != "" {
		what = kind + " task"
	}
	if in.State == "" {
		what = "open " + what
	}
	text := fmt.Sprintf("%s has %s.\n", p.Name, mcpCount(len(resp.Tasks), what))
	if len(page) > 0 {
		text += "\n" + mcpRender(func(w io.Writer) { cli.TaskTable(w, page) }) + mcpMore(next, len(resp.Tasks))
	}
	return text, out, nil
}

func mcpTaskAdd(call *mcpCall, in model.MCPTaskAddInput) (string, model.MCPTaskOutput, error) {
	var out model.MCPTaskOutput
	p, err := call.project(in.Project)
	if err != nil {
		return "", out, err
	}
	t, text, err := call.addTask(p, in)
	if err != nil {
		return "", out, err
	}
	out.Task = mcpTaskItem(t)
	return text, out, nil
}

// addTask is task_add's write, which file shares: create the task, then
// record each blocker. A blocker that fails after the create still leaves
// the task, so the error says it exists.
func (call *mcpCall) addTask(p model.Project, in model.MCPTaskAddInput) (model.Task, string, error) {
	if strings.TrimSpace(in.Title) == "" {
		return model.Task{}, "", mcpInvalid("A task needs a title.")
	}
	priority := in.Priority
	if priority == "" {
		priority = "medium"
	}
	govern := make([]string, len(in.GovernedBy))
	for i, g := range in.GovernedBy {
		govern[i] = spokenRef(g, p.Key)
	}
	t, _, err := call.c.CreateTask(call.ctx, model.CreateTaskInput{
		Project: p.ID, Title: strings.TrimSpace(in.Title), Body: in.Body, Priority: priority,
		Kind: strings.ToLower(strings.TrimSpace(in.Kind)), Parent: call.taskRef(in.Parent, p),
		GovernedBy: govern,
	})
	if err != nil {
		return model.Task{}, "", err
	}
	text := fmt.Sprintf("Created %s task %s, %q, in %s.", t.Kind, t.ID, t.Title, p.Name)
	for _, by := range in.BlockedBy {
		ref := call.taskRef(by, p)
		if _, err := call.c.Block(call.ctx, t.ID, ref); err != nil {
			_, msg := mcpErrorText(err)
			return model.Task{}, "", &mcpToolError{outcome: "error",
				text: fmt.Sprintf("Created task %s, %q, but could not record that %s blocks it: %s", t.ID, t.Title, ref, msg)}
		}
	}
	if len(in.BlockedBy) > 0 {
		text += fmt.Sprintf(" It is blocked by %s.", strings.Join(in.BlockedBy, ", "))
	}
	return t, text, nil
}

func mcpTaskUpdate(call *mcpCall, in model.MCPTaskUpdateInput) (string, model.MCPTaskOutput, error) {
	var out model.MCPTaskOutput
	if in.Title == nil && in.Body == nil && in.Priority == nil {
		return "", out, mcpInvalid("Nothing to change: pass title, body or priority.")
	}
	p, err := call.project(in.Project)
	if err != nil {
		return "", out, err
	}
	t, _, err := call.c.EditTask(call.ctx, call.taskRef(in.ID, p), model.EditTaskInput{
		Title: in.Title, Body: in.Body, Priority: in.Priority,
	})
	if err != nil {
		return "", out, err
	}
	out.Task = mcpTaskItem(t)
	return fmt.Sprintf("Updated task %s, %q.", t.ID, t.Title), out, nil
}

func mcpTaskSetState(call *mcpCall, in model.MCPTaskSetStateInput) (string, model.MCPTaskOutput, error) {
	var out model.MCPTaskOutput
	p, err := call.project(in.Project)
	if err != nil {
		return "", out, err
	}
	id := call.taskRef(in.ID, p)
	state := strings.ToLower(strings.TrimSpace(in.State))
	cur, _, err := call.c.GetTask(call.ctx, id)
	if err != nil {
		return "", out, err
	}
	var t model.Task
	switch {
	case slices.Contains(model.SettableTaskStates, state):
		t, _, err = call.c.SetTaskState(call.ctx, id, state)
	case state == "abandoned" || state == "abandon":
		t, _, err = call.c.AbandonTask(call.ctx, id)
	case state == "ready" && cur.State == "draft":
		t, _, err = call.c.ReadyTask(call.ctx, id)
	case state == "ready" && cur.State == "in_progress":
		t, _, err = call.c.StopTask(call.ctx, id)
	case state == "ready":
		t, _, err = call.c.ReopenTask(call.ctx, id)
	case state == "in_review":
		t, _, err = call.c.SubmitTask(call.ctx, id)
	case state == "in_progress" && cur.State == "in_review":
		t, _, err = call.c.ReworkTask(call.ctx, id)
	case state == "in_progress":
		t, _, err = call.c.StartTask(call.ctx, id)
	default:
		return "", out, mcpInvalid("%q is not a state a task can be moved to. Use one of: ready, in_progress, "+
			"in_review, %s, abandoned.", in.State, strings.Join(model.SettableTaskStates, ", "))
	}
	if err != nil {
		return "", out, err
	}
	out.Task = mcpTaskItem(t)
	return fmt.Sprintf("Moved task %s, %q, from %s to %s.", t.ID, t.Title, cur.State, t.State), out, nil
}

func mcpFile(call *mcpCall, in model.MCPFileInput) (string, model.MCPFileOutput, error) {
	var out model.MCPFileOutput
	alias, err := lookupKindAlias(in.What)
	if err != nil {
		return "", out, err
	}
	if in.Parent != "" && alias.Entity != "task" {
		return "", out, mcpInvalid("parent applies to tasks only, and %q files a %s.", in.What, alias.Entity)
	}
	if in.Doc != "" && !(alias.Entity == "rule" || (alias.Entity == "doc" && alias.Kind == "plan")) {
		return "", out, mcpInvalid("doc applies to a plan (the spec or rule it covers) or a rule (the spec it is arranged in), and %q files a %s %s.",
			in.What, alias.Kind, alias.Entity)
	}
	p, err := call.project(in.Project)
	if err != nil {
		return "", out, err
	}
	out.Entity, out.Kind = alias.Entity, alias.Kind
	var text string
	switch alias.Entity {
	case "task":
		var t model.Task
		t, text, err = call.addTask(p, model.MCPTaskAddInput{Kind: alias.Kind, Title: in.Title, Body: in.Body, Parent: in.Parent})
		out.Ref, out.Title = t.ID, t.Title
	case "doc":
		add := model.MCPDocAddInput{Kind: alias.Kind, Title: in.Title, Body: in.Body}
		if in.Doc != "" {
			add.Covers = []string{in.Doc}
		}
		var d model.Doc
		d, text, err = call.addDoc(p, add)
		out.Ref, out.Title = cli.DocRef(d), d.Title
	case "rule":
		body := in.Body
		if strings.TrimSpace(body) == "" {
			body = in.Title
		}
		var r model.Rule
		r, text, err = call.addRule(p, model.MCPRuleAddInput{Heading: in.Title, Body: body, Kind: alias.Kind, Doc: in.Doc})
		out.Ref, out.Title = r.Ref, r.Heading
	default:
		return "", out, fmt.Errorf("kind alias %q names entity %q, which file cannot create", in.What, alias.Entity)
	}
	if err != nil {
		return "", out, err
	}
	return text, out, nil
}

// lookupKindAlias reads a spoken kind word through ns.KindAliases, singular
// or plural. An unknown word fails with the closest known words.
func lookupKindAlias(word string) (ns.KindAlias, error) {
	w := strings.ToLower(strings.TrimSpace(word))
	if a, ok := ns.KindAliases[w]; ok {
		return a, nil
	}
	if a, ok := ns.KindAliases[strings.TrimSuffix(w, "s")]; ok {
		return a, nil
	}
	if w == "design" || w == "designs" {
		return ns.KindAlias{}, mcpInvalid("%q could mean a design task or a spec. Ask the user which, then call "+
			"file with what set to \"design task\" or \"spec\".", word)
	}
	type near struct {
		word  string
		score float64
	}
	var nears []near
	for k := range ns.KindAliases {
		nears = append(nears, near{k, similarity(w, k)})
	}
	slices.SortFunc(nears, func(a, b near) int {
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		return strings.Compare(a.word, b.word)
	})
	var closest []string
	for _, n := range nears[:min(3, len(nears))] {
		a := ns.KindAliases[n.word]
		closest = append(closest, fmt.Sprintf("%s (a %s %s)", n.word, a.Kind, a.Entity))
	}
	return ns.KindAlias{}, mcpInvalid("Worklode has no kind called %q. The closest are %s; read them back "+
		"to the user and call file again with the one they mean.", word, orList(closest))
}

// aliasKind reads a list filter's kind word: "" is any kind, a word for a
// kind of entity is that kind, a word for another entity's kind is refused,
// and anything else passes through for the API to validate.
func aliasKind(word, entity string) (string, error) {
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return "", nil
	}
	a, ok := ns.KindAliases[w]
	if !ok {
		a, ok = ns.KindAliases[strings.TrimSuffix(w, "s")]
	}
	if !ok {
		return w, nil
	}
	if a.Entity != entity {
		return "", mcpInvalid("%q is a kind of %s, not of %s; use the %s_list tool for it.", word, a.Entity, entity, a.Entity)
	}
	return a.Kind, nil
}

// taskRef reads a task argument: a spoken form, a bare number or a full id.
func (call *mcpCall) taskRef(arg string, p model.Project) string {
	if strings.TrimSpace(arg) == "" {
		return ""
	}
	return spokenRef(arg, p.Key)
}

func mcpTaskItem(t model.Task) model.MCPTaskItem {
	return model.MCPTaskItem{ID: t.ID, Title: t.Title, Kind: t.Kind, State: t.State, Priority: t.Priority, Assignee: t.Assignee}
}
