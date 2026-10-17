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
			"Worklode's projects, by spelling and by sound. Answers ranked candidates with a confidence. "+
			"When clear is true, use the first candidate; otherwise read the top two or three back to the "+
			"user and ask which they meant. Matches projects only, never tasks or documents.",
		true, mcpProjectResolve)
	addMCPTool(s, srv, "project_use", "Set the session's project",
		"Set the Worklode project this session works in, so later calls can omit project. Call it once "+
			"the user has said which project, or with the key from a 'Worklode project: <key>' line in "+
			"their project instructions.",
		false, mcpProjectUse)
}

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

func mcpProjectResolve(call *mcpCall, in model.MCPProjectResolveInput) (string, model.MCPProjectResolveOutput, error) {
	var out model.MCPProjectResolveOutput
	if strings.TrimSpace(in.Spoken) == "" {
		return "", out, mcpInvalid("Pass spoken: the project name as the user said it.")
	}
	projects, err := call.listProjects()
	if err != nil {
		return "", out, err
	}
	out.Candidates, out.Clear = matchProjects(in.Spoken, projects)
	if out.Candidates == nil {
		out.Candidates = []model.MCPProjectCandidate{}
	}
	switch {
	case len(out.Candidates) == 0:
		return fmt.Sprintf("No Worklode project sounds like %q. The projects are: %s.",
			in.Spoken, mcpProjectNames(projects)), out, nil
	case out.Clear:
		c := out.Candidates[0]
		return fmt.Sprintf("%q is the Worklode project %s (%s).", in.Spoken, c.Name, c.Key), out, nil
	default:
		var names []string
		for _, c := range out.Candidates[:min(3, len(out.Candidates))] {
			names = append(names, fmt.Sprintf("%s (%s)", c.Name, c.Key))
		}
		return fmt.Sprintf("%q could be %s. Ask the user which one they meant.",
			in.Spoken, orList(names)), out, nil
	}
}

func mcpProjectUse(call *mcpCall, in model.MCPProjectUseInput) (string, model.MCPProjectUseOutput, error) {
	var out model.MCPProjectUseOutput
	if call.session == "" {
		return "", out, mcpInvalid("This connection has no MCP session to remember a project on; pass project on each call instead.")
	}
	p, err := call.lookupProject(in.Project)
	if err != nil {
		return "", out, err
	}
	call.s.mcp.sessions.set(call.session, call.actor, p.ID)
	out.Project = mcpProjectItem(p)
	return fmt.Sprintf("This session now works in the Worklode project %s (%s).", p.Name, p.Key), out, nil
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

// project resolves the project a scoped tool works in: arg, else the
// session's default, else a project_required failure listing the choices.
func (call *mcpCall) project(arg string) (model.Project, error) {
	if strings.TrimSpace(arg) != "" {
		return call.lookupProject(arg)
	}
	if id := call.s.mcp.sessions.get(call.session, call.actor); id != "" {
		projects, err := call.listProjects()
		if err != nil {
			return model.Project{}, err
		}
		for _, p := range projects {
			if p.ID == id {
				return p, nil
			}
		}
	}
	projects, err := call.listProjects()
	if err != nil {
		return model.Project{}, err
	}
	return model.Project{}, &mcpToolError{outcome: "project_required", text: "project_required: no Worklode " +
		"project is set for this session. Ask the user which Worklode project to use for this session, then " +
		"call project_use. The projects are: " + mcpProjectNames(projects) + "."}
}

// lookupProject finds the project arg names exactly: its id, key or name,
// ignoring case. There is no spoken-name fallback here: a near miss fails
// not_found with the closest candidates, so Claude asks rather than writes
// into the wrong project. Fuzzy matching is project_resolve's alone.
func (call *mcpCall) lookupProject(arg string) (model.Project, error) {
	projects, err := call.listProjects()
	if err != nil {
		return model.Project{}, err
	}
	want := strings.TrimSpace(arg)
	for _, p := range projects {
		if strings.EqualFold(p.ID, want) || strings.EqualFold(p.Key, want) || strings.EqualFold(p.Name, want) {
			return p, nil
		}
	}
	cands, _ := matchProjects(arg, projects)
	msg := fmt.Sprintf("No Worklode project is called %q.", arg)
	if len(cands) > 0 {
		var names []string
		for _, c := range cands[:min(3, len(cands))] {
			names = append(names, fmt.Sprintf("%s (%s)", c.Name, c.Key))
		}
		msg += " It could be " + orList(names) + "; ask the user which one they meant, then pass its key."
	} else {
		msg += " The projects are: " + mcpProjectNames(projects) + "."
	}
	return model.Project{}, &mcpToolError{outcome: "not_found", text: msg}
}

// projectFor resolves the project for a tool given refs: arg when passed,
// else the project the first full ref names by its key (WL-12, WL-SPEC-3),
// else the session's default. A full ref needs no project argument.
func (call *mcpCall) projectFor(arg string, refs ...string) (model.Project, error) {
	if strings.TrimSpace(arg) == "" {
		if p, ok := call.refProject(refs...); ok {
			return p, nil
		}
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
