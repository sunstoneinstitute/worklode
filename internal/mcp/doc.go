package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

type docListArgs struct {
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Cursor  string `json:"cursor"`
}

type docAddArgs struct {
	Project string   `json:"project"`
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Covers  []string `json:"covers"`
}

type docEditArgs struct {
	Project   string `json:"project"`
	ID        string `json:"id"`
	Body      string `json:"body"`
	Patch     string `json:"patch"`
	Note      string `json:"note"`
	IfVersion int    `json:"if_version"`
}

type docTransitionArgs struct {
	Project    string `json:"project"`
	ID         string `json:"id"`
	Transition string `json:"transition"`
	// Reason is the justification a withdrawal records.
	Reason string `json:"reason"`
}

func (s *server) addDocTools() {
	s.addTool("doc_list", "List the project's specs and plans, ten at a time.",
		`{"type":"object","properties":{`+projectProp+`,"kind":{"type":"string","description":"spec, plan or an alias word."},"status":{"type":"string","description":"draft, accepted or superseded."},"cursor":{"type":"string","description":"The cursor from the previous page."}}}`,
		s.docList)
	s.addTool("doc_add", "Create a draft spec or plan. The title becomes the heading and the body follows it. covers lists the rule refs a plan covers.",
		`{"type":"object","properties":{`+projectProp+`,"kind":{"type":"string","description":"spec, plan or an alias word."},"title":{"type":"string"},"body":{"type":"string","description":"The markdown after the title."},"covers":{"type":"array","items":{"type":"string"},"description":"Rule refs a plan covers."}},"required":["kind","title","body"]}`,
		s.docAdd)
	s.addTool("doc_edit", "Replace a document's body, or amend an accepted one in place with a patch. if_version is the version you read; a newer one answers conflict.",
		`{"type":"object","properties":{`+projectProp+`,"id":{"type":"string","description":"The document's ref, as written or spoken."},"body":{"type":"string","description":"The whole new markdown, frontmatter included."},"patch":{"type":"string","description":"The whole amended markdown of an accepted document."},"note":{"type":"string","description":"For a patch: what changed and why."},"if_version":{"type":"integer","description":"The version this edit is based on."}},"required":["id","if_version"]}`,
		s.docEdit)
	s.addTool("doc_transition", "Move a document through its lifecycle: submit sends it to review, accept accepts it, revise opens a revision of an accepted one, withdraw closes it with a reason.",
		`{"type":"object","properties":{`+projectProp+`,"id":{"type":"string","description":"The document's ref, as written or spoken."},"transition":{"type":"string","enum":["submit","accept","revise","withdraw"]},"reason":{"type":"string","description":"Why, for withdraw."}},"required":["id","transition"]}`,
		s.docTransition)
}

// docKind resolves a spoken word to a document kind.
func docKind(word string) (string, error) {
	if _, ok := KindAliases[normalizeKindWord(word)]; !ok {
		word = strings.TrimSuffix(normalizeKindWord(word), "s") // "plans" files as "plan"
	}
	kind, ambiguous, closest := resolveKind(word)
	switch {
	case ambiguous:
		return "", &toolError{outcome: outcomeInvalid, msg: `"design" could be a spec or a design task. Say "design task" or "spec".`}
	case kind == "":
		return "", &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("%q is not a document kind. Did you mean %s?", word, strings.Join(closest, ", "))}
	case KindAliases[normalizeKindWord(word)].Entity != "doc":
		return "", &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("%q is not a document kind. Documents are specs and plans.", word)}
	}
	return kind, nil
}

