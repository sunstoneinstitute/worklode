package ui

// views.go defines the presentation view types the templ components render,
// plus the small presentation helpers (chip-variant mapping, pluralization)
// they call. These types are ui's own vocabulary: internal/api maps
// internal/model values (model.BoardResponse, model.CockpitProjection, ...)
// into them in render.go, so the dependency only ever points api -> ui. View
// types may embed internal/model types (ui may import model) but never
// reference api's DTOs (ADR 036 §3).

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// PageProps carries the fields the Page shell needs on every page: the
// document title and which primary-nav destination to mark aria-current.
// ActiveGlobal drives the top bar's navigation marking: one of "ideas",
// "intake", "projects", "work", "knowledge" — spec 056 §1's five
// destinations. It is left empty on project-scoped pages, whose project-local
// nav carries the current-page marker instead, and on pages that name no
// destination: the task page, Home, Reviews and Deliveries, which kept their
// routes when §1 took them off the list, and the inbox (056 §3), which never
// had one. No page ever sets aria-current="page" twice, or on both navs.
type PageProps struct {
	Title        string
	ActiveGlobal string
}

// --- board (Work) ------------------------------------------------------------

// BoardView is the org-wide board rendered at Work ("/work").
type BoardView struct {
	Page           PageProps
	InboxCount     int
	Projects       []BoardProject
	RecentFailures []BoardFailure
}

// BoardProject is one project's four state buckets on the board. Each row
// embeds model.BoardTask directly; the template reads its Task fields
// (ID/Title/Priority/State/Assignee) plus Holder.
type BoardProject struct {
	ID         string
	Name       string
	InProgress []model.BoardTask
	InReview   []model.BoardTask
	Ready      []model.BoardTask
	Blocked    []model.BoardTask
}

// BoardFailure is one recent runtime failure on the org-wide board.
type BoardFailure struct {
	OccurredAt time.Time
	Cluster    string
	Kind       string
	Workload   string
	Message    string
}

// --- drift board (spec 007) --------------------------------------------------

// DriftView is the read-only drift board at /drift: four of spec 007's five
// views, composed from internal/model directly because every field is a fact
// the overview service already computed in the shape the page renders.
//
// Frontier and CriticalPath are backbone-authoritative and always render.
// GraphEnabled reports whether a graph-server is configured; when it is not,
// Drift and Gaps carry no data — not zero findings — so the page says the
// graph is unconfigured rather than showing empty tables that would read as
// "no drift".
type DriftView struct {
	Page         PageProps
	Frontier     []model.FrontierTask
	CriticalPath model.CriticalPath
	Drift        model.Drift
	Gaps         []model.Gap
	GraphEnabled bool
}

// gapSubject names what a gap finding is about: the component with no
// governing doc, or the repository holding an unmatched path (spec 007 §4.2
// sets exactly one of the two).
func gapSubject(g model.Gap) string {
	if g.Component != "" {
		return g.Component
	}
	return g.Repo
}

// --- run board (032 §8) ------------------------------------------------------

// RunBoardView is a project's run board (032 §8): its live work grouped
// into the spec's six fixed groups, rendered inside the project sidebar
// frame like the other project-local destinations.
type RunBoardView struct {
	Page         PageProps
	CanonicalURL string
	Project      CockpitProject
	Groups       []RunGroupView
}

// RunGroupView is one §8 group in the spec's pinned order: its heading, the
// rows in it, and how many more rows exist beyond the bound (always 0 for
// Ready/Running/Waiting/Needs judgment, which are never bounded).
type RunGroupView struct {
	Label string
	Rows  []RunRowView
	More  int
}

// RunRowView is one task's row on the run board, every field pre-formatted
// so the templ component stays dumb (see internal/api/runboard.go's
// assembleRunBoard). Ready and terminal (Failed/Completed) rows carry only
// TaskID/Title/TaskURL/Owner; Waiting rows also set Holds; Running and
// Needs-judgment rows carry the full §8 fact list — Delegate, LeaseAge,
// LastEvent, Costs, and the PR/check labels — left empty/nil rather than
// invented when the underlying fact is absent.
type RunRowView struct {
	TaskID, Title, TaskURL string
	Owner, Delegate        string
	LeaseAge, LastEvent    string
	Holds                  string
	PRLabel, PRURL         string
	CheckLabel             string
	Costs                  []string
}

// --- projects portfolio -----------------------------------------------------

// ProjectsView is the cross-project portfolio. It embeds model.Project rows
// directly; the template reads ID/Name/Key.
type ProjectsView struct {
	Page     PageProps
	Projects []model.Project
}

// --- reviews (spec 029 §7.1) -------------------------------------------------

// ApprovalsView is the /reviews queue: every approval still awaiting a
// decision, whatever kind of entity it governs, oldest first. Each row
// carries the decide form (029 §7.3).
type ApprovalsView struct {
	Page PageProps
	Rows []ApprovalRow
}

// ApprovalRow is one awaiting-approval queue row: the entity it governs (a
// pull request, a document), the task and project it belongs to, who it is
// awaiting (when known), and how long it has waited. ID is the approvals row
// id the decide form posts to.
//
// Everything below Title/URL is optional and rendered only when set: a
// document hangs off its project with no task in between, and a row whose
// kind nothing correlates has neither. Kind, Revision and Age are
// pre-formatted for display (see FmtAge).
//
// Lane is the flow requirement this row answers (029 §7.2), "" for a row that
// answers no named lane. Two lanes of one revision are two rows, and the lane
// is what tells them apart on the page.
//
// Decidable is false for a row whose approval names no revision: the store
// refuses that decide, so the page renders why instead of the form.
type ApprovalRow struct {
	ID                int64
	Kind              string // the entity kind: "pr", "doc", "deliverable", "task"
	EntityID          string
	Title, URL        string
	Revision          string // the version under review, when the kind has one
	Lane              string
	TaskID, ProjectID string
	ProjectName       string
	RequiredActorName string
	Age               string
	Decidable         bool
	// ReturnURL is the page deciding this row should come back to, for a row
	// rendered somewhere other than the Reviews queue. Empty on the queue
	// itself, which is where the act redirects by default; it must be a path
	// on this site, and the handler refuses anything else.
	ReturnURL string
}

// --- inbox (spec 056 §3) -----------------------------------------------------

// InboxView is the cross-project inbox at "/inbox": what is waiting on the
// signed-in actor, in 056 §3.2's fixed bucket order. Only buckets that hold
// something are present, so the page never renders an empty heading.
type InboxView struct {
	Page    PageProps
	Buckets []InboxBucket
}

