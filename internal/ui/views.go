package ui

// views.go defines the presentation view types the templ components render,
// plus the small presentation helpers (chip-variant mapping, pluralization)
// they call. These types are ui's own vocabulary: internal/api maps
// internal/model values (model.BoardResponse, model.CockpitProjection, ...)
// into them in render.go, so the dependency only ever points api -> ui. View
// types may embed internal/model types (ui may import model) but never
// reference api's DTOs (ADR 036 §3).

import (
	"html/template"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// PageProps carries the fields the Page shell needs on every page: the
// document title and which primary-nav destination to mark aria-current.
// ActiveGlobal drives globalShell's sidebar marking: one of "home", "intake",
// "projects", "work", "reviews", "deliveries", "knowledge". It is left empty
// on project-scoped pages, whose project-local nav carries the current-page
// marker instead, and on pages with no current destination (the task page),
// which mark nothing. Every page sets aria-current="page" exactly once,
// never on both navs.
type PageProps struct {
	Title        string
	ActiveGlobal string
}

// --- board (Home / Work) ----------------------------------------------------

// BoardView is the org-wide board shared by Home ("/") and Work ("/work"):
// same data, only the heading and ActiveGlobal differ (IsHome switches the
// heading).
type BoardView struct {
	Page           PageProps
	IsHome         bool
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

// --- projects portfolio -----------------------------------------------------

// ProjectsView is the cross-project portfolio. It embeds model.Project rows
// directly; the template reads ID/Name/Key.
type ProjectsView struct {
	Page     PageProps
	Projects []model.Project
}

// --- task -------------------------------------------------------------------

// TaskView is one task's detail page. Task/Holder/Progress/Attachments embed
// internal/model types directly; the edge lists are task ids.
type TaskView struct {
	Page PageProps
	Task model.Task
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
	Progress    model.TaskProgress
	Timeline    []TimelineRow
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
	URL     string
}

// --- placeholder ------------------------------------------------------------

// PlaceholderView is an honest "not built yet" page for a global or
// project-scoped destination whose governing spec section is not implemented.
// Project is nil for a global destination (Intake, Reviews, Deliveries,
// Knowledge) and set for a project section (Crew, Deliverables, Reviews,
// Decisions, Documents, Activity), which renders the same project-local
// navigation and header as the project overview page.
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
// prototype's Operations mode (docs/mockups/cockpit/index.html, mode B). It
// mirrors the shape of model.CockpitProjection, flattened for rendering.
// PinnedFocus, NextDecision, and RankingFocus stay nil/empty until the stores
// that back them exist; each panel is omitted honestly when its data is
// absent rather than rendering an invented placeholder.
type CockpitView struct {
	Page              PageProps
	CanonicalURL      string
	NewTaskURL        string
	Project           CockpitProject
	ModeName          string
	ModeBasis         string
	PinnedFocus       *CockpitFocus
	RankingFocus      []string
	NextDecision      *CockpitDecision
	Work              CockpitWork
	SecondaryConcerns []CockpitConcern
	Repositories      []CockpitRepo
	CostTotals        []CockpitCostTotal
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
type CockpitProject struct {
	ID   string
	Name string
	Key  string
}

// CockpitWork holds the cockpit's four work buckets.
type CockpitWork struct {
	InProgress []WorkRow
	InReview   []WorkRow
	Ready      []WorkRow
	Blocked    []WorkRow
}

// Len is the total number of active work items across all four buckets — the
// Active-work list renders an honest "no active work" line when it is zero.
func (w CockpitWork) Len() int {
	return len(w.InProgress) + len(w.InReview) + len(w.Ready) + len(w.Blocked)
}

// Rows is every active work item in the order the Active-work list renders
// them: in progress, in review, ready, blocked. The bucket order is a fact of
// the view, not of the markup, so the template walks one sequence.
func (w CockpitWork) Rows() []WorkRow {
	rows := make([]WorkRow, 0, w.Len())
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

// CockpitRepo is one mapped repository and its declared done-state evidence.
type CockpitRepo struct {
	Repo             string
	DoneState        string
	EvidenceCategory string
}

// CockpitCostTotal is a per-currency cost total for the cockpit's cost window.
type CockpitCostTotal struct {
	Currency       string
	CostAmount     string
	UnpricedTokens int64
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
// deliverable" form; an empty Deliverables slice renders an honest empty
// state next to that form, never a fabricated row.
type DeliverablesView struct {
	Page         PageProps
	CanonicalURL string
	Project      CockpitProject
	NewURL       string
	Deliverables []DeliverableRow
}

// DeliverableRow is one declared deliverable. It carries no state: spec 029
// §3.2 makes deliverable state a fact emitters and probers report, and no
// such report exists yet, so the page says the state is unreported rather
// than showing one.
type DeliverableRow struct {
	ID          string
	Name        string
	Description string
	URL         string
	CreatedBy   string
	CreatedAt   time.Time
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

// --- documents ---------------------------------------------------------------

// DocsView is the document corpus index (GET /docs): every spec, ADR and plan
// the backbone holds (025 §5). Read-only — a document's body is an artifact
// authored in a file and submitted through the API, not typed into a page.
type DocsView struct {
	Page PageProps
	Docs []DocRow
}

// DocRow is one document in the index: the stored row, its page URL, and the
// corpus reference pre-formatted for display ("spec 25"; a plan carries no
// number, so its reference is its kind alone).
type DocRow struct {
	Doc model.Doc
	URL string
	Ref string
}

// DocView is one document's page (GET /docs/{id}): the stored row, its
// sections with their accept-time state, its edges in both directions, and
// the open candidate revision when one exists (nil otherwise).
type DocView struct {
	Page     PageProps
	Doc      model.Doc
	Ref      string
	Sections []model.DocSection
	Edges    []DocEdgeRow
	EdgesIn  []DocEdgeRow
	Revision *model.DocRevision
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

// DocStatusChip returns the .chip variant class for a document status
// (025 §7's draft -> accepted -> superseded ladder).
func DocStatusChip(status string) string {
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

// NewDeliverableView is the "Declare a deliverable" form: exactly the three
// descriptive fields spec 029 §3.1 gives a custom deliverable, and nothing
// that would let a person assert its state.
type NewDeliverableView struct {
	Form        FormShell
	Name        string
	Description string
	URL         string
}

// --- presentation helpers ---------------------------------------------------

// StateChip returns the .chip variant class for a task state.
func StateChip(state string) string {
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

// PriorityChip returns the .chip variant class for a task priority.
func PriorityChip(priority string) string {
	switch priority {
	case "critical", "urgent":
		return "crit"
	case "high":
		return "warn"
	case "medium":
		return "info"
	default:
		return "plain"
	}
}

// EvidenceChip maps an evidence category ("declared", "user_reported",
// "observed", "recommended") to its .chip evidence variant class. The
// user_reported category uses the "user" chip class.
func EvidenceChip(category string) string {
	switch category {
	case "user_reported":
		return "user"
	case "declared", "observed", "recommended":
		return category
	default:
		return "plain"
	}
}

// EvidenceLabel returns the human display text for an evidence category,
// hyphenating user_reported ("User-reported", never "User reported").
func EvidenceLabel(category string) string {
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

// StateLabel returns the human display text for a task state.
func StateLabel(state string) string {
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

// ModeLabel returns the human display text for a cockpit lifecycle mode. Only
// Operations is wired today; the other labels are ready for when their modes
// are stored.
func ModeLabel(mode string) string {
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
// avatar badges in the Active-work list and decision rail. An empty name
// yields "" (a blank avatar), never a fabricated placeholder.
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

// WorkRowWhoClass returns the .who avatar wrapper class for a work row: an
// agent delegate holding the lease renders as ".who agent" (the AI badge
// styling), everything else as a plain ".who".
func WorkRowWhoClass(item WorkRow) string {
	if item.Delegate != "" {
		return "who agent"
	}
	return "who"
}

// WorkRowInitials returns the avatar initials for a work row: the delegated
// agent's when one holds the lease, otherwise the human owner's, otherwise ""
// (unassigned — a blank avatar, never invented).
func WorkRowInitials(item WorkRow) string {
	if item.Delegate != "" {
		return Initials(item.Delegate)
	}
	return Initials(item.Owner)
}

// WorkRowActors renders a work row's who-line: the delegated agent acting on
// behalf of the accountable owner, an agent with no recorded owner, a lone
// human owner, or an honest "Unassigned" when neither is known.
func WorkRowActors(item WorkRow) string {
	switch {
	case item.Delegate != "" && item.Owner != "":
		return item.Delegate + " · on behalf of " + item.Owner
	case item.Delegate != "":
		return item.Delegate + " · delegated agent"
	case item.Owner != "":
		return item.Owner + " · owner"
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
