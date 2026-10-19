package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

type ruleListArgs struct {
	Project string `json:"project"`
	Doc     string `json:"doc"`
	Cursor  string `json:"cursor"`
}

type ruleAddArgs struct {
	Project string `json:"project"`
	Heading string `json:"heading"`
	Body    string `json:"body"`
	Kind    string `json:"kind"`
}

type ruleEditArgs struct {
	Project string  `json:"project"`
	ID      string  `json:"id"`
	Body    *string `json:"body"`
	Heading *string `json:"heading"`
}

type ruleLinkArgs struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	Rel     string `json:"rel"`
	Target  string `json:"target"`
	Remove  bool   `json:"remove"`
}

type ruleSupersedeArgs struct {
	Project string `json:"project"`
	Map     string `json:"map"`
}

func (s *server) addRuleTools() {
	s.addTool("rule_list", "List the project's rules, ten at a time. With doc, list the rules that document arranges, in order.",
		`{"type":"object","properties":{`+projectProp+`,"doc":{"type":"string","description":"A spec's ref, as written or spoken."},"cursor":{"type":"string","description":"The cursor from the previous page."}}}`,
		s.ruleList)
	s.addTool("rule_add", "Add a rule as a draft you own, arranged in no document. kind is requirement unless you say otherwise.",
		`{"type":"object","properties":{`+projectProp+`,"heading":{"type":"string"},"body":{"type":"string","description":"The text under the heading."},"kind":{"type":"string","description":"requirement, catalogue, invariant, definition, principle or an alias word."}},"required":["heading","body"]}`,
		s.ruleAdd)
	s.addTool("rule_edit", "Write a rule's next draft version. Give a body, a heading or both; what you leave out stays as it is.",
		`{"type":"object","properties":{`+projectProp+`,"id":{"type":"string","description":"The rule's ref, as written or spoken."},"body":{"type":"string"},"heading":{"type":"string"}},"required":["id"]}`,
		s.ruleEdit)
	s.addTool("rule_link", "Relate a rule to another, or remove the relation with remove. rel is refines, needs, constrains, conflictsWith, references, amends or wasDerivedFrom.",
		`{"type":"object","properties":{`+projectProp+`,"id":{"type":"string","description":"The rule's ref, as written or spoken."},"rel":{"type":"string"},"target":{"type":"string","description":"The other rule's ref."},"remove":{"type":"boolean","description":"Remove the relation instead of adding it."}},"required":["id","rel","target"]}`,
		s.ruleLink)
	s.addTool("rule_supersede", "Apply a refactor map: withdraw old rules and link each to its successors. One line per entry, \"<old> -> <new> [<new> ...]\"; no successor withdraws the old rule.",
		`{"type":"object","properties":{`+projectProp+`,"map":{"type":"string","description":"The refactor map, one entry per line."}},"required":["map"]}`,
		s.ruleSupersede)
}

// ruleKind resolves a spoken word to a rule kind.
func ruleKind(word string) (string, error) {
	if _, ok := KindAliases[normalizeKindWord(word)]; !ok {
		word = strings.TrimSuffix(normalizeKindWord(word), "s")
	}
	kind, _, closest := resolveKind(word)
	switch {
	case kind == "":
		return "", &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("%q is not a rule kind. Did you mean %s?", word, strings.Join(closest, ", "))}
	case KindAliases[normalizeKindWord(word)].Entity != "rule":
		return "", &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("%q is not a rule kind. Rules are requirements, catalogues, invariants, definitions and principles.", word)}
	}
	return kind, nil
}

// ruleProject confirms the project and returns it with the project keys.
func (s *server) ruleProject(ctx context.Context, c *cli.Client, project, ref string) (model.Project, []string, error) {
	key, err := s.requireProject(ctx, c, project, ref)
	if err != nil {
		return model.Project{}, nil, err
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return model.Project{}, nil, err
	}
	p, _ := exactProject(key, ps)
	return p, projectKeys(ps), nil
}

func notFoundAs(err error, ref string) error {
	var ce *cli.ClientError
	if errors.As(err, &ce) && ce.Status == http.StatusNotFound {
		return &toolError{outcome: outcomeNotFound, msg: fmt.Sprintf("Nothing in Worklode is called %s.", ref)}
	}
	return err
}

