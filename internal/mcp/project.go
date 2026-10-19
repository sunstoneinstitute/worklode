package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// projectToolHint goes in every project tool's description (WL-REQ-1829).
const projectToolHint = " If the user's Claude project instructions contain `Worklode project: <name>`, call project_use with that name and from_instructions=true; otherwise confirm the project with the user first."

// handleTTL is how long a project handle stays valid (WL-REQ-1827).
const handleTTL = 24 * time.Hour

// elicitConfirm is the id of project_use's one input request and the name of
// its one boolean form field.
const elicitConfirm = "confirm"

// elicitVersion is the first MCP protocol with input-required results (D5).
const elicitVersion = "2026-07-28"

type projectListArgs struct {
	Cursor string `json:"cursor"`
}

type projectResolveArgs struct {
	Spoken string `json:"spoken"`
}

type projectUseArgs struct {
	Project          string `json:"project"`
	Confirmed        bool   `json:"confirmed"`
	FromInstructions bool   `json:"from_instructions"`
}

func (s *server) addProjectTools() {
	s.addTool("project_list", "List the Worklode projects, ten at a time."+projectToolHint,
		`{"type":"object","properties":{"cursor":{"type":"string","description":"The cursor from the previous page."}}}`,
		s.projectList)
	s.addTool("project_resolve", "Match a spoken project name against the Worklode projects and return ranked candidates. It never selects one: read the candidates back and wait for a yes before calling project_use with confirmed=true."+projectToolHint,
		`{"type":"object","properties":{"spoken":{"type":"string","description":"The project name as the user said it."}},"required":["spoken"]}`,
		s.projectResolve)
	s.addTool("project_use", "Confirm the Worklode project this conversation works in and return its project handle, which every project tool takes as project. project names a project exactly: its key, id or full name. Pass confirmed=true only after the user confirmed this exact project."+projectToolHint,
		`{"type":"object","properties":{"project":{"type":"string","description":"The project key, id or full name."},"confirmed":{"type":"boolean","description":"The user explicitly confirmed this exact project."},"from_instructions":{"type":"boolean","description":"The name comes from Worklode project: <name> in the user's Claude project instructions."}},"required":["project"]}`,
		s.projectUse)
}

func (s *server) addTool(name, desc, schema string, fn toolFunc) {
	s.srv.AddTool(&mcp.Tool{Name: name, Description: desc, InputSchema: json.RawMessage(schema)}, s.tool(name, fn))
}

// argsOf decodes a tool call's arguments into T.
func argsOf[T any](req *mcp.CallToolRequest) (T, error) {
	var a T
	if raw := req.Params.Arguments; len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return a, &toolError{outcome: outcomeInvalid, msg: "The tool arguments are not valid: " + err.Error()}
		}
	}
	return a, nil
}

func (s *server) projectList(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[projectListArgs](req)
	if err != nil {
		return nil, err
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return nil, err
	}
	page, next, err := pageOf(ps, a.Cursor, s.cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	sentence := fmt.Sprintf("Worklode has %d projects.", len(ps))
	if len(ps) == 1 {
		sentence = "Worklode has 1 project."
	}
	return say(sentence, struct {
		Projects []candidate `json:"projects"`
		Cursor   string      `json:"cursor,omitempty"`
	}{projectCandidates(page), next}), nil
}

// projectResolve ranks candidates and never selects one (WL-REQ-1830).
func (s *server) projectResolve(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[projectResolveArgs](req)
	if err != nil {
		return nil, err
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return nil, err
	}
	cands := matchProjects(a.Spoken, ps)
	if len(cands) == 0 {
		return confirmResult(fmt.Sprintf("No Worklode project sounds like %q; project_list shows them all.", a.Spoken), cands), nil
	}
	names := make([]string, len(cands))
	for i, cd := range cands {
		names[i] = fmt.Sprintf("%s (%s)", cd.Name, cd.Key)
	}
	sentence := "The closest Worklode project is " + names[0] + "."
	if len(names) > 1 {
		sentence = "The closest Worklode projects are " + strings.Join(names, ", ") + "."
	}
	return confirmResult(sentence, cands), nil
}

// projectUse returns a handle only for an exact name that the user confirmed,
// directly, through elicitation, or through their project instructions
// (WL-REQ-1827).
func (s *server) projectUse(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[projectUseArgs](req)
	if err != nil {
		return nil, err
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return nil, err
	}
	p, ok := exactProject(a.Project, ps)
	if !ok {
		return nil, confirmError(outcomeConfirmProject,
			fmt.Sprintf("No Worklode project is named exactly %q.", a.Project), matchProjects(a.Project, ps))
	}
	this := []candidate{{Key: p.Key, Name: p.Name}}
	if a.Confirmed || a.FromInstructions {
		return s.useProject(ctx, c, p)
	}
	if resp, answered := req.Params.InputResponses[elicitConfirm]; answered {
		if er, ok := resp.(*mcp.ElicitResult); ok && er.Action == "accept" && er.Content[elicitConfirm] != false {
			return s.useProject(ctx, c, p)
		}
		return nil, confirmError(outcomeConfirmProject,
			fmt.Sprintf("The user did not confirm %s (%s).", p.Name, p.Key), this)
	}
	if canElicit(req) {
		return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{elicitConfirm: &mcp.ElicitParams{
			Mode:    "form",
			Message: fmt.Sprintf("Use the Worklode project %s (%s) for this conversation?", p.Name, p.Key),
			RequestedSchema: json.RawMessage(`{"type":"object","properties":{"` + elicitConfirm +
				`":{"type":"boolean","title":"Use this project","default":true}},"required":["` + elicitConfirm + `"]}`),
		}}}, nil
	}
	return nil, confirmError(outcomeConfirmProject,
		fmt.Sprintf("Confirm %s (%s) with the user before using it.", p.Name, p.Key), this)
}

// canElicit is D5: protocol 2026-07-28 or later with form elicitation.
func canElicit(req *mcp.CallToolRequest) bool {
	caps := req.ClientCapabilities()
	return req.ProtocolVersion() >= elicitVersion && caps != nil && caps.Elicitation != nil && caps.Elicitation.Form != nil
}

func (s *server) useProject(ctx context.Context, c *cli.Client, p model.Project) (*mcp.CallToolResult, error) {
	who, _, err := c.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	handle := signHandle(s.cfg.SessionSecret, who.ID, p.Key, s.cfg.Now().Add(handleTTL))
	return say(fmt.Sprintf("This conversation now works in the Worklode project %s (%s).", p.Name, p.Key), struct {
		Project string `json:"project"`
		Key     string `json:"key"`
		Name    string `json:"name"`
		Use     string `json:"use"`
	}{handle, p.Key, p.Name, "Pass this handle as project to every Worklode tool in this conversation."}), nil
}
