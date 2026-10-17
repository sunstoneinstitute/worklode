// mcprules.go holds the MCP rule tools: rule_list, rule_add, rule_edit,
// rule_link and rule_supersede.
package api

import (
	"cmp"
	"fmt"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// mcpRuleRels are the edge types rule_link writes. The other rule edges
// (amends, constrains, ...) stay with `lode rule link`.
var mcpRuleRels = []string{"refines", "needs", "references"}

func (s *server) addMCPRuleTools(srv *mcp.Server) {
	addMCPTool(s, srv, "rule_list", "List rules",
		"List rules, ten at a time: a spec's rules in arrangement order when doc is given, else the "+
			"project's rules by number."+mcpRefProjectHint,
		true, mcpRuleList)
	addMCPTool(s, srv, "rule_add", "Add a rule",
		"Add a draft rule with a heading and text: a requirement unless kind says catalogue, invariant, "+
			"definition or principle. doc arranges it in that spec."+mcpProjectHint,
		false, mcpRuleAdd)
	addMCPTool(s, srv, "rule_edit", "Edit a rule",
		"Write a rule's next draft version: new text, and a new heading if given. Specs arranging it show "+
			"the draft once its owner accepts it."+mcpRefProjectHint,
		false, mcpRuleEdit)
	addMCPTool(s, srv, "rule_link", "Relate two rules",
		"Record that one rule refines, needs or references another; remove takes the edge away."+mcpRefProjectHint,
		false, mcpRuleLink)
	addMCPTool(s, srv, "rule_supersede", "Supersede rules",
		"Withdraw old rules and link each to the rules that replace it, in one change. dry_run reports "+
			"what would change without writing."+mcpRefProjectHint,
		false, mcpRuleSupersede)
}

func mcpRuleList(call *mcpCall, in model.MCPRuleListInput) (string, model.MCPRuleListOutput, error) {
	var out model.MCPRuleListOutput
	p, err := call.projectFor(in.Project, in.Doc)
	if err != nil {
		return "", out, err
	}
	params := model.RuleListParams{Project: p.ID, Status: strings.TrimSpace(in.Status)}
	if in.Doc != "" {
		// A document names its own project, so the scope would only hide
		// one from another project, as in `lode rule list --doc`.
		params.Project, params.Doc = "", call.ref(in.Doc, p)
	}
	rules, _, err := call.c.ListRules(call.ctx, params)
	if err != nil {
		return "", out, err
	}
	page, next, err := mcpPage(rules, in.Cursor)
	if err != nil {
		return "", out, err
	}
	out.Rules = make([]model.MCPRuleItem, 0, len(page))
	for _, r := range page {
		out.Rules = append(out.Rules, mcpRuleItem(r))
	}
	out.NextCursor = next
	where := p.Name
	if params.Doc != "" {
		where = params.Doc
	}
	text := fmt.Sprintf("%s has %s.\n", where, mcpCount(len(rules), "rule"))
	if len(page) > 0 {
		text += "\n" + mcpRender(func(w io.Writer) { cli.RulesTable(w, page) }) + mcpMore(next, len(rules))
	}
	return text, out, nil
}

func mcpRuleAdd(call *mcpCall, in model.MCPRuleAddInput) (string, model.MCPRuleOutput, error) {
	var out model.MCPRuleOutput
	p, err := call.project(in.Project)
	if err != nil {
		return "", out, err
	}
	r, text, err := call.addRule(p, in)
	if err != nil {
		return "", out, err
	}
	out.Rule = mcpRuleItem(r)
	return text, out, nil
}

// addRule is rule_add's write, which file shares: add the rule, then
// arrange it in doc when one is named.
func (call *mcpCall) addRule(p model.Project, in model.MCPRuleAddInput) (model.Rule, string, error) {
	heading := strings.TrimSpace(in.Heading)
	if heading == "" {
		return model.Rule{}, "", mcpInvalid("A rule needs a heading.")
	}
	if strings.TrimSpace(in.Body) == "" {
		return model.Rule{}, "", mcpInvalid("A rule needs body text.")
	}
	r, _, err := call.c.AddRule(call.ctx, model.AddRuleInput{
		Project: p.ID, Heading: heading, Body: in.Body, Tags: in.Tags,
		Kind: cmp.Or(strings.ToLower(strings.TrimSpace(in.Kind)), designdoc.RuleKindRequirement),
	})
	if err != nil {
		return model.Rule{}, "", err
	}
	text := fmt.Sprintf("Added draft %s %s, %q, in %s.", r.Kind, r.Ref, r.Heading, p.Name)
	if in.Doc != "" {
		// The arranged rule replaces r only on success: a failed arrange
		// answers a zero rule, and the error must still name the one added.
		doc, _, err := call.resolveDoc(p, call.ref(in.Doc, p))
		if err == nil {
			var arranged model.Rule
			if arranged, _, err = call.c.ArrangeRule(call.ctx, doc.ID, model.ArrangeRuleInput{Rule: r.Ref}); err == nil {
				r = arranged
			}
		}
		if err != nil {
			_, msg := mcpErrorText(err)
			return model.Rule{}, "", &mcpToolError{outcome: "error",
				text: fmt.Sprintf("Added %s, %q, but could not arrange it in %s: %s", r.Ref, r.Heading, in.Doc, msg)}
		}
		text += fmt.Sprintf(" It is arranged in %s.", cli.DocRef(doc))
	}
	return r, text, nil
}

func mcpRuleEdit(call *mcpCall, in model.MCPRuleEditInput) (string, model.MCPRuleOutput, error) {
	var out model.MCPRuleOutput
	if strings.TrimSpace(in.Body) == "" {
		return "", out, mcpInvalid("Pass body: the rule's new text.")
	}
	p, err := call.projectFor(in.Project, in.Ref)
	if err != nil {
		return "", out, err
	}
	ref := call.ref(in.Ref, p)
	heading := strings.TrimSpace(in.Heading)
	if heading == "" {
		cur, _, err := call.c.GetRule(call.ctx, ref)
		if err != nil {
			return "", out, err
		}
		heading = cur.Heading
	}
	r, _, err := call.c.EditRule(call.ctx, ref, model.EditRuleInput{Heading: heading, Body: in.Body})
	if err != nil {
		return "", out, err
	}
	out.Rule = mcpRuleItem(r)
	return fmt.Sprintf("Wrote version %d of %s, %q, as a %s.", r.Version, r.Ref, r.Heading, r.Status), out, nil
}

func mcpRuleLink(call *mcpCall, in model.MCPRuleLinkInput) (string, model.MCPRuleLinkOutput, error) {
	var out model.MCPRuleLinkOutput
	rel := strings.ToLower(strings.TrimSpace(in.Rel))
	valid := false
	for _, r := range mcpRuleRels {
		valid = valid || r == rel
	}
	if !valid {
		return "", out, mcpInvalid("rel must be one of %s.", orList(mcpRuleRels))
	}
	p, err := call.projectFor(in.Project, in.Ref, in.Target)
	if err != nil {
		return "", out, err
	}
	from, to := call.ref(in.Ref, p), call.ref(in.Target, p)
	edge := model.RuleEdgeInput{Type: rel, To: to}
	if in.Remove {
		_, err = call.c.UnlinkRules(call.ctx, from, edge)
	} else {
		_, err = call.c.LinkRules(call.ctx, from, edge)
	}
	if err != nil {
		return "", out, err
	}
	out = model.MCPRuleLinkOutput{Ref: from, Rel: rel, Target: to, Removed: in.Remove}
	if in.Remove {
		return fmt.Sprintf("%s no longer %s %s.", from, rel, to), out, nil
	}
	return fmt.Sprintf("%s now %s %s.", from, rel, to), out, nil
}

func mcpRuleSupersede(call *mcpCall, in model.MCPRuleSupersedeInput) (string, model.MCPSupersedeOutput, error) {
	var out model.MCPSupersedeOutput
	if len(in.Entries) == 0 {
		return "", out, mcpInvalid("Pass entries: each old rule and the rules that replace it.")
	}
	var olds []string
	for _, e := range in.Entries {
		olds = append(olds, e.Old)
	}
	p, err := call.projectFor(in.Project, olds...)
	if err != nil {
		return "", out, err
	}
	entries := make([]model.SupersedeEntry, len(in.Entries))
	for i, e := range in.Entries {
		entries[i].Old = call.ref(e.Old, p)
		for _, n := range e.New {
			entries[i].New = append(entries[i].New, call.ref(n, p))
		}
	}
	res, _, err := call.c.SupersedeRules(call.ctx, p.ID, model.SupersedeInput{Entries: entries, DryRun: in.DryRun})
	if err != nil {
		return "", out, err
	}
	out = model.MCPSupersedeOutput{Withdrawn: res.Withdrawn, Edges: res.Edges, Tasks: res.Tasks, StalePlans: res.StalePlans, DryRun: res.DryRun}
	lead := "Withdrew"
	if res.DryRun {
		lead = "Dry run: this would withdraw"
	}
	return fmt.Sprintf("%s %s and linked %s to successors.\n\n", lead, mcpCount(res.Withdrawn, "rule"), mcpCount(res.Edges, "edge")) +
		mcpRender(func(w io.Writer) { cli.SupersedeRender(w, res) }), out, nil
}

func mcpRuleItem(r model.Rule) model.MCPRuleItem {
	return model.MCPRuleItem{Ref: r.Ref, Kind: r.Kind, Status: r.Status, Version: r.Version, Heading: r.Heading}
}
