package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// decisionInstruction is the WL-REQ-1843 instruction, verbatim.
const decisionInstruction = "Ask the user with your question tool (e.g. AskUserQuestion), offering these options. In a voice conversation, read the question and options and wait for the answer. Then call decision_resolve with that answer and confirmed=true."

// elicitAnswer is the id of decision_resolve's input request and the name of
// its free-text field; elicitChoice is the field offering the options.
const (
	elicitAnswer = "answer"
	elicitChoice = "choice"
)

type decisionListArgs struct {
	Project string `json:"project"`
	Task    string `json:"task"`
	Cursor  string `json:"cursor"`
}

type decisionShowArgs struct {
	Project string `json:"project"`
	Task    string `json:"task"`
	Key     string `json:"key"`
}

type decisionAddArgs struct {
	Project  string   `json:"project"`
	Task     string   `json:"task"`
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

type decisionResolveArgs struct {
	Project   string `json:"project"`
	Task      string `json:"task"`
	Key       string `json:"key"`
	Answer    string `json:"answer"`
	Confirmed bool   `json:"confirmed"`
}

const decisionTaskProp = `"task":{"type":"string","description":"The task's ref, as written or spoken."}`
const decisionKeyProp = `"key":{"type":"string","description":"The decision's key on the task."}`

func (s *server) addDecisionTools() {
	s.addTool("decision_list", "List the questions a task poses, ten at a time.",
		`{"type":"object","properties":{`+projectProp+`,`+decisionTaskProp+`,"cursor":{"type":"string","description":"The cursor from the previous page."}},"required":["task"]}`,
		s.decisionList)
	s.addTool("decision_show", "Show one question a task poses, with its options and its answer once it has one.",
		`{"type":"object","properties":{`+projectProp+`,`+decisionTaskProp+`,`+decisionKeyProp+`},"required":["task","key"]}`,
		s.decisionShow)
	s.addTool("decision_add", "Pose a question on a task. With options the user picks one or answers in their own words; without, the answer is free text.",
		`{"type":"object","properties":{`+projectProp+`,`+decisionTaskProp+`,"question":{"type":"string"},"options":{"type":"array","items":{"type":"string"},"description":"The choices to offer."}},"required":["task","question"]}`,
		s.decisionAdd)
	s.addTool("decision_resolve", "Record the user's answer to a question a task poses. The answer is the user's call: never answer on their behalf. Pass confirmed=true only with the answer the user gave.",
		`{"type":"object","properties":{`+projectProp+`,`+decisionTaskProp+`,`+decisionKeyProp+`,"answer":{"type":"string","description":"The user's answer: an option's label or their own words."},"confirmed":{"type":"boolean","description":"The user gave this answer."}},"required":["task","key"]}`,
		s.decisionResolve)
}

func (s *server) decisionList(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[decisionListArgs](req)
	if err != nil {
		return nil, err
	}
	task, err := s.taskRef(ctx, c, a.Project, a.Task)
	if err != nil {
		return nil, err
	}
	rows, _, err := c.ListDecisions(ctx, task)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return say(task+" poses no questions.", nil), nil
	}
	page, next, err := pageOf(rows, a.Cursor, s.cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	cli.DecisionTable(&b, page)
	if next != "" {
		fmt.Fprintf(&b, "\nMore questions: call decision_list again with cursor %q.\n", next)
	}
	return say(fmt.Sprintf("%s poses %d questions.", task, len(rows)), b.String()), nil
}

func (s *server) decisionShow(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[decisionShowArgs](req)
	if err != nil {
		return nil, err
	}
	task, err := s.taskRef(ctx, c, a.Project, a.Task)
	if err != nil {
		return nil, err
	}
	d, _, err := c.GetDecision(ctx, task, a.Key)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	cli.DecisionRender(&b, d)
	return say(fmt.Sprintf("%s/%s asks: %s", d.Task, d.Key, d.Question), b.String()), nil
}

var nonKey = regexp.MustCompile(`[^a-z0-9]+`)

// decisionKey derives a key from the question: lower case, hyphens, at most
// 40 characters.
func decisionKey(question string) string {
	k := strings.Trim(nonKey.ReplaceAllString(strings.ToLower(question), "-"), "-")
	if len(k) > 40 {
		k = strings.TrimRight(k[:40], "-")
	}
	if k == "" {
		k = "question"
	}
	return k
}

// decisionAdd poses pick_or_freetext with options and freetext without, so
// the answer is always an option or the user's own words.
func (s *server) decisionAdd(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[decisionAddArgs](req)
	if err != nil {
		return nil, err
	}
	task, err := s.taskRef(ctx, c, a.Project, a.Task)
	if err != nil {
		return nil, err
	}
	in := model.DecisionInput{Key: decisionKey(a.Question), Question: model.Markdown(a.Question), ResponseType: "freetext"}
	if len(a.Options) > 0 {
		in.ResponseType = "pick_or_freetext"
		for _, o := range a.Options {
			in.Options = append(in.Options, model.DecisionOption{Label: o})
		}
	}
	d, _, err := c.AddDecision(ctx, task, in)
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("Posed %s/%s: %s", d.Task, d.Key, d.Question), nil), nil
}

