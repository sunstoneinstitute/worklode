package ui

// views.go defines the presentation view types the templ components render,
// plus the small presentation helpers (chip-variant mapping, pluralization)
// they call. These types are ui's own vocabulary: internal/api maps its DTOs
// (boardResponse, cockpitProjection, ...) into them in render.go, so the
// dependency only ever points api -> ui. View types may embed internal/store
// types (ui may import store) but never reference api's DTOs.

import (
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// PageProps carries the fields the Page shell needs on every page: the
// document title and which primary-nav destination to mark aria-current.
// ActiveGlobal is one of "home", "intake", "projects", "work", "reviews",
// "deliveries", "knowledge"; it is left empty on project-scoped pages, whose
// project-local nav carries the current-page marker instead. Every page sets
// aria-current="page" exactly once, never on both navs.
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

// BoardProject is one project's four state buckets on the board.
type BoardProject struct {
	ID         string
	Name       string
	InProgress []BoardItem
	InReview   []BoardItem
	Ready      []BoardItem
	Blocked    []BoardItem
}

// BoardItem is one task row on the board. Holder is the active lease holder
// (nil when none); Assignee is the owning actor id ("" when unassigned).
type BoardItem struct {
	ID       string
	Title    string
	Priority string
	State    string
	Assignee string
	Holder   *BoardHolder
}

// BoardHolder is the lease holder shown on an in-progress board row.
type BoardHolder struct {
	ActorID   string
	ExpiresAt time.Time
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

// ProjectsView is the cross-project portfolio. It embeds store.Project rows
// directly (ui may import store); the template reads ID/Name/Key.
type ProjectsView struct {
	Page     PageProps
	Projects []store.Project
}

// --- task -------------------------------------------------------------------

// TaskView is one task's detail page. Task/Holder/Progress embed store types
// directly; the edge lists are task ids.
type TaskView struct {
	Page      PageProps
	Task      store.Task
	Blocked   bool
	Holder    *store.Lease
	Blocks    []string
	BlockedBy []string
	Parent    string
	Children  []string
	Progress  store.HierarchyProgress
	Timeline  []TimelineRow
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

// CockpitView is the project cockpit page (project overview). It mirrors the
// shape of api's cockpitProjection, flattened for rendering.
type CockpitView struct {
	Page              PageProps
	CanonicalURL      string
	Project           CockpitProject
	ModeName          string
	ModeBasis         string
	RankingFocus      []string
	NextDecision      *CockpitDecision
	Work              CockpitWork
	SecondaryConcerns []CockpitConcern
	Repositories      []CockpitRepo
	CostTotals        []CockpitCostTotal
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

// CockpitConcern is one secondary concern (an open blocker) on the cockpit.
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
	case "declared":
		return "declared"
	case "user_reported":
		return "user"
	case "observed":
		return "observed"
	case "recommended":
		return "recommended"
	default:
		return "plain"
	}
}

// pluralSuffix returns "s" unless n == 1 — the inbox-count pluralization.
func pluralSuffix(n int) string {
	if n != 1 {
		return "s"
	}
	return ""
}

// rankingFocusText renders a project's ranking focus for display:
// space-separated with a trailing space, or the literal "none" when empty.
func rankingFocusText(focus []string) string {
	if len(focus) == 0 {
		return "none"
	}
	return strings.Join(focus, " ") + " "
}
