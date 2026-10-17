// mcpprojects.go holds the MCP project tools — project_list,
// project_resolve, project_use — and the project scope every other tool
// resolves through: the explicit argument, then the session default, then
// project_required.
package api

import (
	"cmp"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func (s *server) addMCPProjectTools(srv *mcp.Server) {
	addMCPTool(s, srv, "project_list", "List projects",
		"List the Worklode projects: key, name and id. Use it to tell the user what projects exist.",
		true, mcpProjectList)
	addMCPTool(s, srv, "project_resolve", "Match a spoken project name",
		"Match a project name the user said or typed (\"work load\", \"the data platform\") against "+
			"Worklode's projects, by spelling and by sound. Answers ranked candidates with a confidence and "+
			"never picks one: confirm the right one with the user, then call project_use. Matches projects "+
			"only, never tasks or documents.",
		true, mcpProjectResolve)
	addMCPTool(s, srv, "project_use", "Set the session's project",
		"Set the Worklode project this session works in, so later calls can omit project. "+mcpProjectRule+
			" Pass confirmed=true only after the user explicitly confirmed this exact project. Without "+
			"confirmed or from_instructions it answers confirm_project and sets nothing.",
		false, mcpProjectUse)
}

// mcpProjectRule is how a session's project is chosen, said in every tool
// description that touches it: never silently, except from the user's own
// project instructions.
const mcpProjectRule = "If the user's Claude project instructions contain 'Worklode project: <name>', " +
	"call project_use with that name and from_instructions=true at the start, without asking. " +
	"Otherwise always confirm with the user first (question tool in text/GUI; read back the full " +
	"project name in voice)."

// mcpConfirmInstruction is what a confirm_project, project_required or
// project_resolve result tells Claude to do with its candidates.
const mcpConfirmInstruction = "Confirm the project with the user before calling project_use. In a text " +
	"or graphical interface, ask with your question tool (e.g. AskUserQuestion) offering these projects as " +
	"options. In a voice conversation, read back the full project name and wait for the user to say yes. " +
	"Then call project_use with confirmed=true. If the user's Claude project instructions contain " +
	"'Worklode project: <name>', call project_use with that name and from_instructions=true instead, " +
	"without asking."

func mcpProjectList(call *mcpCall, in model.MCPProjectListInput) (string, model.MCPProjectListOutput, error) {
	var out model.MCPProjectListOutput
	projects, err := call.listProjects()
	if err != nil {
		return "", out, err
	}
	page, next, err := mcpPage(projects, in.Cursor)
	if err != nil {
		return "", out, err
	}
	for _, p := range page {
		out.Projects = append(out.Projects, mcpProjectItem(p))
	}
	out.NextCursor = next
	text := fmt.Sprintf("Worklode has %s: %s.\n\n", mcpCount(len(projects), "project"), mcpProjectNames(page)) +
		mcpRender(func(w io.Writer) { cli.ProjectTable(w, page) }) + mcpMore(next, len(projects))
	return text, out, nil
}

// mcpProjectResolve ranks candidates and never picks one, even a single
// perfect match: choosing the project is the user's to confirm.
func mcpProjectResolve(call *mcpCall, in model.MCPProjectResolveInput) (string, model.MCPProjectResolveOutput, error) {
	out := model.MCPProjectResolveOutput{Instruction: mcpConfirmInstruction}
	if strings.TrimSpace(in.Spoken) == "" {
		return "", out, mcpInvalid("Pass spoken: the project name as the user said it.")
	}
	projects, err := call.listProjects()
	if err != nil {
		return "", out, err
	}
	out.Candidates, _ = matchProjects(in.Spoken, projects)
	if out.Candidates == nil {
		out.Candidates = []model.MCPProjectCandidate{}
	}
	if len(out.Candidates) == 0 {
		return fmt.Sprintf("No Worklode project sounds like %q. The projects are: %s. %s",
			in.Spoken, mcpProjectNames(projects), mcpConfirmInstruction), out, nil
	}
	var names []string
	for _, c := range out.Candidates[:min(3, len(out.Candidates))] {
		names = append(names, fmt.Sprintf("%s (%s)", c.Name, c.Key))
	}
	lead := fmt.Sprintf("%q could be %s.", in.Spoken, orList(names))
	if len(names) == 1 {
		lead = fmt.Sprintf("%q sounds like the Worklode project %s.", in.Spoken, names[0])
	}
	return lead + " " + mcpConfirmInstruction, out, nil
}

// mcpProjectUse sets the session's project, but only one the user
// confirmed: confirmed=true, a name from their project instructions, or a
// yes to an elicitation when the client can ask the user itself.
func mcpProjectUse(call *mcpCall, in model.MCPProjectUseInput) (string, model.MCPProjectUseOutput, error) {
	var out model.MCPProjectUseOutput
	if call.session == "" {
		return "", out, mcpInvalid("This connection has no MCP session to remember a project on; pass project on each call instead.")
	}
	p, ok, err := call.exactProject(in.Project)
	if err != nil {
		return "", out, err
	}
	if !ok {
		return "", out, call.confirmProject(in.Project, nil)
	}
	how := ""
	switch {
	case in.Confirmed:
	case in.FromInstructions:
		how = ", as the user's project instructions name it"
	default:
		accepted, asked := call.elicitProject(p)
		if !accepted {
			if asked {
				return "", out, &mcpToolError{outcome: "confirm_project", text: fmt.Sprintf("confirm_project: the user "+
					"did not confirm %s (%s) as this session's project. Ask which Worklode project they meant.", p.Name, p.Key)}
			}
			return "", out, call.confirmProject(in.Project, []model.Project{p})
		}
	}
	call.s.mcp.sessions.set(call.session, call.actor, p.ID)
	out.Project = mcpProjectItem(p)
	return fmt.Sprintf("This session now works in the Worklode project %s (%s)%s.", p.Name, p.Key, how), out, nil
}

// elicitProject asks the user directly, when the client supports form
// elicitation, whether p is the project to use. asked is false when the
// client cannot be asked, and the caller falls back to confirm_project.
func (call *mcpCall) elicitProject(p model.Project) (accepted, asked bool) {
	ss := call.ss
	if ss == nil {
		return false, false
	}
	ip := ss.InitializeParams()
	if ip == nil || ip.Capabilities == nil || ip.Capabilities.Elicitation == nil {
		return false, false
	}
	if e := ip.Capabilities.Elicitation; e.Form == nil && e.URL != nil {
		return false, false
	}
	res, err := ss.Elicit(call.ctx, &mcp.ElicitParams{
		Mode:            "form",
		Message:         fmt.Sprintf("Use the Worklode project %s (%s) for this conversation?", p.Name, p.Key),
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	})
	if err != nil {
		return false, false
	}
	return res.Action == "accept", true
}

// confirmProject is the confirm_project failure for arg: the candidates
// (cands, else the closest spoken matches, else every project) and how to
// confirm one with the user.
func (call *mcpCall) confirmProject(arg string, cands []model.Project) error {
	projects, err := call.listProjects()
	if err != nil {
		return err
	}
	if len(cands) == 0 {
		matches, _ := matchProjects(arg, projects)
		for _, m := range matches[:min(3, len(matches))] {
			for _, p := range projects {
				if p.ID == m.ID {
					cands = append(cands, p)
				}
			}
		}
	}
	lead := fmt.Sprintf("confirm_project: %q is not this session's confirmed Worklode project, so nothing was done.", arg)
	if strings.TrimSpace(arg) == "" {
		lead = "confirm_project: this session has no confirmed Worklode project, so nothing was done."
	}
	if len(cands) == 0 {
		cands = projects
	}
	return &mcpToolError{outcome: "confirm_project", text: lead + " Candidates: " + mcpProjectNames(cands) + ". " +
		mcpConfirmInstruction}
}

// listProjects reads the projects the caller may see, once per call.
func (call *mcpCall) listProjects() ([]model.Project, error) {
	if call.projects != nil {
		return call.projects, nil
	}
	resp, _, err := call.c.ListProjects(call.ctx)
	if err != nil {
		return nil, err
	}
	call.projects = resp.Projects
	if call.projects == nil {
		call.projects = []model.Project{}
	}
	return call.projects, nil
}

// project resolves the project a scoped tool works in: the session's
// confirmed project, which arg may name; else project_required. An arg that
// is not the session's project fails confirm_project: a project is never
// picked from an argument alone.
func (call *mcpCall) project(arg string) (model.Project, error) {
	projects, err := call.listProjects()
	if err != nil {
		return model.Project{}, err
	}
	var cur model.Project
	if id := call.s.mcp.sessions.get(call.session, call.actor); id != "" {
		for _, p := range projects {
			if p.ID == id {
				cur = p
			}
		}
	}
	if strings.TrimSpace(arg) != "" {
		if cur.ID != "" && projectNamed(cur, arg) {
			return cur, nil
		}
		p, ok, err := call.exactProject(arg)
		if err != nil {
			return model.Project{}, err
		}
		if ok {
			return model.Project{}, call.confirmProject(arg, []model.Project{p})
		}
		return model.Project{}, call.confirmProject(arg, nil)
	}
	if cur.ID != "" {
		return cur, nil
	}
	return model.Project{}, &mcpToolError{outcome: "project_required", text: "project_required: no Worklode " +
		"project is set for this session. Ask the user which Worklode project to use for this session, then " +
		"call project_use. The projects are: " + mcpProjectNames(projects) + ". " + mcpConfirmInstruction}
}

// projectNamed reports whether arg names p exactly: its id, key or full
// name, ignoring case.
func projectNamed(p model.Project, arg string) bool {
	want := strings.TrimSpace(arg)
	return strings.EqualFold(p.ID, want) || strings.EqualFold(p.Key, want) || strings.EqualFold(p.Name, want)
}

// exactProject finds the project arg names exactly. There is no spoken-name
// fallback: fuzzy matching is project_resolve's alone.
func (call *mcpCall) exactProject(arg string) (model.Project, bool, error) {
	projects, err := call.listProjects()
	if err != nil {
		return model.Project{}, false, err
	}
	for _, p := range projects {
		if projectNamed(p, arg) {
			return p, true, nil
		}
	}
	return model.Project{}, false, nil
}

// projectFor resolves the project for a tool given refs: the project the
// first full ref names by its key (WL-12, WL-SPEC-3), when arg is empty or
// names that same project; else project(arg). A full ref names one item, not
// the session's project, so it needs no confirmed project and sets none.
func (call *mcpCall) projectFor(arg string, refs ...string) (model.Project, error) {
	if p, ok := call.refProject(refs...); ok && (strings.TrimSpace(arg) == "" || projectNamed(p, arg)) {
		return p, nil
	}
	return call.project(arg)
}

// refProject is the project whose key leads the first of refs written as a
// full ref.
func (call *mcpCall) refProject(refs ...string) (model.Project, bool) {
	for _, ref := range refs {
		m := mcpRefKey.FindStringSubmatch(strings.TrimSpace(ref))
		if m == nil {
			continue
		}
		projects, err := call.listProjects()
		if err != nil {
			return model.Project{}, false
		}
		for _, p := range projects {
			if p.Key != "" && strings.EqualFold(p.Key, m[1]) {
				return p, true
			}
		}
	}
	return model.Project{}, false
}

// mcpRefKey is a full task, document or rule ref, capturing its key.
var mcpRefKey = regexp.MustCompile(`(?i)^([A-Z][A-Z0-9]{1,9})-(?:(?:SPEC|PLAN|ADR|REQ|RULE|CL|MILE|DEL)-)?\d+(?:#sec-[\w.\-]+)?$`)

// ref reads a ref argument in p through spokenRef, against every project
// key the caller can see.
func (call *mcpCall) ref(raw string, p model.Project) string {
	var keys []string
	if projects, err := call.listProjects(); err == nil {
		for _, q := range projects {
			keys = append(keys, q.Key)
		}
	}
	return spokenRef(raw, p.Key, keys)
}

func mcpProjectItem(p model.Project) model.MCPProject {
	return model.MCPProject{ID: p.ID, Key: p.Key, Name: p.Name}
}

// mcpProjectNames is "Worklode (WL), Data platform (DP)".
func mcpProjectNames(projects []model.Project) string {
	if len(projects) == 0 {
		return "none"
	}
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = fmt.Sprintf("%s (%s)", cmp.Or(p.Name, p.ID), p.Key)
	}
	return strings.Join(names, ", ")
}

