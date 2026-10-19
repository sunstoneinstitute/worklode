package mcp

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// listFetch is how many tasks task_list asks the API for; the tool pages
// through them ten at a time.
const listFetch = 200

const projectProp = `"project":{"type":"string","description":"The project handle from project_use."}`

type taskListArgs struct {
	Project  string `json:"project"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Assignee string `json:"assignee"`
	Cursor   string `json:"cursor"`
}

type taskAddArgs struct {
	Project    string   `json:"project"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Parent     string   `json:"parent"`
	BlockedBy  []string `json:"blocked_by"`
	GovernedBy []string `json:"governed_by"`
}

type taskUpdateArgs struct {
	Project  string  `json:"project"`
	ID       string  `json:"id"`
	Title    *string `json:"title"`
	Body     *string `json:"body"`
	Priority *string `json:"priority"`
}

type taskStateArgs struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	State   string `json:"state"`
	// Reason is accepted for the caller's benefit; the API has no field to
	// record it on a state change.
	Reason string `json:"reason"`
}

func (s *server) addTaskTools() {
	s.addTool("task_list", "List the project's tasks, ten at a time. The kind may be a spoken word such as \"bugs\".",
		`{"type":"object","properties":{`+projectProp+`,"kind":{"type":"string","description":"Task kind or an alias word, such as bug, bugs or chore."},"state":{"type":"string","description":"Only tasks in this state, such as ready or in_progress."},"assignee":{"type":"string","description":"Only tasks assigned to this actor."},"cursor":{"type":"string","description":"The cursor from the previous page."}}}`,
		s.taskList)
	s.addTool("task_add", "File a task in the project. blocked_by and governed_by are lists of task and rule refs.",
		`{"type":"object","properties":{`+projectProp+`,"kind":{"type":"string","description":"Task kind or an alias word, such as bug or feature."},"title":{"type":"string"},"body":{"type":"string"},"parent":{"type":"string","description":"The parent task's ref."},"blocked_by":{"type":"array","items":{"type":"string"},"description":"Tasks that block this one."},"governed_by":{"type":"array","items":{"type":"string"},"description":"Rules that govern this task."}},"required":["kind","title","body"]}`,
		s.taskAdd)
	s.addTool("task_update", "Change a task's title, body or priority.",
		`{"type":"object","properties":{`+projectProp+`,"id":{"type":"string","description":"The task's ref, as written or spoken."},"title":{"type":"string"},"body":{"type":"string"},"priority":{"type":"string"}},"required":["id"]}`,
		s.taskUpdate)
	s.addTool("task_set_state", "Move a task to a state: abandoned abandons it, ready or reopened reopens a delivered or abandoned one, and merged, deployed_dev, deployed_prod or released record delivery.",
		`{"type":"object","properties":{`+projectProp+`,"id":{"type":"string","description":"The task's ref, as written or spoken."},"state":{"type":"string"},"reason":{"type":"string"}},"required":["id","state"]}`,
		s.taskSetState)
}

// projectKeys returns the project list and its keys.
func projectKeys(ps []model.Project) []string {
	keys := make([]string, len(ps))
	for i, p := range ps {
		keys[i] = p.Key
	}
	return keys
}

var taskKinds = []string{"bug", "feature", "chore", "spike", "decision", "review", "design"}

// taskKind resolves a spoken kind word to a task kind. A trailing plural "s"
// is dropped when the word is not an alias itself ("bugs" files as "bug").
func taskKind(word string) (string, error) {
	w := normalizeKindWord(word)
	kind, ambiguous, closest := resolveKind(w)
	if kind == "" && !ambiguous {
		kind, ambiguous, closest = resolveKind(strings.TrimSuffix(w, "s"))
	}
	if ambiguous {
		return "", &toolError{outcome: outcomeInvalid, msg: `"design" could be a spec or a design task. Say "design task" or "spec".`}
	}
	if kind == "" {
		return "", &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("%q is not a task kind. Did you mean %s?", word, strings.Join(closest, ", "))}
	}
	if !slices.Contains(taskKinds, kind) {
		return "", &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("%q is not a task kind.", word)}
	}
	return kind, nil
}

