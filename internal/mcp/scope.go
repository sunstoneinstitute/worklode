package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// confirmInstruction is the WL-REQ-1829 instruction, verbatim.
const confirmInstruction = "Confirm the project with the user before calling project_use. In a text or graphical interface, ask with your question tool (e.g. AskUserQuestion) offering these projects as options. In a voice conversation, read back the full project name and wait for the user to say yes."

// fromInstructionsShortcut names the from_instructions shortcut in every
// confirmation answer (WL-REQ-1829).
const fromInstructionsShortcut = "If the user's Claude project instructions contain `Worklode project: <name>`, call project_use with that name and from_instructions=true without asking."

// confirmAnswer is the detail of confirm_project, project_required and
// project_resolve answers (WL-REQ-1829).
type confirmAnswer struct {
	Candidates  []candidate `json:"candidates"`
	Instruction string      `json:"instruction"`
	Shortcut    string      `json:"shortcut"`
}

func confirmResult(sentence string, cands []candidate) *mcp.CallToolResult {
	if cands == nil {
		cands = []candidate{}
	}
	return say(sentence, confirmAnswer{cands, confirmInstruction, fromInstructionsShortcut})
}

// confirmError is a confirmation answer the dispatcher counts under outcome.
func confirmError(outcome, sentence string, cands []candidate) error {
	return &toolError{outcome: outcome, msg: sentence, res: confirmResult(sentence, cands)}
}

// requireProject is the first check of every project-scoped tool
// (WL-REQ-1828). A full ref in ref names its own project for that one item. A
// valid handle in project gives its key. A bare key, or a handle that is
// foreign, expired or malformed, answers confirm_project; no project at all
// answers project_required with the project list.
func (s *server) requireProject(ctx context.Context, c *cli.Client, project, ref string) (string, error) {
	if key, ok := fullRef(ref); ok {
		return key, nil
	}
	if project != "" {
		who, _, err := c.WhoAmI(ctx)
		if err != nil {
			return "", err
		}
		if key, reason := verifyHandle(s.cfg.SessionSecret, who.ID, project, s.cfg.Now()); reason == "" {
			return key, nil
		}
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return "", err
	}
	if project == "" {
		return "", confirmError(outcomeProjectRequired,
			"This tool needs a Worklode project confirmed with project_use first.", projectCandidates(ps))
	}
	name, _, _ := strings.Cut(project, ".")
	return "", confirmError(outcomeConfirmProject,
		fmt.Sprintf("The project %s is not confirmed for this conversation.", name), matchProjects(name, ps))
}

func listProjects(ctx context.Context, c *cli.Client) ([]model.Project, error) {
	r, _, err := c.ListProjects(ctx)
	return r.Projects, err
}

// projectCandidates is the project list as candidates without a confidence,
// at most one page.
func projectCandidates(ps []model.Project) []candidate {
	out := make([]candidate, 0, min(len(ps), pageSize))
	for _, p := range ps[:min(len(ps), pageSize)] {
		out = append(out, candidate{Key: p.Key, Name: p.Name})
	}
	return out
}
