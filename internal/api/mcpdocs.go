// mcpdocs.go holds the MCP document tools — doc_list, doc_add, doc_edit,
// doc_transition — and the two cross-entity readers, search and show.
package api

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func (s *server) addMCPDocTools(srv *mcp.Server) {
	addMCPTool(s, srv, "search", "Search Worklode",
		"Search documents, tasks and skills by meaning and by words, best first, ten at a time. Use it "+
			"when you know what you are looking for but not its ref."+mcpProjectHint,
		true, mcpSearch)
	addMCPTool(s, srv, "show", "Show one item",
		"Show one task, spec, plan, rule or project by its ref: WL-12, WL-SPEC-77, WL-PLAN-3, "+
			"WL-REQ-1300, a project key, or a spoken form like \"spec seventy-seven\" or \"task five twelve\". "+
			"A document or rule is shown with its in-force amendments folded in unless inline is false; "+
			"section narrows a document to one section."+mcpRefProjectHint,
		true, mcpShow)
	addMCPTool(s, srv, "doc_list", "List documents",
		"List specs and plans, ten at a time; withdrawn, superseded and spent ones are hidden unless "+
			"status is given."+mcpProjectHint,
		true, mcpDocList)
	addMCPTool(s, srv, "doc_add", "Add a document",
		"Create a draft spec or plan with a title and markdown body. A plan names what it covers."+mcpProjectHint,
		false, mcpDocAdd)
	addMCPTool(s, srv, "doc_edit", "Edit a document",
		"Replace a document's body or set its title. A draft or a plan is rewritten in place; an accepted "+
			"spec is amended, which needs a note (or substantive). Pass if_version, the version show "+
			"reported, so a concurrent edit is refused instead of overwritten."+mcpRefProjectHint,
		false, mcpDocEdit)
	addMCPTool(s, srv, "doc_transition", "Move a document",
		"Move a document through its lifecycle: submit it for review, accept it, revise an accepted spec "+
			"(opens a candidate revision), or withdraw it with a reason."+mcpRefProjectHint,
		false, mcpDocTransition)
}

func mcpSearch(call *mcpCall, in model.MCPSearchInput) (string, model.MCPSearchOutput, error) {
	var out model.MCPSearchOutput
	if strings.TrimSpace(in.Query) == "" {
		return "", out, mcpInvalid("Pass query: what to look for.")
	}
	p, err := call.project(in.Project)
	if err != nil {
		return "", out, err
	}
	resp, _, err := call.c.Search(call.ctx, model.SearchParams{Q: in.Query, Project: p.ID, Kind: in.Kinds, Limit: 50})
	if err != nil {
		return "", out, err
	}
	page, next, err := mcpPage(resp.Hits, in.Cursor)
	if err != nil {
		return "", out, err
	}
	refs := call.c.DocRefs(call.ctx, p.ID)
	out.Hits = make([]model.MCPSearchHit, 0, len(page))
	for _, h := range page {
		ref := h.Title
		switch h.Kind {
		case "task":
			ref = h.TaskID
		case "doc":
			ref = cmp.Or(refs[h.DocID], fmt.Sprintf("doc:%d", h.DocID))
		}
		out.Hits = append(out.Hits, model.MCPSearchHit{Kind: h.Kind, Ref: ref, Title: h.Title, Anchor: h.Anchor, Excerpt: h.Excerpt})
	}
	out.NextCursor = next
	text := fmt.Sprintf("Found %s for %q in %s.", mcpCount(len(resp.Hits), "result"), in.Query, p.Name)
	if len(out.Hits) > 0 {
		text += fmt.Sprintf(" The best is %s, %q.", out.Hits[0].Ref, out.Hits[0].Title)
		text += "\n\n" + mcpRender(func(w io.Writer) { cli.SearchTable(w, page, refs) }) + mcpMore(next, len(resp.Hits))
	}
	return text, out, nil
}

