package ui

// narrow_test.go holds the narrow-width accessibility properties the WL-140
// audit established, measured in a headless browser at 320, 375 and 768 CSS px
// and at 200% zoom on a 1280px desktop (which reflows to the same 640px path).
//
// The audit needed a browser; CI has no business installing one. What a Go
// test can hold is the small set of markup and stylesheet facts each finding
// was fixed by, so that removing one fails here — with the WCAG criterion
// named — instead of silently taking the phone layout back to where the audit
// found it. Each case says which criterion it is standing in for.

import (
	"context"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// pages renders every page component this package exports through the app
// shell, with data long enough to be worth measuring.
//
// The length is the point. These fixtures are what narrowbrowser_test.go's
// browser audit measures, and every finding the WL-140 audit made came from
// content that was longer than its box: a task title that filled a row, an
// unbreakable identifier in a body, a deliverable's URL, a timeline summary in
// a table. A fixture reading Title: "t" reflows perfectly at 320px and proves
// nothing about the page. So each page below gets a realistic worst case —
// long titles, an unbroken token, a full URL, more than one row — and a new
// page added here becomes measured by the audit at no further cost.
func pages(t *testing.T) map[string]string {
	t.Helper()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	proj := CockpitProject{ID: "worklode", Name: "Worklode backbone", Key: "WL"}
	// An unbreakable identifier: no spaces or hyphens for a browser to break
	// on, which is what turns a narrow column into a horizontal scrollbar.
	token := "bigquery://sunstone_prod.casualty_reconciliation.daily_partitioned_snapshot_v3"
	longTitle := "Make the narrow-width reflow check runnable, so the WCAG fixes are measured and not just asserted"
	// A title carrying an identifier is the ordinary case a narrow column has
	// to survive, and the identifier is the half a browser cannot break.
	tokenTitle := "Reconcile " + token + " against the source extract"
	comps := map[string]templ.Component{
		"board": Board(BoardView{
			Page:       PageProps{Title: "Work", ActiveGlobal: "work"},
			InboxCount: 7,
			Projects: []BoardProject{{
				ID: "worklode", Name: "Worklode backbone",
				InProgress: []model.BoardTask{{
					Task:   model.Task{ID: "WL-234", Title: longTitle, Priority: "low", State: "in_progress", Assignee: "stig@sunstoneinstitute.ai"},
					Holder: &model.Holder{ActorID: "claude-worker-01", ExpiresAt: now},
				}},
				InReview: []model.BoardTask{{Task: model.Task{ID: "WL-233", Title: "Cover the WCAG criteria the narrow-width audit left out", Priority: "medium", State: "in_review"}}},
				Ready:    []model.BoardTask{{Task: model.Task{ID: "WL-140", Title: "Fix what the narrow-width WCAG audit found", Priority: "high", State: "ready"}}},
				Blocked:  []model.BoardTask{{Task: model.Task{ID: "WL-141", Title: tokenTitle, Priority: "high", State: "blocked"}}},
			}},
			RecentFailures: []BoardFailure{{
				OccurredAt: now, Cluster: "admin-hel01", Kind: "CrashLoopBackOff",
				Workload: "deployment/worklode-server",
				Message:  "back-off 5m0s restarting failed container=lode pod=worklode-server-7d9f8c6b4d-x2ktp",
			}},
		}),
		"cockpit": Cockpit(CockpitView{
			Page: PageProps{Title: "Worklode backbone"}, Project: CockpitProject{
				ID: proj.ID, Name: proj.Name, Key: proj.Key,
				ModeName:  "operations",
				ModeBasis: "the project has active work and no pending launch decision",
			},
			PinnedFocus:  &CockpitFocus{Note: "Land the cockpit accessibility work before the sandbox demo", PinnedBy: "Stig Bakken", PinnedAt: now},
			NextDecision: &CockpitDecision{Title: "Whether the cockpit ships read-only for the first release", Accountable: "Stig Bakken", Readiness: "awaiting evidence"},
			Work: CockpitWork{
				InProgress: []WorkRow{{
					ID: "WL-234", Title: longTitle, State: "in_progress", Priority: "low", URL: "/tasks/WL-234",
					Owner: "Stig Bakken", Delegate: "claude-worker-01",
					EvidenceCategory: "observed", EvidenceSummary: "a lease has been held for 41 minutes with two commits on the task branch",
				}},
				Ready: []WorkRow{{ID: "WL-141", Title: tokenTitle, State: "ready", Priority: "high", URL: "/tasks/WL-141", Owner: "Stig Bakken", EvidenceCategory: "declared", EvidenceSummary: "declared ready by its author"}},
			},
			SecondaryConcerns: []CockpitConcern{{Title: "WL-140 blocks three ready tasks", URL: "/tasks/WL-140", EvidenceSummary: "an open blocker task holds the frontier"}},
			CostTotals:        []CockpitCostTotal{{Currency: "USD", CostAmount: "1284.55", UnpricedTokens: 92311}},
			AgentSessions: []AgentSessionRow{
				{
					Agent: "claude-code", AgentVersion: "2.1.231", ActorID: "claude-worker-01",
					Task: "WL-234", TaskTitle: longTitle, TaskURL: "/tasks/WL-234",
					Started: "3h ago", LastSeen: "2m ago", Running: true,
				},
				{
					Agent: "codex", AgentVersion: "0.52.0-alpha.20260814",
					ActorID: "stig@sunstoneinstitute.ai",
					Task:    "WL-141", TaskTitle: tokenTitle, TaskURL: "/tasks/WL-141",
					Started: "26h ago", LastSeen: "just now", Running: true,
				},
			},
		}),
		"task": Task(TaskView{
			Page:    PageProps{Title: "WL-234"},
			Project: proj,
			Task: model.Task{
				ID: "WL-234", Project: "worklode", Title: longTitle, Priority: "low", Kind: "chore",
				State: "in_progress", Concern: "usability", CreatedBy: "stig", Assignee: "stig@sunstoneinstitute.ai",
				CreatedAt: now, UpdatedAt: now, Branch: "WL-234-make-the-narrow-width-reflow-check-runna",
			},
			// The stored body is markdown rendered elsewhere; what matters here is
			// that it carries an unbreakable token and a <pre> nobody can re-wrap.
			BodyHTML: template.HTML(`<p>The audit measured ` + token + ` and reported:</p>` +
				`<pre><code>go test -trimpath -tags narrowcheck -run TestNarrowWidthAudit ./internal/ui/</code></pre>`),
			Blocked:   true,
			Holder:    &model.Lease{TaskID: "WL-234", ActorID: "claude-worker-01", Worktree: "hel01:/home/stig/git/worklode/.worktrees/WL-234-make-the-narrow-width-reflow-check-runna", ExpiresAt: now},
			Blocks:    []string{"WL-235", "WL-236"},
			BlockedBy: []string{"WL-140"},
			Children:  []string{"WL-237"},
			Progress:  model.TaskProgress{Closed: 1, Total: 3},
			Timeline: []TimelineRow{
				{At: now, Type: "pr", Label: "Pull request", Summary: "#242 Make the narrow-width reflow check runnable — merged by stig", URL: "https://github.com/sunstoneinstitute/worklode/pull/242"},
				{At: now, Type: "ci", Label: "Check", Summary: "pr-checks / test (pull_request) succeeded in 4m12s", URL: "https://github.com/sunstoneinstitute/worklode/actions/runs/1234567890"},
				{At: now, Type: "lease", Label: "Lease", Summary: "claimed by claude-worker-01 in .worktrees/WL-234-make-the-narrow-width-reflow-check-runna"},
			},
			AgentSessions: []AgentSessionRow{
				{Agent: "claude-code", AgentVersion: "2.1.231", ActorID: "claude-worker-01", Started: "3h ago", LastSeen: "2m ago", Running: true},
				{Agent: "codex", AgentVersion: "0.52.0-alpha.20260814", ActorID: "stig@sunstoneinstitute.ai", Started: "2d ago", LastSeen: "1d ago"},
			},
		}),
		"docs": Docs(DocsView{
			Page: PageProps{Title: "Knowledge", ActiveGlobal: "knowledge"},
			Docs: []DocRow{
				{Doc: model.Doc{ID: 32, Kind: "spec", Number: 32, Slug: "032-project-cockpit", Title: "Project cockpit", Status: "draft", CreatedBy: "stig", UpdatedAt: now}, URL: "/docs/32", Ref: "spec 32"},
				{Doc: model.Doc{ID: 41, Kind: "plan", Slug: "032-project-cockpit-part-3-accessibility", Title: "Project cockpit, part 3: accessibility and responsive behaviour", Status: "accepted", CreatedBy: "stig", UpdatedAt: now}, URL: "/docs/41", Ref: "plan"},
			},
		}),
		"doc": Doc(DocView{
			Page: PageProps{Title: "Project cockpit"},
			Doc:  model.Doc{ID: 32, Project: "worklode", Kind: "spec", Number: 32, Slug: "032-project-cockpit", Title: "Project cockpit", Status: "draft", CreatedBy: "stig", CreatedAt: now, UpdatedAt: now},
			Ref:  "spec 32",
			BodyHTML: template.HTML(`<h2 id="sec-10">10. Accessibility and responsive behavior</h2>` +
				`<p>Measured against ` + token + `.</p><pre><code>./scripts/narrow-check.sh</code></pre>`),
			Sections: []model.DocSection{
				{Anchor: "sec-10", Number: "10", Heading: "10. Accessibility and responsive behavior", Depth: 2, Position: 10, LastRevisedIn: 3, Published: true},
				{Anchor: "sec-12", Number: "12", Heading: "12. Cockpit rendering and styling toolchain", Depth: 2, Position: 12, LastRevisedIn: 1, Published: true},
			},
			Edges:   []DocEdgeRow{{Type: "covers", Anchor: "sec-10", Ref: "plan", Label: "032-project-cockpit-part-3-accessibility#sec-2", URL: "/docs/41"}},
			EdgesIn: []DocEdgeRow{{Type: "amends", Ref: "spec 29", Label: "029-research-work-in-the-backbone#sec-7", URL: "/docs/29"}},
		}),
		"projects": Projects(ProjectsView{
			Page: PageProps{Title: "Projects", ActiveGlobal: "projects"},
			Projects: []model.Project{
				{ID: "worklode", Name: "Worklode backbone", Key: "WL"},
				{ID: "casualty-reconciliation", Name: "Casualty reconciliation data platform", Key: "CRD"},
			},
		}),
		// GraphEnabled, so the two drift tables and the gap table render
		// alongside the frontier and critical-path ones.
		"drift": Drift(DriftView{
			Page: PageProps{Title: "Drift", ActiveGlobal: "knowledge"},
			Frontier: []model.FrontierTask{
				{ID: "WL-234", Title: longTitle, Project: "worklode", Priority: "low", Concern: "usability", FanOut: 2, Depth: 1, IsCritical: true},
				{ID: "WL-141", Title: tokenTitle, Project: "worklode", Priority: "high", Concern: "reliability", FanOut: 5, Depth: 3},
			},
			CriticalPath: model.CriticalPath{
				MaxDepth: 3,
				Tasks:    []model.FrontierTask{{ID: "WL-234", Title: longTitle, Depth: 1, FanOut: 2, IsCritical: true}},
				Cycles:   [][]string{{"WL-301", "WL-302", "WL-303"}},
			},
			Drift: model.Drift{
				Violations:  []model.DriftEdge{{From: "worklode/internal/ui", To: "worklode/internal/api"}},
				StaleIntent: []model.DriftEdge{{From: "casualty-reconciliation/ingest", To: "casualty-reconciliation/warehouse"}},
			},
			Gaps:         []model.Gap{{Component: "worklode/internal/skillstore"}, {Repo: "casualty-reconciliation", Path: "pipelines/daily/reconcile_partitions.py"}},
			GraphEnabled: true,
		}),
		"home": Home(HomeView{
			Page: PageProps{Title: "Home"},
			Mode: "actor",
			Cards: []HomeCard{
				{ProjectID: "worklode", Name: "Worklode backbone", Key: "WL", RoleBadge: "Lead", Signal: "Three tasks are blocked on a decision only you can make", InProgress: 2, InReview: 1, Blocked: 1, CrewInitials: []string{"SB", "JD", "AK", "MP", "TL"}, CrewMore: 3, LastActivity: now},
				{ProjectID: "casualty-reconciliation", Name: "Casualty reconciliation data platform", Key: "CRD", RoleBadge: "Member", Signal: "You are on this project", CrewInitials: []string{"AB"}},
			},
		}),
		"deliverables": Deliverables(DeliverablesView{
			Page: PageProps{Title: "Deliverables"}, Project: proj, NewURL: "/projects/worklode/deliverables/new",
			Groups: []DeliverableGroup{{Rows: []DeliverableRow{{
				ID: "DL-4", Name: "Daily casualty reconciliation snapshot",
				Description: "The partitioned daily snapshot the newsroom queries, republished whenever an upstream correction lands",
				URL:         "https://console.cloud.google.com/bigquery?project=sunstone-prod&ws=!1m5!1m4!4m3!1ssunstone-prod!2scasualty_reconciliation",
				CreatedBy:   "stig", CreatedAt: now, Artifact: token,
				ReportedState: "published", ReportedAt: &now,
			}}}},
		}),
		// Two sections: one holding a long task title, an unbreakable
		// artifact address and a deliverable, and one holding nothing, so
		// both branches of milestoneSection are measured.
		"milestones": Milestones(MilestonesView{
			Page: PageProps{Title: "Milestones"}, Project: proj,
			CanonicalURL: "/projects/worklode/milestones",
			Milestones: []MilestoneSection{{
				ID: "WL-MILE-1", Title: "Internal review of the casualty reconciliation methodology",
				TasksTotal: 3, TasksClosed: 1, DeliverablesTotal: 2, DeliverablesLive: 1,
				Tasks: []MilestoneTaskRow{
					{ID: "WL-234", Title: longTitle, State: "in_progress", Assignee: "stig@sunstoneinstitute.ai"},
					{ID: "WL-141", Title: tokenTitle, State: "ready"},
				},
				Deliverables: []DeliverableRow{{
					ID: "WL-DEL-4", Name: "Daily casualty reconciliation snapshot",
					Description: "The partitioned daily snapshot the newsroom queries, republished whenever an upstream correction lands",
					CreatedBy:   "stig", CreatedAt: now, Artifact: token,
					ReportedState: "published", ReportedAt: &now,
				}},
			}, {
				ID: "WL-MILE-2", Title: "Publication and the editorial evaluation that gates it",
			}},
		}),
		"crew": Crew(CrewView{
			Page: PageProps{Title: "Crew"}, Project: proj,
			AddAction: "/projects/worklode/crew", RemoveAction: "/projects/worklode/crew/remove",
			Roles: []FormOption{{Value: "member", Label: "Member", Selected: true}, {Value: "reviewer", Label: "Reviewer"}},
			Members: []CrewMember{
				{ActorID: "stig@sunstoneinstitute.ai", DisplayName: "Stig Bakken", Roles: []string{"lead", "reviewer"}, IsLead: true},
				{ActorID: "claude-worker-01", DisplayName: "claude-worker-01", Roles: []string{"member"}},
			},
			RemoveError:      "claude-worker-01 still owns open work; reassign or close it first",
			Responsibilities: []CrewWorkItem{{Kind: "task", ID: "WL-234", Title: longTitle, State: "in_progress"}},
		}),
		// One tombstone with a justification and one without, so both
		// branches of tombstoneNote are measured.
		"deleted": Deleted(DeletedView{
			Page: PageProps{Title: "Deleted"}, Project: proj,
			CanonicalURL:      "/projects/worklode/deleted",
			RestoreTaskAction: "/projects/worklode/deleted/tasks/restore",
			RestoreDocAction:  "/projects/worklode/deleted/docs/restore",
			RestoreError:      "WL-234 is not deleted. It may already have been restored.",
			Tasks: []model.Task{{
				ID: "WL-234", Title: longTitle, Kind: "feature", Priority: "low", State: "ready",
				Tombstone: &model.Tombstone{
					DeletedAt: now, DeletedBy: "stig@sunstoneinstitute.ai",
					Justification: "Filed twice by the importer against " + token + ", and this is the copy nobody worked.",
				},
			}},
			Docs: []DeletedDocRow{{
				Doc: model.Doc{
					ID: 44, Kind: "spec", Slug: "044-deleting-tasks-and-documents",
					Title: longTitle, Status: "draft",
					Tombstone: &model.Tombstone{DeletedAt: now, DeletedBy: "claude-worker-01"},
				},
				URL: "/docs/44", Ref: "spec 44",
			}},
		}),
		"approvals": Approvals(ApprovalsView{
			Page: PageProps{Title: "Reviews"},
			Rows: []ApprovalRow{{
				ID: 12, Kind: "PR", EntityID: "sunstoneinstitute/worklode#242",
				Title:  "Make the narrow-width reflow check runnable, so the WCAG fixes are measured",
				URL:    "https://github.com/sunstoneinstitute/worklode/pull/242",
				TaskID: "WL-234", ProjectID: "worklode", ProjectName: "Worklode backbone",
				RequiredActorName: "Stig Bakken", Age: "3h ago",
			}, {
				ID: 13, Kind: "Document", EntityID: "doc:44",
				Title: longTitle, URL: "/docs/44", Revision: "7",
				ProjectID: "worklode", ProjectName: "Worklode backbone",
				RequiredActorName: "Stig Bakken", Age: "2d ago",
			}},
		}),
		"newtask": NewTask(NewTaskView{
			Form:  FormShell{Page: PageProps{Title: "New task"}, Project: proj, Action: "/projects/worklode/tasks/new", CancelURL: "/projects/worklode", Error: "A task needs a title before it can be created"},
			Title: longTitle, Body: "The audit measured " + token + " and reported nothing.",
			Priorities: []FormOption{{Value: "high", Label: "High"}, {Value: "low", Label: "Low", Selected: true}},
			Kinds:      []FormOption{{Value: "chore", Label: "Chore", Selected: true}, {Value: "feature", Label: "Feature"}},
			Concerns:   []FormOption{{Value: "usability", Label: "Usability", Selected: true}},
			Draft:      true,
		}),
		"newdeliverable": NewDeliverable(NewDeliverableView{
			Form: FormShell{Page: PageProps{Title: "Declare a deliverable"}, Project: proj, Action: "/projects/worklode/deliverables/new", CancelURL: "/projects/worklode/deliverables"},
			Name: "Daily casualty reconciliation snapshot", Artifact: token,
			URL: "https://console.cloud.google.com/bigquery?project=sunstone-prod",
		}),
		"placeholder": Placeholder(PlaceholderView{
			Page: PageProps{Title: "Decisions"}, Heading: "Decisions", Project: &proj, ActiveSection: "decisions",
			Message: "Governed decisions are not stored in the backbone yet, so this page would have nothing honest to show.",
		}),
	}
	out := make(map[string]string, len(comps))
	for name, comp := range comps {
		var b strings.Builder
		if err := comp.Render(context.Background(), &b); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		out[name] = b.String()
	}
	return out
}

// TestEveryTableScrollsInsideItsOwnContainer holds WCAG 1.4.10 Reflow. A data
// table is the criterion's own exception — it needs two dimensions — so it may
// scroll sideways, but only inside its own container: an unwrapped table made
// the whole page scroll horizontally at 320px (the board's recent failures
// went to 437px, the task timeline to 507px, the document index to 333px).
func TestEveryTableScrollsInsideItsOwnContainer(t *testing.T) {
	seen := 0
	for name, body := range pages(t) {
		for i := 0; i < len(body); {
			at := strings.Index(body[i:], "<table")
			if at < 0 {
				break
			}
			at += i
			i = at + len("<table")
			seen++
			open := strings.LastIndex(body[:at], `class="tablewrap"`)
			if open < 0 {
				t.Errorf("%s: a <table> renders outside a .tablewrap scroll container (WCAG 1.4.10)", name)
				continue
			}
			if strings.Contains(body[open:at], "</div>") {
				t.Errorf("%s: a <table> renders after its .tablewrap container closed (WCAG 1.4.10)", name)
			}
		}
	}
	// The fixtures above exist to make the table-bearing regions render; if
	// none did, this test proved nothing.
	if seen < 4 {
		t.Errorf("expected the fixtures to render the package's four tables, saw %d", seen)
	}
}

// TestScrollContainersAreKeyboardReachable holds WCAG 2.1.1. A container that
// scrolls but holds no focusable child is unreachable by keyboard in browsers
// that do not focus scroll containers themselves, so both of ours carry
// tabindex="0" and a label naming the region it becomes.
func TestScrollContainersAreKeyboardReachable(t *testing.T) {
	rendered := pages(t)
	for name, body := range rendered {
		for _, want := range []string{`<div class="tablewrap" role="region"`, `aria-label=`} {
			if strings.Contains(body, `class="tablewrap"`) && !strings.Contains(body, want) {
				t.Errorf("%s: tablewrap is missing %s (WCAG 2.1.1)", name, want)
			}
		}
		if strings.Contains(body, `class="tablewrap"`) && !strings.Contains(body, `tabindex="0"`) {
			t.Errorf("%s: tablewrap is not focusable, so its overflow cannot be scrolled by keyboard (WCAG 2.1.1)", name)
		}
	}
	// The stage stepper scrolls inside itself too, and holds no link at all.
	if !strings.Contains(rendered["cockpit"], `<div class="stepper" role="group" aria-label="Sunstone Way stages" tabindex="0">`) {
		t.Error("cockpit: the stage stepper scrolls horizontally and must stay keyboard-focusable (WCAG 2.1.1)")
	}
}

// TestModePillRendersInSidebar pins where the operating-mode pill lives and how
// its basis is disclosed: in the project sidebar under the key, with the basis
// as a title tooltip plus aria-label rather than a permanently visible line.
// Pages that set no mode render no pill.
func TestModePillRendersInSidebar(t *testing.T) {
	rendered := pages(t)
	head, _, ok := strings.Cut(rendered["cockpit"], `<main id="main-content"`)
	if !ok {
		t.Fatal("cockpit: no main landmark to split the sidebar on")
	}
	if !strings.Contains(head, `class="mode-name ok"`) {
		t.Error("cockpit: the mode pill must render in the sidebar, before the main landmark")
	}
	basis := "the project has active work and no pending launch decision"
	if !strings.Contains(head, `title="`+basis+`"`) {
		t.Error("cockpit: the mode basis must be the pill's title tooltip, not visible body text")
	}
	if !strings.Contains(head, `aria-label="Operations mode: `+basis+`"`) {
		t.Error("cockpit: the mode basis must also reach screen readers via aria-label")
	}
	if strings.Contains(rendered["cockpit"], `class="mode-banner"`) {
		t.Error("cockpit: the mode banner was replaced by the sidebar pill and must not come back")
	}
	if strings.Contains(rendered["crew"], `class="mode-name`) {
		t.Error("crew: a page that sets no mode must render no mode pill")
	}
}

// TestMainLandmarkIsFocusable holds WCAG 2.4.1. "Skip to content" must move
// focus, not just the scroll position: a <main> with no tabindex takes the
// scroll but leaves focus on the skip link, so the next Tab returns to the
// topbar the link was there to skip.
func TestMainLandmarkIsFocusable(t *testing.T) {
	for name, body := range pages(t) {
		if !strings.Contains(body, `<main id="main-content" class="main" tabindex="-1">`) {
			t.Errorf("%s: the skip link's target must be focusable (WCAG 2.4.1)", name)
		}
	}
}

// TestStylesheetKeepsTheNarrowWidthRules holds the findings that were fixed in
// CSS rather than in markup. It reads the built stylesheet, not the Tailwind
// source, because the built one is what ships; whitespace is stripped so the
// generator's formatting is not part of the contract.
func TestStylesheetKeepsTheNarrowWidthRules(t *testing.T) {
	css, err := Assets().Open("app.css")
	if err != nil {
		t.Fatalf("open app.css: %v", err)
	}
	defer css.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := css.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	flat := strings.Join(strings.Fields(b.String()), "")

	for _, c := range []struct{ want, why string }{
		{"scroll-padding-top:64px", "an in-page jump must clear the sticky top bar (WCAG 2.4.11)"},
		{".tablewrap{overflow-x:auto", "a data table scrolls inside its own container (WCAG 1.4.10)"},
		{".tablewrapth,.tablewraptd{padding:8px12px", "data-table cells need readable separation"},
		{"pre{overflow-x:auto", "a stored document body cannot be re-wrapped, so it scrolls inside itself (WCAG 1.4.10)"},
		{".prose{overflow-wrap:anywhere", "an unbroken token in a task body must not widen the page (WCAG 1.4.10)"},
		{".wlrow.tl.t{white-space:normal", "a work row's title wraps below 880px instead of truncating to nothing (WCAG 1.4.10)"},
		{".wlrow.tl.t{white-space:normal;overflow:visible;text-overflow:clip;overflow-wrap:anywhere", "a work-row title holding an unbreakable identifier must break rather than widen the page (WCAG 1.4.10)"},
		{".dodrow.def{color:var(--ink-3);font-size:12px;margin-top:2px;overflow-wrap:anywhere", "a deliverable's artifact address has no soft wrap opportunity in it (WCAG 1.4.10)"},
		{"nav.global{align-self:stretch;display:flex;align-items:stretch", "the global destinations live inside the sticky top bar, filling its height (spec 056 §1)"},
		{".topbar{height:auto;flex-wrap:wrap;gap:012px", "below 880px the top bar wraps so the destinations keep a row of their own, with no seam between the two"},
		{"html{scroll-padding-top:112px;}", "the wrapped two-row top bar is 105px tall below 880px, and an in-page jump must clear it (WCAG 2.4.11)"},
		{"nav.global{order:3;flex:00100%", "the wrapped destinations row spans the whole bar below 880px"},
		{"nav.globala.tab-secondary{display:none;order:2;flex:00100%", "narrow layouts move secondary destinations under More"},
		{".tab-more{display:flex;cursor:pointer", "narrow layouts expose the hidden destinations through More"},
		{".dodrow.eva{display:flex;align-items:center;min-height:24px", "a deliverable's URL is a link on its own line, so it needs a 24px box (WCAG 2.5.8)"},
		{".fieldrow.checkinput{width:24px;height:24px", "the draft checkbox meets the minimum target size (WCAG 2.5.8)"},
		{".homegrid{display:grid;grid-template-columns:1fr1fr", "Home's two-column grid must stay fixed, never auto-fit/auto-fill (spec 032 §10)"},
		{"@media(max-width:820px){.homegrid{grid-template-columns:1fr;}}", "Home's grid must collapse to one column below 820px (spec 032 §10)"},
		{".proj-name{font-size:19px;line-height:1.2;margin:8px010px;min-width:0;overflow-wrap:anywhere", "an unbroken project name must not overrun the 236px sidebar at desktop widths, not just below 880px (WL-90)"},
	} {
		if !strings.Contains(flat, c.want) {
			t.Errorf("app.css no longer declares %q: %s", c.want, c.why)
		}
	}
	// The rail used to be lifted above the work list by order:-1, which left a
	// sighted keyboard user tabbing in an order that did not match what they
	// saw. Visual order follows document order now, at every width.
	if strings.Contains(flat, ".rail{order:") {
		t.Error("the decision rail must not be re-ordered away from its document position (WCAG 1.3.2)")
	}
}
