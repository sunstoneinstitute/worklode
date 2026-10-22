package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
)

type fileArgs struct {
	Project string `json:"project"`
	What    string `json:"what"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Parent  string `json:"parent"`
	Doc     string `json:"doc"`
}

func (s *server) addFileTools() {
	s.addTool("file", "File something by what it is: \"bug\", \"feature\", \"spec\", \"plan\", \"review\", \"invariant\" and the other words in the kind table. A word that fits no kind, or two kinds, creates nothing and answers with the closest kinds. doc names the document a review or rule is about.",
		`{"type":"object","properties":{`+projectProp+`,"what":{"type":"string","description":"What is being filed, as spoken."},"title":{"type":"string"},"body":{"type":"string"},"parent":{"type":"string","description":"A task's parent task."},"doc":{"type":"string","description":"The document a review or rule is about."}},"required":["what","title"]}`,
		s.file)
}

// file routes what to the task, document or rule add path (WL-REQ-1833).
func (s *server) file(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[fileArgs](req)
	if err != nil {
		return nil, err
	}
	word := normalizeKindWord(a.What)
	if _, ok := KindAliases[word]; !ok {
		word = strings.TrimSuffix(word, "s") // "bugs" files as "bug"
	}
	kind, ambiguous, closest := resolveKind(word)
	switch {
	case ambiguous:
		return nil, &toolError{outcome: outcomeInvalid, msg: `"design" could be a spec or a design task. Say "design task" or "spec". Nothing was filed.`}
	case kind == "":
		return nil, &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("%q is not something I can file. Did you mean %s? Nothing was filed.", a.What, strings.Join(closest, ", "))}
	}
	entity := KindAliases[word].Entity
	if a.Doc != "" && kind != ns.TaskKindReview && entity != "rule" {
		return nil, &toolError{outcome: outcomeInvalid, msg: "doc applies to a review or a rule. Nothing was filed."}
	}
	body := a.Body
	var d model.Doc
	if a.Doc != "" {
		if d, err = s.docID(ctx, c, a.Project, a.Doc); err != nil {
			return nil, err
		}
		if kind == ns.TaskKindReview {
			body = strings.TrimSpace(fmt.Sprintf("Review %s.\n\n%s", cli.DocRef(d), body))
		}
	}
	switch entity {
	case "task":
		return s.addTask(ctx, c, taskAddArgs{Project: a.Project, Kind: word, Title: a.Title, Body: body, Parent: a.Parent})
	case "doc":
		return s.addDoc(ctx, c, docAddArgs{Project: a.Project, Kind: word, Title: a.Title, Body: body})
	}
	r, err := s.addRule(ctx, c, ruleAddArgs{Project: a.Project, Heading: a.Title, Body: body, Kind: word})
	if err != nil {
		return nil, err
	}
	if a.Doc != "" {
		if _, _, err := c.ArrangeRule(ctx, d.ID, model.ArrangeRuleInput{Rule: r.Ref}); err != nil {
			return nil, fmt.Errorf("Added %s, but it is not arranged in %s: %w", r.Ref, cli.DocRef(d), err)
		}
	}
	return say(fmt.Sprintf("Added the draft %s %s: %s.", r.Kind, r.Ref, r.Heading), nil), nil
}