// mcpTaskID is a full task id, after spokenRef.
var mcpTaskID = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}-[0-9]+$`)

func mcpShow(call *mcpCall, in model.MCPShowInput) (string, model.MCPShowOutput, error) {
	var out model.MCPShowOutput
	inline := in.Inline == nil || *in.Inline
	if strings.TrimSpace(in.Ref) == "" {
		return "", out, mcpInvalid("Pass ref: what to show.")
	}
	// A project key or id names a project wherever the session is.
	if projects, err := call.listProjects(); err == nil {
		for _, p := range projects {
			if strings.EqualFold(in.Ref, p.Key) || strings.EqualFold(in.Ref, p.ID) {
				out = model.MCPShowOutput{Kind: "project", Ref: p.Key, Title: p.Name}
				return fmt.Sprintf("Here is the Worklode project %s (%s).\n\n", p.Name, p.Key) +
					mcpRender(func(w io.Writer) { cli.ProjectTable(w, []model.Project{p}) }), out, nil
			}
		}
	}
	p, err := call.projectFor(in.Project, in.Ref)
	if err != nil {
		return "", out, err
	}
	ref := call.ref(in.Ref, p)
	if _, ok := designdoc.ParseRuleRef(ref); ok {
		if in.Section != "" {
			return "", out, mcpInvalid("section applies to documents, and %s is a rule.", ref)
		}
		rule, _, err := call.c.GetRule(call.ctx, ref)
		if err != nil {
			return "", out, err
		}
		out = model.MCPShowOutput{Kind: "rule", Ref: rule.Ref, Title: rule.Heading}
		text := fmt.Sprintf("Here is %s %s, %q (%s, version %d).\n\n", rule.Kind, rule.Ref, rule.Heading, rule.Status, rule.Version) +
			mcpRender(func(w io.Writer) { cli.RuleRender(w, rule) })
		if inline {
			blocks, pending, err := designdoc.NewInliner(call.c.RuleFetcher(call.ctx)).RuleAmendments(&rule)
			if err != nil {
				return "", out, err
			}
			text += mcpRender(func(w io.Writer) { cli.RuleAmendmentsRender(w, blocks, pending) })
		}
		return text, out, nil
	}
	if mcpTaskID.MatchString(ref) {
		if in.Section != "" {
			return "", out, mcpInvalid("section applies to documents, and %s is a task.", ref)
		}
		t, _, err := call.c.GetTask(call.ctx, ref)
		if err != nil {
			return "", out, err
		}
		out = model.MCPShowOutput{Kind: "task", Ref: t.ID, Title: t.Title}
		return fmt.Sprintf("Here is %s task %s, %q, %s.\n\n", t.Kind, t.ID, t.Title, strings.ReplaceAll(t.State, "_", " ")) +
			mcpRender(func(w io.Writer) { cli.TaskDetailRender(w, t, call.s.cfg.PublicURL) }), out, nil
	}
	doc, refSection, err := call.resolveDoc(p, ref)
	if err != nil {
		return "", out, err
	}
	section := cli.NormalizeSection(in.Section)
	if section != "" && refSection != "" && section != refSection {
		return "", out, mcpInvalid("The ref names #%s but section is %s; pass one.", refSection, section)
	}
	section = cmp.Or(section, refSection)
	detail, _, err := call.c.GetDoc(call.ctx, doc.ID)
	if err != nil {
		return "", out, err
	}
	if detail.Ref == "" {
		detail.Ref = cli.DocRef(doc)
	}
	text, err := call.c.DocShowText(call.ctx, &detail, section, inline)
	if err != nil {
		return "", out, showTextErr(err)
	}
	out = model.MCPShowOutput{Kind: detail.Kind, Ref: cli.DocRef(detail.Doc), Title: detail.Title}
	where := ""
	if section != "" {
		where = " section " + strings.TrimPrefix(section, "sec-") + " of"
	}
	return fmt.Sprintf("Here is%s %s %s, %q (%s, version %d).\n\n", where, detail.Kind, out.Ref, detail.Title,
		detail.Status, detail.Version) + text, out, nil
}

// showTextErr classifies a DocShowText failure. A rule the inliner fetched
// can be refused or missing; that is the API's answer, classified as any
// other (refused, not_found). The rest is the document or the section asked
// for, which the arguments can fix.
func showTextErr(err error) error {
	var ce *cli.ClientError
	if errors.As(err, &ce) {
		return err
	}
	return mcpInvalid("%s", err.Error())
}

// resolveDoc finds the document ref names in p: the ref grammar against p's
// documents first, then the API's own resolver, which reads an id, any
// project's ref or an exact slug. It also returns the ref's #section.
func (call *mcpCall) resolveDoc(p model.Project, ref string) (model.Doc, string, error) {
	list, _, err := call.c.ListDocs(call.ctx, model.DocListParams{Project: p.ID})
	if err != nil {
		return model.Doc{}, "", err
	}
	doc, section, err := designdoc.ResolveRef(list.Docs, p.Key, ref)
	if err == nil {
		return doc, section, nil
	}
	var notFound *designdoc.NotFoundError
	var unresolved *designdoc.UnresolvedError
	if !errors.As(err, &notFound) && !errors.As(err, &unresolved) {
		return model.Doc{}, "", mcpInvalid("%s", err.Error())
	}
	base, section := designdoc.SplitFragment(ref)
	d, rerr := call.c.ResolveDoc(call.ctx, base)
	if rerr != nil {
		return model.Doc{}, "", rerr
	}
	return d, section, nil
}

func mcpDocList(call *mcpCall, in model.MCPDocListInput) (string, model.MCPDocListOutput, error) {
	var out model.MCPDocListOutput
	p, err := call.project(in.Project)
	if err != nil {
		return "", out, err
	}
	kind, err := aliasKind(in.Kind, "doc")
	if err != nil {
		return "", out, err
	}
	status := strings.TrimSpace(in.Status)
	resp, _, err := call.c.ListDocs(call.ctx, model.DocListParams{
		Project: p.ID, Kind: kind, Status: status, HideTerminal: status == "",
	})
	if err != nil {
		return "", out, err
	}
	page, next, err := mcpPage(resp.Docs, in.Cursor)
	if err != nil {
		return "", out, err
	}
	out.Docs = make([]model.MCPDocItem, 0, len(page))
	for _, d := range page {
		out.Docs = append(out.Docs, mcpDocItem(d))
	}
	out.NextCursor = next
	what := cmp.Or(kind, "document")
	if status != "" && status != "all" {
		what = status + " " + what
	}
	text := fmt.Sprintf("%s has %s.\n", p.Name, mcpCount(len(resp.Docs), what))
	if len(page) > 0 {
		text += "\n" + mcpRender(func(w io.Writer) { cli.DocTable(w, page) }) + mcpMore(next, len(resp.Docs))
	}
	return text, out, nil
}

func mcpDocAdd(call *mcpCall, in model.MCPDocAddInput) (string, model.MCPDocOutput, error) {
	var out model.MCPDocOutput
	p, err := call.project(in.Project)
	if err != nil {
		return "", out, err
	}
	d, text, err := call.addDoc(p, in)
	if err != nil {
		return "", out, err
	}
	out.Doc = mcpDocItem(d)
	return text, out, nil
}

// addDoc is doc_add's write, which file shares. The API takes the whole
// markdown artifact, so the title becomes its H1 and covers its frontmatter.
func (call *mcpCall) addDoc(p model.Project, in model.MCPDocAddInput) (model.Doc, string, error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return model.Doc{}, "", mcpInvalid("A document needs a title.")
	}
	if len(in.Covers) > 0 && kind != "plan" {
		return model.Doc{}, "", mcpInvalid("covers applies to plans only.")
	}
	if strings.HasPrefix(strings.TrimSpace(in.Body), "---") {
		return model.Doc{}, "", mcpInvalid("Pass the body without frontmatter; name what a plan covers with covers.")
	}
	var b strings.Builder
	if len(in.Covers) > 0 {
		b.WriteString("---\ncovers:\n")
		for _, c := range in.Covers {
			fmt.Fprintf(&b, "  - %s\n", call.ref(c, p))
		}
		b.WriteString("---\n")
	}
	fmt.Fprintf(&b, "# %s\n", title)
	if body := strings.TrimSpace(in.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	d, _, err := call.c.CreateDoc(call.ctx, model.CreateDocInput{
		Project: p.ID, Kind: kind, Slug: mcpSlug(title), Body: b.String(),
	})
	if err != nil {
		return model.Doc{}, "", err
	}
	text := fmt.Sprintf("Created %s %s %s, %q, in %s.", d.Status, d.Kind, cli.DocRef(d), d.Title, p.Name)
	for _, w := range d.Warnings {
		text += "\nWarning: " + w
	}
	return d, text, nil
}

// mcpSlug is a document slug from its title: lower-case words joined by
// hyphens, at most 60 characters.
func mcpSlug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	return cmp.Or(s, "untitled")
}

func mcpDocEdit(call *mcpCall, in model.MCPDocEditInput) (string, model.MCPDocOutput, error) {
	var out model.MCPDocOutput
	if in.Body == "" && in.Title == "" {
		return "", out, mcpInvalid("Nothing to change: pass body or title.")
	}
	if strings.HasPrefix(strings.TrimSpace(in.Body), "---") {
		return "", out, mcpInvalid("Pass the body without frontmatter; set the title with title.")
	}
	p, err := call.projectFor(in.Project, in.ID)
	if err != nil {
		return "", out, err
	}
	doc, _, err := call.resolveDoc(p, call.ref(in.ID, p))
	if err != nil {
		return "", out, err
	}
	d := doc
	text := ""
	if in.Body != "" {
		detail, _, err := call.c.GetDoc(call.ctx, doc.ID)
		if err != nil {
			return "", out, err
		}
		// The kind and status decide the endpoint, as `lode doc edit` does:
		// a draft or a plan carries no accepted text to amend.
		if detail.Kind == "plan" || detail.Status != "accepted" {
			d, _, err = call.c.UpdateDocBody(call.ctx, doc.ID, model.UpdateDocBodyInput{Body: in.Body, IfVersion: in.IfVersion})
			if err != nil {
				return "", out, err
			}
			text = fmt.Sprintf("Rewrote %s, %q; it is now version %d.", cli.DocRef(d), d.Title, d.Version)
		} else {
			res, _, err := call.c.PatchDoc(call.ctx, doc.ID, model.PatchDocInput{
				Body: in.Body, Substantive: in.Substantive, Note: in.Note, IfVersion: in.IfVersion,
			})
			if err != nil {
				return "", out, err
			}
			d = res.Doc
			text = fmt.Sprintf("Amended accepted %s %s, %q; it is now version %d.\n\n", d.Kind, cli.DocRef(d), d.Title, d.Version) +
				mcpRender(func(w io.Writer) { cli.DocPatchRender(w, res.Doc, res.Patch, detail.Reviewers) })
		}
		for _, w := range d.Warnings {
			text += "\nWarning: " + w
		}
	}
	if in.Title != "" {
		title := in.Title
		dd, _, err := call.c.SetDocColumns(call.ctx, doc.ID, model.DocColumnsInput{Title: &title})
		if err != nil {
			return "", out, err
		}
		d = dd.Doc
		if text == "" {
			text = fmt.Sprintf("Retitled %s to %q.", cli.DocRef(d), d.Title)
		} else {
			text += fmt.Sprintf("\nIts title is now %q.", d.Title)
		}
	}
	out.Doc = mcpDocItem(d)
	return text, out, nil
}

func mcpDocTransition(call *mcpCall, in model.MCPDocTransitionInput) (string, model.MCPDocOutput, error) {
	var out model.MCPDocOutput
	p, err := call.projectFor(in.Project, in.ID)
	if err != nil {
		return "", out, err
	}
	doc, _, err := call.resolveDoc(p, call.ref(in.ID, p))
	if err != nil {
		return "", out, err
	}
	ref := cli.DocRef(doc)
	var d model.Doc
	var text string
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "submit":
		d, _, err = call.c.SubmitDoc(call.ctx, doc.ID)
		text = fmt.Sprintf("Submitted %s, %q, for review.", ref, doc.Title)
	case "accept":
		var resp model.AcceptDocResponse
		resp, _, err = call.c.AcceptDoc(call.ctx, doc.ID)
		d = resp.Doc
		text = fmt.Sprintf("Accepted %s, %q.", ref, doc.Title)
		if len(resp.Tasks) > 0 {
			text += fmt.Sprintf(" It minted %s.\n\n", mcpCount(len(resp.Tasks), "task")) +
				mcpRender(func(w io.Writer) { cli.TaskTable(w, resp.Tasks) })
		}
	case "revise":
		if _, _, err = call.c.ReviseDoc(call.ctx, doc.ID); err == nil {
			var detail model.DocDetail
			detail, _, err = call.c.GetDoc(call.ctx, doc.ID)
			d = detail.Doc
		}
		text = fmt.Sprintf("Opened a candidate revision of %s, %q.", ref, doc.Title)
	case "withdraw":
		d, _, err = call.c.WithdrawDoc(call.ctx, doc.ID, in.Reason)
		text = fmt.Sprintf("Withdrew %s, %q.", ref, doc.Title)
	default:
		return "", out, mcpInvalid("%q is not a document action. Use submit, accept, revise or withdraw.", in.Action)
	}
	if err != nil {
		return "", out, err
	}
	out.Doc = mcpDocItem(d)
	return text, out, nil
}

func mcpDocItem(d model.Doc) model.MCPDocItem {
	return model.MCPDocItem{ID: d.ID, Ref: cli.DocRef(d), Kind: d.Kind, Title: d.Title, Status: d.Status, Version: d.Version}
}
