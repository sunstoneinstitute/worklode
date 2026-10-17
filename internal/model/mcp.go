package model

// mcp.go holds the shapes the MCP server at /mcp (internal/api/mcp.go)
// exchanges with its clients: the RFC 9728 protected-resource metadata, and
// each tool's arguments and structured result. Tool arguments carry a
// `jsonschema` tag, which the go-sdk turns into the description of that
// property in the tool's input schema; the tag is the text Claude reads when
// deciding what to pass.

// ProtectedResourceMetadata is the RFC 9728 document served at
// /.well-known/oauth-protected-resource[/mcp]: which resource this is and
// which authorization server issues tokens for it.
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name,omitempty"`
}

// MCPProjectListInput is project_list's arguments.
type MCPProjectListInput struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"the next_cursor from a previous call, to read the next page"`
}

// MCPProjectResolveInput is project_resolve's arguments.
type MCPProjectResolveInput struct {
	Spoken string `json:"spoken" jsonschema:"the project name as the user said or typed it, e.g. 'work load'"`
}

// MCPProjectUseInput is project_use's arguments.
type MCPProjectUseInput struct {
	Project string `json:"project" jsonschema:"project key, id or name, e.g. WL"`
}

// MCPSearchInput is search's arguments.
type MCPSearchInput struct {
	Query   string   `json:"query" jsonschema:"what to look for, in plain words"`
	Kinds   []string `json:"kinds,omitempty" jsonschema:"limit to these result kinds: doc, task, skill"`
	Project string   `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
	Cursor  string   `json:"cursor,omitempty" jsonschema:"the next_cursor from a previous call"`
}