// InboxBucket is one §3.2 bucket: its heading and its items, already ranked.
type InboxBucket struct {
	Label string
	Items []InboxItem
}

// InboxItem is one row: the text it shows, where it links, and a muted
// detail line (project, age).
type InboxItem struct {
	Text   string
	Href   string
	Detail string
}

// --- task -------------------------------------------------------------------

// TaskView is one task's detail page. Task/Holder/Progress/Attachments embed
// internal/model types directly; the edge lists are task ids.
type TaskView struct {
	Page PageProps
	// Project is the task's owning project identity — id, name, key — the
	// same lookup projectHeader makes for every other project-scoped page.
	// The task page renders through projectShell with it (spec 056 §2), so
	// it carries the project sidebar like every other project destination.
	Project CockpitProject
	Task    model.Task
	// BodyHTML is Task.Body rendered from markdown and sanitised by
	// internal/mdrender, which internal/api calls: ui may not import it (it
	// imports goldmark and bluemonday, and ui is a stdlib+model leaf). It is
	// the only value any component emits unescaped, and it is safe only
	// because mdrender's allowlist already stripped every element, attribute
	// and URL scheme not on it — never assign anything else here.
	BodyHTML template.HTML
	// Attachments is the task's blob reference graph row, embedded and
	// attached alike (spec 021 §3), with URL filled in at the HTTP boundary.
	Attachments []model.TaskBlob
	Blocked     bool
	Holder      *model.Lease
	Blocks      []string
	BlockedBy   []string
	Parent      string
	Children    []string
	FollowUpTo  string
	FollowUps   []string
	DuplicateOf string
	Duplicates  []string
	Progress    model.TaskProgress
	Timeline    []TimelineRow
	// AgentSessions is the coding-agent sessions recorded against the task's
	// active lease, oldest first. Empty when nothing holds the task — a lease
	// is what a session is recorded against, so there is nowhere for one to
	// hang otherwise.
	AgentSessions []AgentSessionRow
}

// AgentSessionRow is one coding-agent session as a page renders it: the
// harness, who is running it, and how long it has been alive. Times are
// pre-formatted relative strings because "last seen 3m ago" is the question a
// reader is actually asking, and internal/ui has no clock of its own to
// answer it with — internal/api formats them at the render seam.
//
// Task and TaskTitle are set only on the project page, where a session has to
// name the work it is on; on a task page they would repeat the heading.
type AgentSessionRow struct {
	Agent        string
	AgentVersion string
	ActorID      string
	Task         string
	TaskTitle    string
	TaskURL      string
	Started      string
	LastSeen     string
	// Running is false once the session has ended. A task page shows both,
	// since a finished session is still part of that task's story.
	Running bool
}

// TimelineRow is one rendered row of a task's timeline: a type label and a
// human summary line, derived from the same entries the JSON timeline API
// emits. URL is the entry's source-native link (set for pr and ci entries
// only; "" otherwise) — rendered as a plain string href, so templ's SafeURL
// sanitizer neutralizes an unsafe scheme before it reaches the page.
type TimelineRow struct {
	At      time.Time
	Type    string
	Label   string
	Summary string
	// Detail is the untruncated summary, set only when Summary was cut to
	// fit the column (a body edit carries the whole new body). The task page
	// puts it behind a "see more" disclosure; empty means nothing was cut.
	Detail string
	URL    string
}

// --- placeholder ------------------------------------------------------------

// PlaceholderView is an honest "not built yet" page for a global or
// project-scoped destination whose governing spec section is not implemented.
// Project is nil for a global destination (Intake, Deliveries) and set for a
// project section (Crew, Deliverables, Reviews, Decisions, Documents,
// Activity), which renders the same project-local navigation and header as
// the project overview page.
type PlaceholderView struct {
	Page          PageProps
	Heading       string
	Message       string
	Project       *CockpitProject
	CanonicalURL  string
	ActiveSection string
}

// --- project cockpit --------------------------------------------------------

// CockpitView is the project cockpit page (project overview), rendered in the
// prototype's Operations mode (docs/mockups/cockpit/index.html, mode B). It is
// the subset of model.CockpitProjection mode B shows, flattened for rendering
// — not the whole projection: the projection's ranking focus and mapped
// repositories have no panel in mode B, so they are absent here rather than
// carried unrendered (WL-164); the JSON cockpit still serves both.
// Rally, PinnedFocus and NextDecision are optional facts, and each panel is
// omitted honestly when its data is absent rather than rendering an invented
// placeholder.
type CockpitView struct {
	Page         PageProps
	CanonicalURL string
	NewTaskURL   string
	Project      CockpitProject
	// Rally is the project's active rally (WL-667), shown above PinnedFocus.
	// Nil when the project has none active — a project may hold any number of
	// draft rallies, which are inert and never rendered here, and at most one
	// active, per the tasks_one_active_rally index.
	Rally             *CockpitRally
	PinnedFocus       *CockpitFocus
	NextDecision      *CockpitDecision
	Work              CockpitWork
	SecondaryConcerns []CockpitConcern
	CostTotals        []CockpitCostTotal
	// AgentSessions is the agent sessions running on this project's tasks
	// right now, liveliest first. It is carried on the view rather than added
	// to model.CockpitProjection: the JSON projection's shape is contracted
	// by spec 032, and this is a page affordance, not a change to that
	// contract.
	AgentSessions []AgentSessionRow
}

// CockpitRally is the project's active rally: a hand-assembled goal that
// carries no work of its own, its blocks edges naming the tasks to finish
// now (WL-667). Done/Total count its live direct members regardless of state;
// Members lists only the ones still open. Pickup ranking boosts more than
// this card shows — the whole transitive blocker closure, not just depth 1.
type CockpitRally struct {
	ID      string
	Title   string
	URL     string
	Done    int
	Total   int
	Members []CockpitRallyMember
}

// CockpitRallyMember is one of a rally's still-open members.
type CockpitRallyMember struct {
	ID    string
	Title string
	URL   string
}

// CockpitFocus is the project's pinned focus note shown at the top of the
// Operations canvas: a short human-authored steer, who pinned it, and when.
// PinnedBy is the pinner's display name ("" when unknown).
type CockpitFocus struct {
	Note     string
	PinnedBy string
	PinnedAt time.Time
}

// CockpitProject is the project identity shown in the cockpit and the
// project-scoped sidebar.
//
// ModeName/ModeBasis carry the cockpit's operating mode, which the sidebar
// renders as a pill under the project key. Only the cockpit sets them; the
// other project pages leave them empty and the sidebar renders no pill, so the
// mode fact has one home rather than one per page that shows it.
type CockpitProject struct {
	ID        string
	Name      string
	Key       string
	ModeName  string
	ModeBasis string

	// HasSpecs says whether the project has at least one spec. The sidebar's
	// Progress entry is conditional on it (WL-SPEC-66 §2, amending 056 §2):
	// a project with no spec has no Progress page to link to.
	HasSpecs bool
}