func (s *server) taskList(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[taskListArgs](req)
	if err != nil {
		return nil, err
	}
	key, err := s.requireProject(ctx, c, a.Project, "")
	if err != nil {
		return nil, err
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return nil, err
	}
	p, _ := exactProject(key, ps)
	params := model.TaskListParams{Project: p.ID, Assignee: a.Assignee}
	if a.Kind != "" {
		if params.Kind, err = taskKind(a.Kind); err != nil {
			return nil, err
		}
	}
	if a.State != "" {
		params.States = []string{a.State}
	}
	resp, _, err := c.ListTasks(ctx, params)
	if err != nil {
		return nil, err
	}
	if len(resp.Tasks) > listFetch {
		resp.Tasks = resp.Tasks[:listFetch]
	}
	if len(resp.Tasks) == 0 {
		return say(fmt.Sprintf("No tasks in %s match.", p.Name), nil), nil
	}
	page, next, err := pageOf(resp.Tasks, a.Cursor, s.cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	cli.TaskTable(&b, page)
	if next != "" {
		fmt.Fprintf(&b, "\nMore tasks: call task_list again with cursor %q.\n", next)
	}
	return say(fmt.Sprintf("Found %d tasks in %s.", len(resp.Tasks), p.Name), b.String()), nil
}

// taskAdd files the task, then one edge call per blocked_by and governed_by
// entry. An edge failure after the task exists names the task and the failed
// edge and does not roll back.
func (s *server) taskAdd(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[taskAddArgs](req)
	if err != nil {
		return nil, err
	}
	key, err := s.requireProject(ctx, c, a.Project, "")
	if err != nil {
		return nil, err
	}
	kind, err := taskKind(a.Kind)
	if err != nil {
		return nil, err
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return nil, err
	}
	p, _ := exactProject(key, ps)
	keys := projectKeys(ps)
	in := model.CreateTaskInput{Project: p.ID, Kind: kind, Title: a.Title, Body: a.Body, Priority: "medium"}
	if a.Parent != "" {
		in.Parent = parseRef(a.Parent, key, keys)
	}
	t, _, err := c.CreateTask(ctx, in)
	if err != nil {
		return nil, err
	}
	for _, by := range a.BlockedBy {
		by = parseRef(by, key, keys)
		if _, err := c.Block(ctx, t.ID, by); err != nil {
			return nil, fmt.Errorf("Filed %s, but it is not blocked by %s: %w", t.ID, by, err)
		}
	}
	for _, rule := range a.GovernedBy {
		rule = parseRef(rule, key, keys)
		if _, err := c.Govern(ctx, t.ID, rule, false); err != nil {
			return nil, fmt.Errorf("Filed %s, but %s does not govern it: %w", t.ID, rule, err)
		}
	}
	return say(fmt.Sprintf("Filed %s, a %s task: %s.", t.ID, t.Kind, t.Title), nil), nil
}

func (s *server) taskUpdate(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[taskUpdateArgs](req)
	if err != nil {
		return nil, err
	}
	id, err := s.taskRef(ctx, c, a.Project, a.ID)
	if err != nil {
		return nil, err
	}
	if a.Title == nil && a.Body == nil && a.Priority == nil {
		return nil, &toolError{outcome: outcomeInvalid, msg: "Nothing to change: give a title, body or priority."}
	}
	t, _, err := c.EditTask(ctx, id, model.EditTaskInput{Title: a.Title, Body: a.Body, Priority: a.Priority})
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("Updated %s: %s.", t.ID, t.Title), nil), nil
}

func (s *server) taskSetState(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[taskStateArgs](req)
	if err != nil {
		return nil, err
	}
	id, err := s.taskRef(ctx, c, a.Project, a.ID)
	if err != nil {
		return nil, err
	}
	state := strings.ToLower(strings.TrimSpace(a.State))
	var t model.Task
	switch {
	case state == "abandoned" || state == "abandon":
		t, _, err = c.AbandonTask(ctx, id)
	case state == "ready" || state == "reopened" || state == "reopen":
		t, _, err = c.ReopenTask(ctx, id)
	case slices.Contains(model.SettableTaskStates, state):
		t, _, err = c.SetTaskState(ctx, id, state)
	default:
		return nil, &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("%q is not a state this tool sets. Use abandoned, ready, %s.", a.State, strings.Join(model.SettableTaskStates, ", "))}
	}
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("%s is now %s.", t.ID, t.State), nil), nil
}

// taskRef confirms the project and turns a spoken id into a canonical task id;
// a full ref names its own project (WL-REQ-1828).
func (s *server) taskRef(ctx context.Context, c *cli.Client, project, id string) (string, error) {
	key, err := s.requireProject(ctx, c, project, id)
	if err != nil {
		return "", err
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return "", err
	}
	return parseRef(id, key, projectKeys(ps)), nil
}