// MCPShowInput is show's arguments.
type MCPShowInput struct {
	Ref     string `json:"ref" jsonschema:"what to show: WL-12, WL-SPEC-77, WL-PLAN-3, WL-REQ-1300, a project key, or a spoken form like 'spec seventy-seven' or 'task five twelve'"`
	Inline  *bool  `json:"inline,omitempty" jsonschema:"fold every in-force amendment into the text (default true)"`
	Section string `json:"section,omitempty" jsonschema:"for a document, only this section: sec-3 or 3"`
	Project string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPFileInput is file's arguments.
type MCPFileInput struct {
	What    string `json:"what" jsonschema:"the kind of thing in the user's own word: bug, feature, chore, spec, plan, requirement, decision, ..."`
	Title   string `json:"title" jsonschema:"a short title (for a rule, its heading)"`
	Body    string `json:"body,omitempty" jsonschema:"markdown body"`
	Parent  string `json:"parent,omitempty" jsonschema:"for a task, the parent task id"`
	Doc     string `json:"doc,omitempty" jsonschema:"for a plan, the spec or rule it covers; for a rule, the spec to arrange it in"`
	Project string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPTaskListInput is task_list's arguments.
type MCPTaskListInput struct {
	Kind     string `json:"kind,omitempty" jsonschema:"task kind, or a word for one such as 'bugs'"`
	State    string `json:"state,omitempty" jsonschema:"task state, e.g. ready or in_progress; 'all' includes delivered and abandoned tasks; default is open tasks"`
	Assignee string `json:"assignee,omitempty" jsonschema:"actor id of the assignee"`
	Project  string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"the next_cursor from a previous call"`
}

// MCPTaskAddInput is task_add's arguments.
type MCPTaskAddInput struct {
	Kind       string   `json:"kind" jsonschema:"feature, bug, chore, design, review, spike, decision or rally"`
	Title      string   `json:"title" jsonschema:"a short title"`
	Body       string   `json:"body,omitempty" jsonschema:"markdown body"`
	Priority   string   `json:"priority,omitempty" jsonschema:"critical, high, medium (default) or low"`
	Parent     string   `json:"parent,omitempty" jsonschema:"parent task id"`
	BlockedBy  []string `json:"blocked_by,omitempty" jsonschema:"task ids that block the new task"`
	GovernedBy []string `json:"governed_by,omitempty" jsonschema:"rule refs that govern the new task, e.g. WL-REQ-12"`
	Project    string   `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPTaskUpdateInput is task_update's arguments. A nil field is unchanged.
type MCPTaskUpdateInput struct {
	ID       string  `json:"id" jsonschema:"task id, e.g. WL-12, a bare number, or 'task five twelve'"`
	Title    *string `json:"title,omitempty" jsonschema:"new title"`
	Body     *string `json:"body,omitempty" jsonschema:"new markdown body, replacing the old one"`
	Priority *string `json:"priority,omitempty" jsonschema:"critical, high, medium or low"`
	Project  string  `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPTaskSetStateInput is task_set_state's arguments.
type MCPTaskSetStateInput struct {
	ID      string `json:"id" jsonschema:"task id, e.g. WL-12"`
	State   string `json:"state" jsonschema:"ready, in_progress, in_review, merged, deployed_dev, deployed_prod, released or abandoned"`
	Project string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPDocListInput is doc_list's arguments.
type MCPDocListInput struct {
	Kind    string `json:"kind,omitempty" jsonschema:"spec or plan, or a word for one"`
	Status  string `json:"status,omitempty" jsonschema:"draft, accepted, superseded, withdrawn, or all"`
	Project string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"the next_cursor from a previous call"`
}

// MCPDocAddInput is doc_add's arguments.
type MCPDocAddInput struct {
	Kind    string   `json:"kind" jsonschema:"spec or plan"`
	Title   string   `json:"title" jsonschema:"the document's title"`
	Body    string   `json:"body,omitempty" jsonschema:"markdown body below the title, no frontmatter"`
	Covers  []string `json:"covers,omitempty" jsonschema:"for a plan, the rule refs or spec sections it covers, e.g. WL-REQ-12 or WL-SPEC-7#sec-2"`
	Project string   `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPDocEditInput is doc_edit's arguments.
type MCPDocEditInput struct {
	ID          string `json:"id" jsonschema:"document ref, e.g. WL-SPEC-77 or 'plan three'"`
	Body        string `json:"body,omitempty" jsonschema:"the whole new markdown body, no frontmatter; a draft or plan is replaced in place, an accepted spec is amended"`
	Title       string `json:"title,omitempty" jsonschema:"new title"`
	IfVersion   int    `json:"if_version,omitempty" jsonschema:"only write if the document is still at this version (from show); refused otherwise"`
	Note        string `json:"note,omitempty" jsonschema:"amending an accepted spec: what changed and why (required unless substantive)"`
	Substantive bool   `json:"substantive,omitempty" jsonschema:"amending an accepted spec: the change is substantive, so its reviewers are asked again"`
	Project     string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPDocTransitionInput is doc_transition's arguments.
type MCPDocTransitionInput struct {
	ID      string `json:"id" jsonschema:"document ref, e.g. WL-SPEC-77"`
	Action  string `json:"action" jsonschema:"submit (for review), accept, revise (open a revision of an accepted spec) or withdraw"`
	Reason  string `json:"reason,omitempty" jsonschema:"withdraw only: why"`
	Project string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPRuleListInput is rule_list's arguments.
type MCPRuleListInput struct {
	Doc     string `json:"doc,omitempty" jsonschema:"only the rules this spec arranges, in order, e.g. WL-SPEC-73"`
	Status  string `json:"status,omitempty" jsonschema:"draft, accepted, superseded or withdrawn"`
	Project string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"the next_cursor from a previous call"`
}

// MCPRuleAddInput is rule_add's arguments.
type MCPRuleAddInput struct {
	Heading string   `json:"heading" jsonschema:"the rule's heading"`
	Body    string   `json:"body" jsonschema:"the rule's markdown text"`
	Kind    string   `json:"kind,omitempty" jsonschema:"requirement (default), catalogue, invariant, definition or principle"`
	Tags    []string `json:"tags,omitempty" jsonschema:"tags for the rule"`
	Doc     string   `json:"doc,omitempty" jsonschema:"a spec to arrange the new rule in, e.g. WL-SPEC-77"`
	Project string   `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPRuleEditInput is rule_edit's arguments.
type MCPRuleEditInput struct {
	Ref     string `json:"ref" jsonschema:"rule ref, e.g. WL-REQ-1300 or 'requirement thirteen hundred'"`
	Body    string `json:"body" jsonschema:"the new text under the heading"`
	Heading string `json:"heading,omitempty" jsonschema:"new heading; unchanged when omitted"`
	Project string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPRuleLinkInput is rule_link's arguments.
type MCPRuleLinkInput struct {
	Ref     string `json:"ref" jsonschema:"the rule the edge leaves from"`
	Rel     string `json:"rel" jsonschema:"refines, needs or references"`
	Target  string `json:"target" jsonschema:"the rule the edge points at"`
	Remove  bool   `json:"remove,omitempty" jsonschema:"remove the edge instead of adding it"`
	Project string `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPRuleSupersedeInput is rule_supersede's arguments.
type MCPRuleSupersedeInput struct {
	Entries []SupersedeEntry `json:"entries" jsonschema:"each old rule and the rules that replace it (none withdraws it)"`
	DryRun  bool             `json:"dry_run,omitempty" jsonschema:"report what would change without writing"`
	Project string           `json:"project,omitempty" jsonschema:"Worklode project key; omit to use the session's project"`
}

// MCPProject is one project in a tool result.
type MCPProject struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// MCPProjectListOutput is project_list's result.
type MCPProjectListOutput struct {
	Projects   []MCPProject `json:"projects"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// MCPProjectCandidate is one ranked match from project_resolve.
type MCPProjectCandidate struct {
	MCPProject
	Confidence float64 `json:"confidence"`
}

// MCPProjectResolveOutput is project_resolve's result. Clear is true when
// the first candidate is a clear winner.
type MCPProjectResolveOutput struct {
	Candidates []MCPProjectCandidate `json:"candidates"`
	Clear      bool                  `json:"clear"`
}

// MCPProjectUseOutput is project_use's result.
type MCPProjectUseOutput struct {
	Project MCPProject `json:"project"`
}

// MCPSearchHit is one search result.
type MCPSearchHit struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Title   string `json:"title"`
	Anchor  string `json:"anchor,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

// MCPSearchOutput is search's result.
type MCPSearchOutput struct {
	Hits       []MCPSearchHit `json:"hits"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// MCPShowOutput is show's result; the text itself is the tool's content.
type MCPShowOutput struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Title string `json:"title"`
}

// MCPTaskItem is one task in a tool result.
type MCPTaskItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Priority string `json:"priority"`
	Assignee string `json:"assignee,omitempty"`
}

// MCPTaskListOutput is task_list's result.
type MCPTaskListOutput struct {
	Tasks      []MCPTaskItem `json:"tasks"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// MCPTaskOutput is the result of a tool that writes one task.
type MCPTaskOutput struct {
	Task MCPTaskItem `json:"task"`
}

// MCPDocItem is one document in a tool result.
type MCPDocItem struct {
	ID      int64  `json:"id"`
	Ref     string `json:"ref"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Version int    `json:"version"`
}

// MCPDocListOutput is doc_list's result.
type MCPDocListOutput struct {
	Docs       []MCPDocItem `json:"docs"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// MCPDocOutput is the result of a tool that writes one document.
type MCPDocOutput struct {
	Doc MCPDocItem `json:"doc"`
}

// MCPRuleItem is one rule in a tool result.
type MCPRuleItem struct {
	Ref     string `json:"ref"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Version int    `json:"version"`
	Heading string `json:"heading"`
}

// MCPRuleListOutput is rule_list's result.
type MCPRuleListOutput struct {
	Rules      []MCPRuleItem `json:"rules"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// MCPRuleOutput is the result of a tool that writes one rule.
type MCPRuleOutput struct {
	Rule MCPRuleItem `json:"rule"`
}

// MCPRuleLinkOutput is rule_link's result.
type MCPRuleLinkOutput struct {
	Ref     string `json:"ref"`
	Rel     string `json:"rel"`
	Target  string `json:"target"`
	Removed bool   `json:"removed"`
}

// MCPSupersedeOutput is rule_supersede's result.
type MCPSupersedeOutput struct {
	Withdrawn  int  `json:"withdrawn"`
	Edges      int  `json:"edges"`
	Tasks      int  `json:"tasks"`
	StalePlans int  `json:"stale_plans"`
	DryRun     bool `json:"dry_run"`
}

// MCPFileOutput is file's result: what the spoken word filed as and the ref
// of what was created.
type MCPFileOutput struct {
	Entity string `json:"entity"`
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Title  string `json:"title"`
}