// CockpitWork holds the cockpit's four work buckets.
type CockpitWork struct {
	InProgress []WorkRow
	InReview   []WorkRow
	Ready      []WorkRow
	Blocked    []WorkRow
}

// count is the total number of active work items across all four buckets — the
// Active-work list renders an honest "no active work" line when it is zero.
func (w CockpitWork) count() int {
	return len(w.InProgress) + len(w.InReview) + len(w.Ready) + len(w.Blocked)
}

// Rows is every active work item in the order the Active-work list renders
// them: in progress, in review, ready, blocked. The bucket order is a fact of
// the view, not of the markup, so the template walks one sequence.
func (w CockpitWork) Rows() []WorkRow {
	rows := make([]WorkRow, 0, w.count())
	for _, bucket := range [][]WorkRow{w.InProgress, w.InReview, w.Ready, w.Blocked} {
		rows = append(rows, bucket...)
	}
	return rows
}

// WorkRow is one cockpit work item: the task link, its state, the evidence
// behind that state, and the resolved owner/delegate display names ("" when
// none).
type WorkRow struct {
	ID               string
	Title            string
	State            string
	Priority         string
	URL              string
	Owner            string
	Delegate         string
	EvidenceCategory string
	EvidenceSummary  string
}

// CockpitConcern is one secondary concern on the cockpit: what holds a ready
// task — an open blocker task or an unfinished blocking plan.
type CockpitConcern struct {
	Title           string
	URL             string
	EvidenceSummary string
}

// CockpitCostTotal is a per-currency cost total for the cockpit's cost window.
type CockpitCostTotal struct {
	Currency       string
	CostAmount     string
	UnpricedTokens int64
	// OverheadCostAmount is the share of CostAmount that had no task to bill
	// to — orchestration run from the main checkout (spec 052).
	OverheadCostAmount string
}

// CockpitDecision is the next governed decision shown in the decision aside.
type CockpitDecision struct {
	Title       string
	Accountable string
	Readiness   string
}

// --- deliverables -----------------------------------------------------------

// DeliverablesView is a project's declared deliverables (spec 029 §3), the
// project-local Deliverables destination. NewURL is the "Declare a
// deliverable" form; Groups holding no row at all renders an honest empty
// state next to that form, never a fabricated row.
type DeliverablesView struct {
	Page         PageProps
	CanonicalURL string
	Project      CockpitProject
	NewURL       string
	// Groups is the page's deliverables, grouped by milestone (spec 029 §2):
	// one group per milestone that holds one, in the project's milestone
	// order, then an unattached group last. A single group renders with no
	// header — the flat list a project with no milestones has always shown.
	Groups []DeliverableGroup
}

// DeliverableGroup is one milestone's worth of deliverables on the
// Deliverables page, or the unattached group when MilestoneID is "".
type DeliverableGroup struct {
	MilestoneID    string
	MilestoneTitle string
	Rows           []DeliverableRow
}

// totalRows sums every group's rows, for the page's empty-state check: it is
// distinct from "no groups" because the unattached group is always present.
func (v DeliverablesView) totalRows() int {
	n := 0
	for _, g := range v.Groups {
		n += len(g.Rows)
	}
	return n
}

// header is the group's subsection heading: the milestone's title, or "No
// milestone" for the unattached group.
func (g DeliverableGroup) header() string {
	if g.MilestoneTitle == "" {
		return "No milestone"
	}
	return g.MilestoneTitle
}

// DeliverableRow is one declared deliverable. Spec 029 §3.2 makes deliverable
// state a fact emitters and probers report, never one the deliverable stores,
// so ReportedState and ReportedAt come from the newest evidence filed against
// Artifact and are empty until something reports. A row with nothing reported
// says "Declared" rather than inventing a status.
type DeliverableRow struct {
	ID          string
	Name        string
	Description string
	URL         string
	CreatedBy   string
	CreatedAt   time.Time

	// Artifact is the address the deliverable declares it is verified by
	// (029 §3.1) — a catalog identifier, not necessarily a browser link, so
	// it renders as text and never as an href.
	Artifact string

	// ReportedState is the newest reported state of Artifact
	// (published | updated | deprecated | removed | failed), "" when nothing
	// has reported; ReportedAt is when that report says it happened.
	ReportedState string
	ReportedAt    *time.Time
}

// deliverableChip maps a deliverable's reported state to its .chip variant.
// An unreported deliverable keeps the "declared" evidence chip: a declaration
// is all it honestly carries (spec 032 §1). Anything reported is observed
// evidence, coloured by what the state means for the deliverable.
func deliverableChip(state string) string {
	switch state {
	case "":
		return "declared"
	case "published", "updated":
		return "ok"
	case "deprecated":
		return "warn"
	case "removed", "failed":
		return "crit"
	default:
		return "observed"
	}
}

// deliverableLabel is the chip's text: the reported state, or "Declared" when
// nothing has reported one.
func deliverableLabel(state string) string {
	if state == "" {
		return "Declared"
	}
	return strings.ToUpper(state[:1]) + state[1:]
}

// --- milestones --------------------------------------------------------------

// MilestonesView is the project-local Milestones destination (spec 029 §2).
// An empty Milestones slice renders an honest empty state, never a
// fabricated row.
type MilestonesView struct {
	Page         PageProps
	CanonicalURL string
	Project      CockpitProject
	Milestones   []MilestoneSection
}

// MilestoneSection is one milestone with the children its progress was
// derived from. The counts echo model.MilestoneProgress; the view repeats
// the numbers, never the derivation.
type MilestoneSection struct {
	ID                string
	Title             string
	TasksTotal        int
	TasksClosed       int
	DeliverablesTotal int
	DeliverablesLive  int
	Tasks             []MilestoneTaskRow
	Deliverables      []DeliverableRow
}

// MilestoneTaskRow is one task in a milestone section, linking to the task
// page the way every other task list does.
type MilestoneTaskRow struct {
	ID       string
	Title    string
	State    string
	Assignee string
}

// progress is the section's one-line readout: plain counts of what the store
// derived. Spec 029 §2 makes progress a query over children, and a
// percentage or a bar would claim a precision two small integers do not
// carry.
func (m MilestoneSection) progress() string {
	return fmt.Sprintf("%d/%d tasks closed · %d/%d deliverables live",
		m.TasksClosed, m.TasksTotal, m.DeliverablesLive, m.DeliverablesTotal)
}