// decisionResolve records an answer only when the user gave it: a confirmed
// answer, or an accepted elicitation. Otherwise it returns the question, the
// options and the WL-REQ-1843 instruction, and records nothing.
func (s *server) decisionResolve(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[decisionResolveArgs](req)
	if err != nil {
		return nil, err
	}
	task, err := s.taskRef(ctx, c, a.Project, a.Task)
	if err != nil {
		return nil, err
	}
	d, _, err := c.GetDecision(ctx, task, a.Key)
	if err != nil {
		return nil, err
	}
	if d.Answer != nil {
		return nil, &toolError{outcome: outcomeConflict, msg: fmt.Sprintf("%s/%s is already answered.", d.Task, d.Key)}
	}
	if a.Confirmed && a.Answer != "" {
		return s.recordAnswer(ctx, c, d, a.Answer)
	}
	if resp, answered := req.Params.InputResponses[elicitAnswer]; answered {
		if er, ok := resp.(*mcp.ElicitResult); ok && er.Action == "accept" {
			choice, _ := er.Content[elicitChoice].(string)
			own, _ := er.Content[elicitAnswer].(string)
			if ans := joinAnswer(choice, own); ans != "" {
				return s.recordAnswer(ctx, c, d, ans)
			}
		}
		return say(fmt.Sprintf("The user did not answer %s/%s; nothing was recorded.", d.Task, d.Key), nil), nil
	}
	opts := decisionOptions(d)
	if canElicit(req) {
		props := map[string]any{elicitAnswer: map[string]any{"type": "string", "title": "Your answer"}}
		if len(opts) > 0 {
			props[elicitChoice] = map[string]any{"type": "string", "title": "Choose an option", "enum": opts}
			props[elicitAnswer] = map[string]any{"type": "string", "title": "Or answer in your own words"}
		}
		schema, _ := json.Marshal(map[string]any{"type": "object", "properties": props})
		return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{elicitAnswer: &mcp.ElicitParams{
			Mode: "form", Message: string(d.Question), RequestedSchema: json.RawMessage(schema),
		}}}, nil
	}
	return say(fmt.Sprintf("Ask the user: %s Nothing was recorded.", d.Question), struct {
		Question    string   `json:"question"`
		Options     []string `json:"options,omitempty"`
		Instruction string   `json:"instruction"`
	}{string(d.Question), opts, decisionInstruction}), nil
}

// decisionOptions are the choices to offer: the option labels, or yes, no
// and unsure for a yes_no question.
func decisionOptions(d model.Decision) []string {
	if d.ResponseType == "yes_no" {
		return []string{"yes", "no", "unsure"}
	}
	out := make([]string, len(d.Options))
	for i, o := range d.Options {
		out[i] = o.Label
	}
	return out
}

// joinAnswer combines an elicited choice and free text as "choice: text".
func joinAnswer(choice, own string) string {
	choice, own = strings.TrimSpace(choice), strings.TrimSpace(own)
	switch {
	case choice == "":
		return own
	case own == "":
		return choice
	}
	return choice + ": " + own
}

func (s *server) recordAnswer(ctx context.Context, c *cli.Client, d model.Decision, answer string) (*mcp.CallToolResult, error) {
	got, _, err := c.AnswerDecision(ctx, d.Task, d.Key, answerFor(d, answer))
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("Recorded the answer to %s/%s.", got.Task, got.Key), nil), nil
}

// answerFor shapes a spoken answer as the decision's response type expects:
// an option label is a pick, "label: text" is a pick with notes, comma
// separated labels are a multi_select's picks, anything else is free text.
// The API refuses a shape the type does not take.
func answerFor(d model.Decision, answer string) model.DecisionAnswer {
	answer = strings.TrimSpace(answer)
	label := func(s string) (string, bool) {
		for _, o := range d.Options {
			if strings.EqualFold(o.Label, strings.TrimSpace(s)) {
				return o.Label, true
			}
		}
		return "", false
	}
	switch d.ResponseType {
	case "yes_no":
		return model.DecisionAnswer{Value: strings.ToLower(answer)}
	case "freetext":
		return model.DecisionAnswer{Freetext: answer}
	case "multi_select":
		var picked []string
		for _, part := range strings.Split(answer, ",") {
			l, ok := label(part)
			if !ok {
				l = strings.TrimSpace(part)
			}
			picked = append(picked, l)
		}
		return model.DecisionAnswer{Picked: picked}
	}
	if l, ok := label(answer); ok {
		return model.DecisionAnswer{Picked: []string{l}}
	}
	if pick, notes, ok := strings.Cut(answer, ":"); ok && d.ResponseType == "single_select_notes" {
		if l, ok := label(pick); ok {
			return model.DecisionAnswer{Picked: []string{l}, Notes: strings.TrimSpace(notes)}
		}
	}
	if d.ResponseType == "pick_or_freetext" {
		return model.DecisionAnswer{Freetext: answer}
	}
	return model.DecisionAnswer{Picked: []string{answer}}
}