func (s *server) ruleList(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[ruleListArgs](req)
	if err != nil {
		return nil, err
	}
	p, keys, err := s.ruleProject(ctx, c, a.Project, a.Doc)
	if err != nil {
		return nil, err
	}
	params := model.RuleListParams{Project: p.ID}
	if a.Doc != "" {
		params.Doc = parseRef(a.Doc, p.Key, keys)
	}
	rules, _, err := c.ListRules(ctx, params)
	if err != nil {
		return nil, notFoundAs(err, params.Doc)
	}
	if len(rules) == 0 {
		return say(fmt.Sprintf("No rules in %s match.", p.Name), nil), nil
	}
	page, next, err := pageOf(rules, a.Cursor, s.cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	cli.RulesTable(&b, page)
	if next != "" {
		fmt.Fprintf(&b, "\nMore rules: call rule_list again with cursor %q.\n", next)
	}
	return say(fmt.Sprintf("Found %d rules in %s.", len(rules), p.Name), b.String()), nil
}

func (s *server) ruleAdd(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[ruleAddArgs](req)
	if err != nil {
		return nil, err
	}
	p, _, err := s.ruleProject(ctx, c, a.Project, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Heading) == "" {
		return nil, &toolError{outcome: outcomeInvalid, msg: "The rule needs a heading."}
	}
	in := model.AddRuleInput{Project: p.ID, Heading: strings.TrimSpace(a.Heading), Body: a.Body}
	if a.Kind != "" {
		if in.Kind, err = ruleKind(a.Kind); err != nil {
			return nil, err
		}
	}
	r, _, err := c.AddRule(ctx, in)
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("Added the draft %s %s: %s.", r.Kind, r.Ref, r.Heading), nil), nil
}

func (s *server) ruleEdit(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[ruleEditArgs](req)
	if err != nil {
		return nil, err
	}
	p, keys, err := s.ruleProject(ctx, c, a.Project, a.ID)
	if err != nil {
		return nil, err
	}
	if a.Body == nil && a.Heading == nil {
		return nil, &toolError{outcome: outcomeInvalid, msg: "Nothing to change: give a body or a heading."}
	}
	ref := parseRef(a.ID, p.Key, keys)
	cur, _, err := c.GetRule(ctx, ref)
	if err != nil {
		return nil, notFoundAs(err, ref)
	}
	in := model.EditRuleInput{Heading: cur.Heading, Body: cur.Body}
	if a.Heading != nil {
		in.Heading = strings.TrimSpace(*a.Heading)
	}
	if a.Body != nil {
		in.Body = *a.Body
	}
	r, _, err := c.EditRule(ctx, ref, in)
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("Wrote %s as a draft, now at version %d.", r.Ref, r.Version), nil), nil
}

func (s *server) ruleLink(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[ruleLinkArgs](req)
	if err != nil {
		return nil, err
	}
	p, keys, err := s.ruleProject(ctx, c, a.Project, a.ID)
	if err != nil {
		return nil, err
	}
	id, target := parseRef(a.ID, p.Key, keys), parseRef(a.Target, p.Key, keys)
	// The API answers 400 for a rel it does not know, which reads as invalid.
	in := model.RuleEdgeInput{Type: strings.TrimSpace(a.Rel), To: target}
	if a.Remove {
		if _, err = c.UnlinkRules(ctx, id, in); err != nil {
			return nil, notFoundAs(err, id)
		}
		return say(fmt.Sprintf("%s no longer %s %s.", id, in.Type, target), nil), nil
	}
	if _, err = c.LinkRules(ctx, id, in); err != nil {
		return nil, notFoundAs(err, id)
	}
	return say(fmt.Sprintf("%s now %s %s.", id, in.Type, target), nil), nil
}

func (s *server) ruleSupersede(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[ruleSupersedeArgs](req)
	if err != nil {
		return nil, err
	}
	p, keys, err := s.ruleProject(ctx, c, a.Project, "")
	if err != nil {
		return nil, err
	}
	var entries []model.SupersedeEntry
	for i, raw := range strings.Split(a.Map, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		old, rest, ok := strings.Cut(line, "->")
		if !ok || strings.TrimSpace(old) == "" {
			return nil, &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("Line %d is not \"<old> -> <new> ...\": %q.", i+1, raw)}
		}
		e := model.SupersedeEntry{Old: parseRef(old, p.Key, keys)}
		for _, n := range strings.Fields(rest) {
			e.New = append(e.New, parseRef(n, p.Key, keys))
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, &toolError{outcome: outcomeInvalid, msg: "The map has no entries."}
	}
	res, _, err := c.SupersedeRules(ctx, p.ID, model.SupersedeInput{Entries: entries})
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	cli.SupersedeRender(&b, res)
	return say(fmt.Sprintf("Applied %d entries: withdrew %d rules.", len(entries), res.Withdrawn), b.String()), nil
}