// hasChildren reports whether anything is attached. A milestone with nothing
// attached says so instead of rendering two empty containers.
func (m MilestoneSection) hasChildren() bool {
	return len(m.Tasks) > 0 || len(m.Deliverables) > 0
}

// --- crew --------------------------------------------------------------------

// CrewView is a project's Crew roster (spec 029 §6.1), the project-local
// Crew destination. An empty Members slice renders an honest "No Crew yet"
// state, never a fabricated row.
type CrewView struct {
	Page         PageProps
	CanonicalURL string
	Project      CockpitProject
	Members      []CrewMember

	// AddAction is where the add-member form POSTs. Add is what the person
	// typed, preserved so a rejected submit comes back with the form filled
	// in, and AddError is the one message to fix ("" on first render).
	// Roles is the fixed role vocabulary as the dropdown's options (WL-297),
	// with the submitted (or default) role marked selected.
	AddAction string
	Add       CrewFormValues
	AddError  string
	Roles     []FormOption

	// RemoveAction is where each non-lead member's Remove button POSTs; the
	// member is named in a hidden field. RemoveError is the one message a
	// refused removal shows ("" otherwise), and Responsibilities is that
	// member's open work — spec 032 §6's responsibility review: what has to
	// be reassigned or closed before the removal can proceed (spec 029 §6.1).
	RemoveAction     string
	RemoveError      string
	Responsibilities []CrewWorkItem
}

// CrewWorkItem is one open item a Crew member owns, shown when their
// removal is refused. Kind is "task" today (internal/store's OwnedWork);
// the responsibility review does not yet count a member's open approvals.
type CrewWorkItem struct {
	Kind  string
	ID    string
	Title string
	State string
}

// CrewFormValues are the add-member form's fields as submitted. Role is one
// of the fixed project-role vocabulary (WL-297) — CrewView.Roles carries the
// menu the page offers, and the store and migration 0046's CHECK enforce the
// same set.
type CrewFormValues struct {
	Actor  string
	Role   string
	Lead   bool
	Deputy bool
}

// CrewMember is one Crew member: an actor holding at least one role-labelled
// project_participants row, folded to one row per actor (internal/store's
// ListParticipants already aggregates this). Exactly one member on a project
// may have IsLead set (032 §6's "accountable human").
type CrewMember struct {
	ActorID     string
	DisplayName string
	Roles       []string
	IsLead      bool
}

// --- deleted -----------------------------------------------------------------

// DeletedView is a project's tombstoned tasks and documents (spec 044 §2),
// the project-local Deleted destination. Every other cockpit page reads
// through the same filtered store calls the CLI does, so a deleted row
// disappears from all of them; this page is the one that shows them, and the
// only surface besides the CLI and the JSON API where the justification a
// delete carried can be read.
//
// Restoring is a per-row form, one action per entity kind: the two undeletes
// are different capabilities (permTaskWrite and permDocWrite) and a single
// route could carry only one of them.
type DeletedView struct {
	Page         PageProps
	CanonicalURL string
	Project      CockpitProject
	Tasks        []model.Task
	Docs         []DeletedDocRow

	// RestoreTaskAction and RestoreDocAction are where a row's Restore
	// button POSTs; the row is named in a hidden field. RestoreError is the
	// one message a refused restore shows ("" otherwise).
	RestoreTaskAction string
	RestoreDocAction  string
	RestoreError      string
}

// DeletedDocRow is one tombstoned document, with the corpus reference
// pre-formatted the way the document index formats it ("spec 25"). Bodies are
// dropped before the view is built, for the reason docsView states.
type DeletedDocRow struct {
	Doc model.Doc
	URL string
	Ref string
}

// tombstone reads the delete record off a task or document for rendering.
// Every row on this page has one — the lists it comes from select on
// `deleted_at IS NOT NULL` — but the field is a pointer on the wire, so the
// nil case still needs an answer rather than a panic.
func tombstone(t *model.Tombstone) model.Tombstone {
	if t == nil {
		return model.Tombstone{}
	}
	return *t
}

// --- documents ---------------------------------------------------------------

// DocsView is the document corpus index (GET /docs): every spec, ADR and plan
// the backbone holds (025 §5). Read-only — a document's body is an artifact
// authored in a file and submitted through the API, not typed into a page.
type DocsView struct {
	Page PageProps
	Docs []DocRow
}

// DocRow is one document in the index: the stored row, its page URL, and the
// direct web reference pre-formatted for display ("WL-SPEC-25"; a plan carries
// no shorthand and keeps its kind label).
type DocRow struct {
	Doc model.Doc
	URL string
	Ref string
}

// DocView is one document's page (GET /docs/{id}): the stored row, its
// sections with their accept-time state, its edges in both directions, and
// the open candidate revision when one exists (nil otherwise).
type DocView struct {
	Page PageProps
	Doc  model.Doc
	// BodyHTML is Doc.Body rendered from markdown and sanitised by
	// internal/mdrender, filled in by internal/api for the reason TaskView's
	// field gives. The document flavour keeps a {#sec-N} heading anchor, so
	// the Sections table below can link into the body; nothing else about the
	// allowlist differs. The same "never assign anything else here" rule
	// applies.
	BodyHTML template.HTML
	Ref      string
	Sections []model.DocSection
	Edges    []DocEdgeRow
	EdgesIn  []DocEdgeRow
	Revision *model.DocRevision
	// Versions is the document's version history (025 §4.5), newest first —
	// its live row and every version it has superseded — rendered as the
	// Versions table below the chips.
	Versions []model.DocVersionSummary
	// Consolidated says BodyHTML is the folded view (026 §3.2) rather than
	// the stored source: every effective amendment and supersession inlined
	// under the section it acts on. That is the page's default, because a
	// reviewer reading the source of a spec four other specs have amended is
	// reading something no longer true. SourceURL and ConsolidatedURL are the
	// two spellings of this page; the toggle links at whichever is not shown.
	// Consolidated is false when the fold failed, and the page then says so
	// rather than passing the source off as the current state.
	Consolidated    bool
	ConsolidatedURL string
	SourceURL       string
	// Notes are the document's anchored notes (025 §8.5), oldest first.
	Notes []DocNoteRow
	// Reviewers is the durable reviewer roster (025 §7.3), each row saying
	// whether that reviewer still owes a verdict on the current version.
	Reviewers []DocReviewerRow
	// Approvals are this document's still-awaiting approval rows (029 §7),
	// each carrying the decide form the Reviews queue uses. Empty when none
	// is open, which is the ordinary case for an accepted document.
	Approvals []ApprovalRow
}