// orList joins names as "a, b or c".
func orList(names []string) string {
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// spokenFiller are words a spoken project name carries that name nothing.
var spokenFiller = map[string]bool{"the": true, "project": true}

// spokenNorm lower-cases s and keeps only its letters and digits, after
// dropping filler words, so "Work Load", "work-load" and "the workload
// project" all read "workload".
func spokenNorm(s string) string {
	var b strings.Builder
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if spokenFiller[w] {
			continue
		}
		b.WriteString(w)
	}
	return b.String()
}

// phoneticCode is a small sound-alike key over a normalized word: the first
// letter kept, later vowels and h/w/y dropped, consonants that sound alike
// merged, and runs collapsed. "workload" and "worklode" both code "wrklt".
func phoneticCode(norm string) string {
	r := strings.NewReplacer("ph", "f", "ck", "k", "qu", "kw", "sh", "x", "ch", "x", "th", "0", "gh", "g")
	s := r.Replace(norm)
	var out []rune
	for i, c := range s {
		switch c {
		case 'c', 'q':
			c = 'k'
		case 'z':
			c = 's'
		case 'v':
			c = 'f'
		case 'd':
			c = 't'
		case 'b':
			c = 'p'
		}
		if i > 0 && strings.ContainsRune("aeiouyhw", c) {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == c {
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// levenshtein is the edit distance between a and b.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// similarity is 1 minus the edit distance over the longer length.
func similarity(a, b string) float64 {
	n := max(len([]rune(a)), len([]rune(b)))
	if n == 0 {
		return 0
	}
	return 1 - float64(levenshtein(a, b))/float64(n)
}

// spokenScore is how well spoken matches one name field, from 0 to 1.
func spokenScore(spoken, field string) float64 {
	a, b := spokenNorm(spoken), spokenNorm(field)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	pa, pb := phoneticCode(a), phoneticCode(b)
	score := similarity(a, b)
	if pa == pb {
		score = max(score, 0.92)
	} else {
		score = max(score, 0.9*similarity(pa, pb))
	}
	if len(a) >= 3 && strings.HasPrefix(b, a) {
		score = max(score, 0.7+0.2*float64(len(a))/float64(len(b)))
	}
	return score
}

// matchProjects ranks projects against spoken by their key, id and name,
// best first, keeping those at 0.4 or above (five at most). clear reports a
// winner: at 0.8 or above, and 0.15 ahead of the next.
func matchProjects(spoken string, projects []model.Project) (cands []model.MCPProjectCandidate, clear bool) {
	for _, p := range projects {
		score := max(spokenScore(spoken, p.Key), spokenScore(spoken, p.ID), spokenScore(spoken, p.Name))
		if score >= 0.4 {
			cands = append(cands, model.MCPProjectCandidate{MCPProject: mcpProjectItem(p), Confidence: round2(score)})
		}
	}
	slices.SortStableFunc(cands, func(a, b model.MCPProjectCandidate) int {
		return cmp.Compare(b.Confidence, a.Confidence)
	})
	if len(cands) > 5 {
		cands = cands[:5]
	}
	clear = len(cands) > 0 && cands[0].Confidence >= 0.8 &&
		(len(cands) == 1 || cands[0].Confidence-cands[1].Confidence >= 0.15)
	return cands, clear
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
