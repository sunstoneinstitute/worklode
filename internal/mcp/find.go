package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// searchFetch is how many hits search asks the API for; the tool pages
// through them ten at a time. It is the API's own cap.
const searchFetch = 100

// taskRefRe is a canonical task id, WL-12.
var taskRefRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

type searchArgs struct {
	Project string   `json:"project"`
	Query   string   `json:"query"`
	Kinds   []string `json:"kinds"`
	Cursor  string   `json:"cursor"`
}

type showArgs struct {
	Project string `json:"project"`
	Ref     string `json:"ref"`
	Inline  *bool  `json:"inline"`
	Section string `json:"section"`
}

func (s *server) addFindTools() {
	s.addTool("search", "Search the project's documents, tasks and skills by meaning and by exact words, ten results at a time.",
		`{"type":"object","properties":{"project":{"type":"string","description":"The project handle from project_use."},"query":{"type":"string","description":"What to look for."},"kinds":{"type":"array","items":{"type":"string","enum":["doc","task","skill"]},"description":"Limit to these kinds; default all three."},"cursor":{"type":"string","description":"The cursor from the previous page."}},"required":["query"]}`,
		s.search)
	s.addTool("show", "Show one Worklode item by its ref, written or spoken: a spec, plan, rule, task or project, such as WL-SPEC-77, \"spec seventy-seven\" or WL-12. A full ref needs no project handle.",
		`{"type":"object","properties":{"project":{"type":"string","description":"The project handle from project_use."},"ref":{"type":"string","description":"The item's ref, as written or spoken."},"inline":{"type":"boolean","default":true,"description":"For a document or rule: fold in-force amendments into the text."},"section":{"type":"string","description":"For a document: show only this section, by anchor (sec-3) or number (3)."}},"required":["ref"]}`,
		s.show)
}

func (s *server) search(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[searchArgs](req)
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
	resp, _, err := c.Search(ctx, model.SearchParams{Q: a.Query, Kind: a.Kinds, Project: p.ID, Limit: searchFetch})
	if err != nil {
		return nil, err
	}
	page, next, err := pageOf(resp.Hits, a.Cursor, s.cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	var docRefs map[int64]string
	for _, h := range page {
		if h.Kind == "doc" {
			docRefs = c.DocRefs(ctx, p.ID)
			break
		}
	}
	var b bytes.Buffer
	cli.SearchTable(&b, page, docRefs)
	if next != "" {
		fmt.Fprintf(&b, "\nMore results: call search again with cursor %q.\n", next)
	}
	sentence := fmt.Sprintf("Found %d results for %q.", len(resp.Hits), a.Query)
	switch len(resp.Hits) {
	case 0:
		return say(fmt.Sprintf("Nothing in %s matches %q.", p.Name, a.Query), nil), nil
	case 1:
		sentence = fmt.Sprintf("Found 1 result for %q.", a.Query)
	}
	return say(sentence, b.String()), nil
}

// show resolves ref through parseRef, with a full ref naming its own project
// (WL-REQ-1828), then shows what it names: a rule, a task, a project or a
// document.
func (s *server) show(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[showArgs](req)
	if err != nil {
		return nil, err
	}
	key, err := s.requireProject(ctx, c, a.Project, a.Ref)
	if err != nil {
		return nil, err
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(ps))
	for i, p := range ps {
		keys[i] = p.Key
	}
	ref := parseRef(a.Ref, key, keys)
	base, frag := designdoc.SplitFragment(ref)
	inline := a.Inline == nil || *a.Inline
	var b bytes.Buffer

	res, err := func() (*mcp.CallToolResult, error) {
		if _, ok := designdoc.ParseRuleRef(base); ok {
			r, _, err := c.GetRule(ctx, base)
			if err != nil {
				return nil, err
			}
			cli.RuleRender(&b, r)
			if inline {
				blocks, pending, err := designdoc.NewInliner(cli.RuleFetcher(ctx, c)).RuleAmendments(&r)
				if err != nil {
					return nil, err
				}
				cli.RuleAmendmentsRender(&b, blocks, pending)
			}
			return say(fmt.Sprintf("%s is the %s %s %s.", r.Ref, r.Status, r.Kind, r.Heading), b.String()), nil
		}
		if taskRefRe.MatchString(base) {
			t, _, err := c.GetTask(ctx, base)
			if err != nil {
				return nil, err
			}
			cli.TaskDetailRender(&b, t, s.cfg.PublicURL)
			return say(fmt.Sprintf("%s is a %s %s task: %s.", t.ID, t.State, t.Kind, t.Title), b.String()), nil
		}
		if p, ok := exactProject(base, ps); ok {
			d, _, err := c.ProjectDetail(ctx, p.ID, time.Time{}, time.Time{})
			if err != nil {
				return nil, err
			}
			cli.ProjectDetailRender(&b, d, "all time")
			return say(fmt.Sprintf("%s (%s) is a Worklode project.", p.Name, p.Key), b.String()), nil
		}
		d, err := c.ResolveDoc(ctx, base)
		if err != nil {
			return nil, err
		}
		detail, _, err := c.GetDoc(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		section := cli.NormalizeSection(a.Section)
		if section == "" {
			section = frag
		}
		text, err := cli.DocShowText(ctx, c, detail, section, inline)
		if err != nil {
			return nil, err
		}
		sentence := fmt.Sprintf("%s is the %s %s %s.", cli.DocRef(detail.Doc), d.Status, d.Kind, d.Title)
		if section != "" {
			sentence = fmt.Sprintf("Section %s of the %s %s %s.", section, d.Status, d.Kind, d.Title)
		}
		return say(sentence, text), nil
	}()
	var ce *cli.ClientError
	if errors.As(err, &ce) && ce.Status == http.StatusNotFound {
		return nil, &toolError{outcome: outcomeNotFound, msg: fmt.Sprintf("Nothing in Worklode is called %s.", ref)}
	}
	return res, err
}