// DocNoteRow is one anchored note rendered for the page: the note's own body
// as sanitised markdown, plus who left it, when, and the section it hangs on.
// Task is the task that raised it, "" when a human at a prompt did.
type DocNoteRow struct {
	Anchor    string
	BodyHTML  template.HTML
	CreatedBy string
	CreatedAt time.Time
	Task      string
}

// DocReviewerRow is one member of a document's reviewer roster. Awaiting is
// true while that reviewer has neither approved nor requested changes on the
// current version.
type DocReviewerRow struct {
	Actor    string
	Awaiting bool
}

// DocVersionView is one version's page (GET /docs/versions/{id}/{n}): the
// document's live identity for the header chips (status, project, corpus
// reference) alongside the specific version rendered — its own title and
// body, since a superseded version's title can differ from the current
// one's. Current is false for every version but the document's live one,
// which is what shows the "back to current" banner; DocURL is where that
// banner links.
type DocVersionView struct {
	Page     PageProps
	Doc      model.Doc
	Ref      string
	Version  model.DocVersion
	BodyHTML template.HTML
	Current  bool
	DocURL   string
}

// docVersionURL is a version's page path, always addressed by the document's
// numeric id rather than its corpus shorthand — this route, unlike docPage,
// takes no ref form. Shaped /docs/versions/{id}/{n} rather than
// /docs/{id}/versions/{n}: see routeGuards' comment on this route in
// internal/api/router.go.
func docVersionURL(docID int64, version int) string {
	return "/docs/versions/" + strconv.FormatInt(docID, 10) + "/" + strconv.Itoa(version)
}

// DocEdgeRow is one typed link with its far end resolved for rendering.
// Anchor is the anchor in the document being read that the edge attaches to
// ("" for a document-level edge); Label names the other end by slug rather
// than by id, the way the corpus spells a reference ("025-documents#sec-5");
// Ref is that document's corpus reference ("spec 25"), shown as a chip so a
// plan and the spec it covers are told apart, and is "" for an unresolved
// reference. URL links to the other end and is "" for a cross-corpus
// reference this backbone cannot resolve, which is rendered as text rather
// than as a dead link.
type DocEdgeRow struct {
	Type   string
	Anchor string
	Ref    string
	Label  string
	URL    string
}

// docStatusChip returns the .chip variant class for a document status
// (025 §7's draft -> accepted -> superseded ladder).
func docStatusChip(status string) string {
	switch status {
	case "accepted":
		return "ok"
	case "superseded":
		return "plain"
	default:
		return "info"
	}
}

// --- creation forms ---------------------------------------------------------

// FormOption is one choice in a form's <select>, pre-selected when Selected.
type FormOption struct {
	Value    string
	Label    string
	Selected bool
}

// FormShell is what every project-scoped creation form needs from the shell:
// where to POST, where Cancel returns to, and the validation message from a
// rejected submit ("" on first render). The entered values live on the
// concrete view so a rejected submit re-renders what the person typed.
type FormShell struct {
	Page      PageProps
	Project   CockpitProject
	Action    string
	CancelURL string
	Error     string
	// Dictation reports whether the server has a speech-to-text provider
	// configured (WL-299); it decides whether MarkdownInput offers the
	// microphone, never whether the input works.
	Dictation bool
}

// MarkdownInputView is one MarkdownInput component (WL-299): the textarea's
// own attributes plus whether dictation is offered. Value is the draft as
// submitted, preserved across a refused form post like every other field.
type MarkdownInputView struct {
	ID          string
	Name        string
	Rows        int
	Placeholder string
	Value       string
	Dictation   bool
}

// NewTaskView is the "New task" form (POST to Form.Action), rendering the
// same fields POST /api/v1/tasks takes: the two required choices (priority,
// kind), the optional concern, and the draft switch that decides whether the
// task lands claimable.
type NewTaskView struct {
	Form       FormShell
	Title      string
	Body       string
	Priorities []FormOption
	Kinds      []FormOption
	Concerns   []FormOption
	Draft      bool
}

// NewDeliverableView is the "Declare a deliverable" form: exactly the
// descriptive fields spec 029 §3.1 gives a custom deliverable — name,
// description, URL, and the artifact address the ingest routes reports by —
// and nothing that would let a person assert its state.
type NewDeliverableView struct {
	Form        FormShell
	Name        string
	Description string
	URL         string
	Artifact    string
	// Milestones is the project's milestones as a select menu, "No
	// milestone" leading and selected by default (spec 029 §2).
	Milestones []FormOption
}

// --- presentation helpers ---------------------------------------------------

// stateChip returns the .chip variant class for a task state.
func stateChip(state string) string {
	switch state {
	case "blocked":
		return "crit"
	case "in_progress":
		return "info"
	case "in_review":
		return "warn"
	case "ready":
		return "ok"
	default:
		return "plain"
	}
}

// evidenceChip maps an evidence category ("declared", "user_reported",
// "observed", "recommended") to its .chip evidence variant class. The
// user_reported category uses the "user" chip class.
func evidenceChip(category string) string {
	switch category {
	case "user_reported":
		return "user"
	case "declared", "observed", "recommended":
		return category
	default:
		return "plain"
	}
}

// evidenceLabel returns the human display text for an evidence category,
// hyphenating user_reported ("User-reported", never "User reported").
func evidenceLabel(category string) string {
	switch category {
	case "declared":
		return "Declared"
	case "user_reported":
		return "User-reported"
	case "observed":
		return "Observed"
	case "recommended":
		return "Recommended"
	default:
		return category
	}
}

// stateLabel returns the human display text for a task state.
func stateLabel(state string) string {
	switch state {
	case "in_progress":
		return "In progress"
	case "in_review":
		return "In review"
	case "ready":
		return "Ready"
	case "blocked":
		return "Blocked"
	default:
		return state
	}
}

// modeLabel returns the human display text for a cockpit lifecycle mode. Only
// Operations is wired today; the other labels are ready for when their modes
// are stored.
func modeLabel(mode string) string {
	switch mode {
	case "operations":
		return "Operations"
	case "approved_launch":
		return "Approved launch"
	case "editorial_decision":
		return "Editorial decision"
	default:
		return mode
	}
}