func (s *server) docList(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[docListArgs](req)
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
	params := model.DocListParams{Project: p.ID, Status: a.Status}
	if a.Kind != "" {
		if params.Kind, err = docKind(a.Kind); err != nil {
			return nil, err
		}
	}
	resp, _, err := c.ListDocs(ctx, params)
	if err != nil {
		return nil, err
	}
	if len(resp.Docs) == 0 {
		return say(fmt.Sprintf("No documents in %s match.", p.Name), nil), nil
	}
	page, next, err := pageOf(resp.Docs, a.Cursor, s.cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	cli.DocTable(&b, page)
	if next != "" {
		fmt.Fprintf(&b, "\nMore documents: call doc_list again with cursor %q.\n", next)
	}
	return say(fmt.Sprintf("Found %d documents in %s.", len(resp.Docs), p.Name), b.String()), nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugOf(title string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
}

func (s *server) docAdd(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[docAddArgs](req)
	if err != nil {
		return nil, err
	}
	key, err := s.requireProject(ctx, c, a.Project, "")
	if err != nil {
		return nil, err
	}
	kind, err := docKind(a.Kind)
	if err != nil {
		return nil, err
	}
	slug := slugOf(a.Title)
	if slug == "" {
		return nil, &toolError{outcome: outcomeInvalid, msg: "The document needs a title."}
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return nil, err
	}
	p, _ := exactProject(key, ps)
	keys := projectKeys(ps)
	var b strings.Builder
	b.WriteString("---\nstatus: draft\n")
	if len(a.Covers) > 0 {
		b.WriteString("covers:\n")
		for _, r := range a.Covers {
			fmt.Fprintf(&b, "  - %s\n", parseRef(r, key, keys))
		}
	}
	fmt.Fprintf(&b, "---\n\n# %s\n\n%s\n", a.Title, strings.TrimSpace(a.Body))
	d, _, err := c.CreateDoc(ctx, model.CreateDocInput{Project: p.ID, Kind: kind, Slug: slug, Body: b.String()})
	if err != nil {
		return nil, err
	}
	return say(fmt.Sprintf("Created the draft %s %s: %s, at version %d.", d.Kind, cli.DocRef(d), d.Title, d.Version), nil), nil
}

// docID confirms the project and turns a spoken ref into a document id; a
// full ref names its own project (WL-REQ-1828).
func (s *server) docID(ctx context.Context, c *cli.Client, project, ref string) (model.Doc, error) {
	key, err := s.requireProject(ctx, c, project, ref)
	if err != nil {
		return model.Doc{}, err
	}
	ps, err := listProjects(ctx, c)
	if err != nil {
		return model.Doc{}, err
	}
	ref = parseRef(ref, key, projectKeys(ps))
	d, err := c.ResolveDoc(ctx, ref)
	if d.ProjectKey == "" {
		d.ProjectKey = key
	}
	var ce *cli.ClientError
	if errors.As(err, &ce) && ce.Status == http.StatusNotFound {
		return d, &toolError{outcome: outcomeNotFound, msg: fmt.Sprintf("Nothing in Worklode is called %s.", ref)}
	}
	return d, err
}

// withCurrentVersion adds the stored version to a 409 so the caller can
// re-read and retry (D5).
func withCurrentVersion(ctx context.Context, c *cli.Client, d model.Doc, err error) error {
	var ce *cli.ClientError
	if errors.As(err, &ce) && ce.Status == http.StatusConflict {
		if cur, _, gerr := c.GetDoc(ctx, d.ID); gerr == nil {
			return &toolError{outcome: outcomeConflict, msg: fmt.Sprintf("%s The current version is %d.", ce.Msg, cur.Version)}
		}
	}
	return err
}

func (s *server) docEdit(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[docEditArgs](req)
	if err != nil {
		return nil, err
	}
	if (a.Body == "") == (a.Patch == "") {
		return nil, &toolError{outcome: outcomeInvalid, msg: "Give either body or patch."}
	}
	if a.IfVersion <= 0 {
		return nil, &toolError{outcome: outcomeInvalid, msg: "if_version is the version you based the edit on. Show the document to read it."}
	}
	d, err := s.docID(ctx, c, a.Project, a.ID)
	if err != nil {
		return nil, err
	}
	if a.Patch != "" {
		r, _, err := c.PatchDoc(ctx, d.ID, model.PatchDocInput{Body: a.Patch, Note: a.Note, IfVersion: a.IfVersion})
		if err != nil {
			return nil, withCurrentVersion(ctx, c, d, err)
		}
		return say(fmt.Sprintf("Amended %s in place, now at version %d.", cli.DocRef(d), r.Doc.Version), nil), nil
	}
	u, _, err := c.UpdateDocBody(ctx, d.ID, model.UpdateDocBodyInput{Body: a.Body, IfVersion: a.IfVersion})
	if err != nil {
		return nil, withCurrentVersion(ctx, c, d, err)
	}
	return say(fmt.Sprintf("Updated %s, now at version %d.", cli.DocRef(u), u.Version), nil), nil
}

var docTransitions = []string{"submit", "accept", "revise", "withdraw"}

func (s *server) docTransition(ctx context.Context, c *cli.Client, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a, err := argsOf[docTransitionArgs](req)
	if err != nil {
		return nil, err
	}
	tr := strings.ToLower(strings.TrimSpace(a.Transition))
	if !slices.Contains(docTransitions, tr) {
		return nil, &toolError{outcome: outcomeInvalid, msg: fmt.Sprintf("%q is not a transition. Use %s.", a.Transition, strings.Join(docTransitions, ", "))}
	}
	d, err := s.docID(ctx, c, a.Project, a.ID)
	if err != nil {
		return nil, err
	}
	ref := cli.DocRef(d)
	switch tr {
	case "submit":
		if _, _, err = c.SubmitDoc(ctx, d.ID); err == nil {
			return say(fmt.Sprintf("Submitted %s for review.", ref), nil), nil
		}
	case "accept":
		var r model.AcceptDocResponse
		if r, _, err = c.AcceptDoc(ctx, d.ID); err == nil {
			return say(fmt.Sprintf("Accepted %s, which minted %d tasks.", ref, len(r.Tasks)), nil), nil
		}
	case "revise":
		if _, _, err = c.ReviseDoc(ctx, d.ID); err == nil {
			return say(fmt.Sprintf("Opened a revision of %s.", ref), nil), nil
		}
	case "withdraw":
		if _, _, err = c.WithdrawDoc(ctx, d.ID, a.Reason); err == nil {
			return say(fmt.Sprintf("Withdrew %s.", ref), nil), nil
		}
	}
	return nil, err
}