// Initials returns up to two uppercase initials for a display name, for the
// avatar badges in the Active-work list and decision rail, and for
// internal/api's Home card crew mapping (assembled facts carry full names,
// never truncated ones). An empty name yields "" (a blank avatar), never a
// fabricated placeholder.
func Initials(name string) string {
	out := make([]rune, 0, 2)
	for _, field := range strings.Fields(name) {
		// Fields never yields an empty string, so there is always a first rune.
		r, _ := utf8.DecodeRuneInString(field)
		out = append(out, unicode.ToUpper(r))
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

// avatarClass returns the .who avatar wrapper class for an owner/delegate
// pair: an agent delegate holding the lease renders as ".who agent" (the AI
// badge styling), everything else as a plain ".who". Shared by
// cockpit.templ's workRow and runboard.templ's runRow.
func avatarClass(owner, delegate string) string {
	if delegate != "" {
		return "who agent"
	}
	return "who"
}

// avatarInitials returns the avatar initials for an owner/delegate pair: the
// delegated agent's when one holds the lease, otherwise the human owner's,
// otherwise "" (unassigned — a blank avatar, never invented).
func avatarInitials(owner, delegate string) string {
	if delegate != "" {
		return Initials(delegate)
	}
	return Initials(owner)
}

// actorsLine renders an owner/delegate pair's who-line: the delegated agent
// acting on behalf of the accountable owner, an agent with no recorded
// owner, a lone human owner, or an honest "Unassigned" when neither is
// known.
func actorsLine(owner, delegate string) string {
	switch {
	case delegate != "" && owner != "":
		return delegate + " · on behalf of " + owner
	case delegate != "":
		return delegate + " · delegated agent"
	case owner != "":
		return owner + " · owner"
	default:
		return "Unassigned"
	}
}

// pluralSuffix returns "s" unless n == 1 — the inbox-count pluralization.
func pluralSuffix(n int) string {
	if n != 1 {
		return "s"
	}
	return ""
}

// --- home project list -------------------------------------------------------

// HomeView is the Home project list (spec 032 §9, first slice). Mode is
// "actor" (signed-in, has cards), "open" (no actor — all projects, no role
// badge or signal), or "empty" (an actor on no projects); it also labels the
// worklode_web_home_renders_total metric, so the three values are fixed.
type HomeView struct {
	Page  PageProps
	Mode  string
	Cards []HomeCard
	Brief *MorningBriefView // nil = no section (open mode, or nothing to say)
}

// HomeCard is one project card, density B: identity, role badge ("Lead",
// "Member", or "" when the viewer has no role), the one-line signal saying
// why the card sits where it does ("" in open mode), the three-count strip,
// up to five crew initials plus an overflow count, and last activity (zero
// time = no tasks yet). The whole card links to /projects/{ProjectID}.
type HomeCard struct {
	ProjectID, Name, Key          string
	RoleBadge                     string
	Signal                        string
	InProgress, InReview, Blocked int
	CrewInitials                  []string
	CrewMore                      int
	LastActivity                  time.Time
}

// homeActivity renders a card's last-activity line, honest about absence.
func homeActivity(t time.Time) string {
	if t.IsZero() {
		return "No activity yet"
	}
	return "Last activity " + fmtTime(t)
}

// homeRoleChip returns the .chip variant class for a Home card's role badge,
// reusing the Lead affordance's existing accent styling (crew.templ) for
// "Lead" and the neutral variant for "Member".
func homeRoleChip(badge string) string {
	if badge == "Lead" {
		return "lead"
	}
	return "plain"
}

// --- CLI login (spec 001 §8.7) ----------------------------------------------

// CLICodeView is the manual-`lode login` page: the one-time code the user
// carries back to their terminal. It holds no project data and no session —
// the page is rendered mid-login, before either exists — and deliberately
// never carries the wl_ token itself, only the short-lived code that redeems
// for one.
type CLICodeView struct {
	Title   string
	ActorID string
	Code    string
	// ExpiresIn is pre-formatted for prose ("5 minutes"), like every other
	// human-facing duration ui renders.
	ExpiresIn string
}

// --- morning brief (032 §9; NOT the task brief in internal/api/brief.go) --

// MorningBriefView is the assembled Morning Brief: 032 §9's four tiers,
// grouped by project in Home's display order. Nil (not zero) means there is
// nothing to show — no tier-1 state and no events past the boundary.
type MorningBriefView struct {
	Cutoff    int64 // displayed cutoff; the hidden form value
	CanReview bool  // Cutoff advanced past the stored boundary
	Truncated bool
	Shown     int // events represented, for the truncation line
	Groups    []MorningBriefGroup
}

// MorningBriefGroup is one project's slice of the brief, in tier order.
type MorningBriefGroup struct {
	ProjectID, Name string
	FocusNote       string             // pinned focus, "" = none
	NeedsYou        []MorningBriefItem // tier 1: decisions and exceptions
	Outcomes        []MorningBriefItem // tier 2: material outcomes and changes
	Stopped         []MorningBriefItem // tier 3: stopped or reached a bound
	Routine         int                // tier 4: routine, collapsed to a count
}

// MorningBriefItem is one renderable line; Href "" renders as plain text.
type MorningBriefItem struct {
	Text string
	Href string
}

// routineLabel is the tier-4 collapsed count line's exact spelling. There is
// no per-item text to pluralize against — Routine is a count, not a list
// (032 §11: judgment obvious, no firehose).
func routineLabel(n int) string {
	if n == 1 {
		return "1 routine update"
	}
	return strconv.Itoa(n) + " routine updates"
}

// --- progress (WL-SPEC-66 §2) ------------------------------------------------

// ProgressView is a project's Progress page: the rally band, the four group
// counts, the section bar with its legend, and the §1.3 groups of spec rows.
// Everything on it is derived per request (model.ProjectProgress); the page
// stores no progress figure of its own, and shows no percentage (§2.5).
type ProgressView struct {
	Page         PageProps
	CanonicalURL string
	Project      CockpitProject
	// Viewer is the session's actor, empty for an anonymous viewer of an
	// open instance. Every act on the page is enabled or disabled against
	// it (§3); the server never derives an actor from anything the page
	// sends back (§4.2 rule 6).
	Viewer string
	P      model.ProjectProgress
	Legend []LegendEntry
	// ReviewEnabled is hasReviewSurface's answer (internal/api): whether spec
	// 059's routes are registered yet. The server decides it, not this
	// package (§3.3) — every Review button on the page renders off this one
	// bit rather than each act guessing at it.
	ReviewEnabled bool
	// MergeEnabled is whether the server has a GitHub App to act with
	// (internal/api). §3.6's button needs one, and only the server knows;
	// without it the button renders disabled with that reason rather than
	// posting a route that would answer 503.
	MergeEnabled bool
}

// LegendEntry is one section state in the bar's legend: §1.2's label, a
// one-line meaning, and how many owed sections carry it. The bound state has
// no entry — §1.4 keeps it off the page entirely.
type LegendEntry struct {
	State, Label, Help string
	Count              int
}

// progressStates is §2.1's section-bar order, which is also the legend's.
// "bound" is absent by design (§1.4).
var progressStates = []string{"built", "in_progress", "not_started", "no_record", "draft", "unplanned"}

// progressStateLabels is §1.2's "Label on the page" column, verbatim.
var progressStateLabels = map[string]string{
	"built":       "Built",
	"in_progress": "In progress",
	"not_started": "Accepted, not started",
	"no_record":   "Accepted, no execution record",
	"draft":       "Plan awaiting acceptance",
	"unplanned":   "Unplanned",
}

// progressStateHelp is the legend's one-line meaning for each state.
var progressStateHelp = map[string]string{
	"built":       "every covering plan's tasks have landed",
	"in_progress": "a covering plan has work under way",
	"not_started": "a covering plan is accepted and no task has started",
	"no_record":   "a covering plan is accepted with no minted task",
	"draft":       "every covering plan is still a draft",
	"unplanned":   "no plan covers this section",
}

// progressGroupLabels and progressGroupHelp are §1.3's group names and its
// "Why this order" column, one line each.
var progressGroupLabels = map[string]string{
	"active": "Active", "planning": "Needs planning",
	"no_record": "No execution record", "built": "Built",
}

var progressGroupHelp = map[string]string{
	"active":    "work is claimable now",
	"planning":  "a human has to plan or accept",
	"no_record": "the record is missing, not the work",
	"built":     "nothing to do",
}

func progressStateLabel(state string) string { return progressStateLabels[state] }
func progressGroupLabel(key string) string   { return progressGroupLabels[key] }
func progressGroupMeaning(key string) string { return progressGroupHelp[key] }

// ProgressLegend builds the section bar's legend from a derived progress
// model: every state in bar order with its own count, so a state the project
// has none of still explains its colour.
func ProgressLegend(p model.ProjectProgress) []LegendEntry {
	counts := make(map[string]int, len(p.Bar))
	for _, s := range p.Bar {
		counts[s.State] = s.Count
	}
	out := make([]LegendEntry, 0, len(progressStates))
	for _, st := range progressStates {
		out = append(out, LegendEntry{
			State: st, Label: progressStateLabel(st),
			Help: progressStateHelp[st], Count: counts[st],
		})
	}
	return out
}

// progressCellClass is a strip cell's class: its state colour plus the inset
// ring a partially covered section carries (§1.2).
func progressCellClass(s model.ProgressSection) string {
	c := "cell cell-" + s.State
	if s.Partial {
		c += " cell-partial"
	}
	return c
}

// progressSectionNumber renders an anchor as the section number a reader
// recognises: "sec-3.1" reads "§3.1".
func progressSectionNumber(anchor string) string {
	return "§" + strings.TrimPrefix(anchor, "sec-")
}

// progressCellTip is a strip cell's tooltip text (§2.4): the section, its
// state, and the plans covering it, in the "·" form the spec spells out
// (§3.1 Renewal · In progress · WL-PLAN-99). progress.js reads it from
// data-tip; the page carries no title attribute for it, so a reader never
// sees two tooltips for one cell.
func progressCellTip(s model.ProgressSection) string {
	t := progressSectionNumber(s.Anchor) + " " + s.Heading + " · " + progressStateLabel(s.State)
	if len(s.Plans) > 0 {
		t += " · " + strings.Join(s.Plans, ", ")
	}
	return t
}

// progressTaskTip is a task cell's tooltip text (§2.4): the task, its title,
// and its position — the furthest fact the backbone holds about it, already
// rendered as one line by internal/progress.
func progressTaskTip(t model.ProgressTask) string {
	s := t.ID + " · " + t.Title
	if t.Position != "" {
		s += " · " + t.Position
	}
	return s
}

// progressRefTip is a plan or spec reference's tooltip text (§2.4).
func progressRefTip(ref, title string) string { return ref + " · " + title }

// progressSliceTitle is a bar slice's hover text: what the colour means and
// how many owed sections it covers. A count, never a percentage (§2.5).
func progressSliceTitle(s model.ProgressSlice) string {
	return progressStateLabel(s.State) + ": " + strconv.Itoa(s.Count) + " sections"
}

// progressSliceClass is one bar slice's whole class list: the state colour it
// shares with the strips, plus the .seg-N rule that gives it its share of the
// bar. The share cannot be an inline style — the cockpit is served under
// style-src 'self' with no nonce, so the attribute would be dropped and every
// slice would come out the same width (csp_test.go holds that line) — so the
// count is turned into a whole percent here and app.tailwind.css carries one
// flex-grow rule per percent. Whole percent is the finest granularity a fixed
// class set can hold, and it is exact to about a pixel at any bar width the
// page draws. A slice with a count never rounds away to nothing.
func progressSliceClass(bar []model.ProgressSlice, i int) string {
	total := 0
	for _, s := range bar {
		total += s.Count
	}
	s := bar[i]
	pct := 0
	if total > 0 {
		pct = (s.Count*200 + total) / (total * 2) // round half up
		if pct == 0 && s.Count > 0 {
			pct = 1
		}
	}
	return "seg cell-" + s.State + " seg-" + strconv.Itoa(pct)
}

// progressDetailID is the id of a row's detail block, which the row points at
// with aria-controls so the two are one control to assistive technology.
func progressDetailID(ref string) string { return "d-" + ref }

// ProgressAction is one §3.1 action button: the route progress.js posts to
// under /projects/{id}/progress/, the JSON body it sends, and the sentence
// the confirmation step shows. A non-empty Reason renders the button
// disabled with that reason as its hover text, because a hidden button reads
// as a missing feature (§3).
type ProgressAction struct {
	Route   string
	Body    string
	Label   string
	Confirm string
	Reason  string
}

// progressPlanActions are the acts on one plan line (§2.3). Accept applies
// only to a draft plan — the document `lode doc accept` accepts (§3.2).
// Review (§3.3) is offered on every plan line, whatever its state.
func progressPlanActions(p model.ProgressPlan, viewer string, reviewEnabled bool) []ProgressAction {
	var acts []ProgressAction
	if p.State == "draft" {
		acts = append(acts, progressAcceptAction(p.Doc, p.Ref, p.Owner, viewer))
	}
	acts = append(acts, progressReviewAction(p.Doc, p.Ref, reviewEnabled))
	return acts
}

// progressSpecActions are the acts on a spec row (§2.2's action slot). A
// draft spec is accepted from here the same way a draft plan is (§3.2); an
// accepted one has nothing to accept. Plan (§3.4) joins it when the spec has
// a section no plan covers and no planning task is open — an open one is
// drawn as a link instead, because minting a second is not an act this page
// offers. Rally (§3.5) and Review (§3.3) end the row and are offered on
// every spec.
func progressSpecActions(s model.ProgressSpec, viewer string, reviewEnabled bool) []ProgressAction {
	var acts []ProgressAction
	if s.Status == "draft" {
		acts = append(acts, progressAcceptAction(s.Doc, s.Ref, s.Owner, viewer))
	}
	if s.PlanningTask == "" && progressHasUnplanned(s) {
		acts = append(acts, progressPlanAction(s.Doc, s.Ref, viewer))
	}
	acts = append(acts, progressRallyAction(s.Doc, s.Ref, viewer))
	acts = append(acts, progressReviewAction(s.Doc, s.Ref, reviewEnabled))
	return acts
}

// progressReviewAction is §3.3's Review button. Whether it may be pressed at
// all is not this page's call: hasReviewSurface (internal/api) reports
// whether spec 059's routes exist, and reviewEnabled is that answer, passed
// in rather than re-derived here (ui depends on nothing beyond stdlib and
// model). Disabled carries the reason rather than hiding the button, the
// same rule every other act on this page follows (§3).
func progressReviewAction(doc int64, ref string, reviewEnabled bool) ProgressAction {
	a := ProgressAction{
		Route:   "review",
		Body:    progressDocBody(doc),
		Label:   "Review",
		Confirm: "Request review for " + ref,
	}
	if !reviewEnabled {
		a.Reason = "Review surface (spec 059) not yet built"
	}
	return a
}

// progressRallyAction is §3.5's Rally button. Every spec row carries one,
// whatever group it is in: a spec with nothing outstanding is a no-op the
// route answers with added 0, not an act to hide. Any signed-in viewer may
// assemble a rally — a draft rally is inert until someone confirms it (005
// §9) — so having no session is the only reason it is ever disabled.
func progressRallyAction(doc int64, ref, viewer string) ProgressAction {
	a := ProgressAction{
		Route:   "rally/add",
		Body:    progressDocBody(doc),
		Label:   "Rally",
		Confirm: "Add " + ref + " to the rally",
	}
	if viewer == "" {
		a.Reason = "sign in to assemble a rally"
	}
	return a
}

// progressFooterActions are §3.5's footer controls: publish the draft rally,
// or drop it. Both are two-step buttons like every other write on this page,
// and both send an empty body — the draft rally a project has is the one they
// act on, so there is nothing to name.
func progressFooterActions(viewer string) []ProgressAction {
	confirm := ProgressAction{
		Route: "rally/confirm", Body: "{}", Label: "Confirm Rally",
		Confirm: "Publish this rally",
	}
	discard := ProgressAction{
		Route: "rally/discard", Body: "{}", Label: "Discard",
		Confirm: "Discard this rally",
	}
	if viewer == "" {
		confirm.Reason = "sign in to publish a rally"
		discard.Reason = "sign in to discard a rally"
	}
	return []ProgressAction{confirm, discard}
}

// progressHasUnplanned is §3.4's condition: a section no plan covers. It is
// the same fact the route checks before it mints, so the button and the route
// agree about when planning is owed.
func progressHasUnplanned(s model.ProgressSpec) bool {
	for _, sec := range s.Sections {
		if sec.State == "unplanned" {
			return true
		}
	}
	return false
}

// progressPlanAction is §3.4's Plan button: it mints 025 §15.4's planning
// task for the spec. Any signed-in viewer may ask for it — the task is a
// prompt to plan, not the plan — so the only reason it is ever disabled is
// having no session to act as.
func progressPlanAction(doc int64, ref, viewer string) ProgressAction {
	a := ProgressAction{
		Route:   "plan",
		Body:    progressDocBody(doc),
		Label:   "Plan",
		Confirm: "Mint a planning task for " + ref,
	}
	if viewer == "" {
		a.Reason = "sign in to mint a planning task"
	}
	return a
}

// progressAcceptAction is §3.2's Accept button for one document. It is
// enabled only for the document's owner, because that is the only actor
// store.AcceptDoc admits (025 §7) — a button this page enabled for anyone
// else would promise a write the backbone refuses. Everyone else gets it
// disabled with the reason, never hidden: a missing button reads as a
// missing feature (§3).
func progressAcceptAction(doc int64, ref, owner, viewer string) ProgressAction {
	a := ProgressAction{
		Route:   "accept",
		Body:    progressDocBody(doc),
		Label:   "Accept",
		Confirm: "Accept " + ref,
	}
	switch {
	case viewer == "":
		a.Reason = "sign in to accept"
	case owner == "":
		a.Reason = ref + " has no owner to accept it"
	case owner != viewer:
		a.Reason = ref + " is owned by " + owner
	}
	return a
}

// progressDocBody is the one-field body every document act sends (§7). It is
// built from an integer, so it is a well-formed JSON object by construction.
func progressDocBody(doc int64) string {
	return `{"doc":` + strconv.FormatInt(doc, 10) + `}`
}

// progressMergeAction is §3.6's act on a task's open pull request. What the
// repository's default branch does with a PR decides the label: a
// queue-protected branch takes it into the queue, anything else merges it.
// The page moves this button into the task cell's pinned tooltip, which is
// where §3.6 puts it.
//
// mergeEnabled is the server's answer about the GitHub App; t.Merge.Reason
// carries what the backbone can already tell (checks not passed, already
// queued). Either one renders the button disabled with that reason rather
// than hidden (§3).
func progressMergeAction(t model.ProgressTask, mergeEnabled bool) ProgressAction {
	m := t.Merge
	pr := "PR #" + strconv.FormatInt(m.Number, 10)
	a := ProgressAction{
		Route:   "merge",
		Body:    progressMergeBody(t.ID, *m),
		Label:   "Merge",
		Confirm: "Merge " + pr,
	}
	if m.Queue {
		a.Label = "Queue for merge"
		a.Confirm = "Queue " + pr + " for merge"
	}
	a.Reason = m.Reason
	if !mergeEnabled {
		a.Reason = "no GitHub App is configured"
	}
	return a
}

// progressMergeBody is the merge act's body, marshalled from the wire type
// the route decodes, so the two cannot drift apart.
func progressMergeBody(task string, m model.ProgressMerge) string {
	b, err := json.Marshal(model.ProgressMergeInput{
		Task: task,
		PR:   model.ProgressPR{Repo: m.Repo, Number: m.Number},
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}
