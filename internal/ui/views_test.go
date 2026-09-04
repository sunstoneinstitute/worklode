package ui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHomeActivity(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zero time", time.Time{}, "No activity yet"},
		{"fixed timestamp", time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC), "Last activity 2026-08-14 09:30"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := homeActivity(c.in); got != c.want {
				t.Errorf("homeActivity(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// renderHome renders Home(v) into a string, failing the test on error.
func renderHome(t *testing.T, v HomeView) string {
	t.Helper()
	var b strings.Builder
	if err := Home(v).Render(context.Background(), &b); err != nil {
		t.Fatalf("render Home: %v", err)
	}
	return b.String()
}

func TestHomeActorMode(t *testing.T) {
	body := renderHome(t, HomeView{
		Page: PageProps{Title: "Home"},
		Mode: "actor",
		Cards: []HomeCard{
			{
				ProjectID:    "p1",
				Name:         "Alpha",
				Key:          "ALP",
				RoleBadge:    "Lead",
				Signal:       "You lead this project",
				InProgress:   2,
				InReview:     1,
				Blocked:      0,
				CrewInitials: []string{"SB", "JD"},
				CrewMore:     3,
				LastActivity: time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC),
			},
		},
	})
	if !strings.Contains(body, `href="/projects/p1"`) {
		t.Errorf("expected card link to /projects/p1, got: %s", body)
	}
	if !strings.Contains(body, "Lead") {
		t.Error("expected the role-badge chip text \"Lead\"")
	}
	if !strings.Contains(body, "You lead this project") {
		t.Error("expected the signal line")
	}
	for _, want := range []string{"In progress", "In review", "Blocked"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected counts-strip label %q", want)
		}
	}
	if !strings.Contains(body, "+3") {
		t.Error("expected the crew overflow chip \"+3\"")
	}
	if !strings.Contains(body, "Last activity 2026-08-14 09:30") {
		t.Error("expected the last-activity line")
	}
}

func TestHomeOpenModeOmitsRoleAndSignal(t *testing.T) {
	body := renderHome(t, HomeView{
		Page: PageProps{Title: "Home"},
		Mode: "open",
		Cards: []HomeCard{
			{ProjectID: "p1", Name: "Alpha", Key: "ALP", InProgress: 1, InReview: 0, Blocked: 0},
			{ProjectID: "p2", Name: "Beta", Key: "BET", InProgress: 0, InReview: 0, Blocked: 0},
		},
	})
	if !strings.Contains(body, `href="/projects/p1"`) || !strings.Contains(body, `href="/projects/p2"`) {
		t.Errorf("expected both card links, got: %s", body)
	}
	if strings.Contains(body, "chip lead") || strings.Contains(body, ">Lead<") || strings.Contains(body, ">Member<") {
		t.Error("open mode must render no role-badge chip")
	}
	if strings.Contains(body, "You lead this project") || strings.Contains(body, "You are on this project") || strings.Contains(body, "approval") {
		t.Error("open mode must render no signal line")
	}
}

func TestHomeEmptyMode(t *testing.T) {
	body := renderHome(t, HomeView{
		Page: PageProps{Title: "Home"},
		Mode: "empty",
	})
	if !strings.Contains(body, "You are not on any project yet.") {
		t.Error("expected the empty-state text")
	}
	if !strings.Contains(body, `href="/projects"`) {
		t.Error("expected the Browse all projects link")
	}
	if !strings.Contains(body, "Browse all projects") {
		t.Error("expected the Browse all projects label")
	}
	if strings.Contains(body, `class="homecard"`) {
		t.Error("empty mode must render no homecard")
	}
}

func TestHomeOpenModeZeroProjects(t *testing.T) {
	body := renderHome(t, HomeView{
		Page: PageProps{Title: "Home"},
		Mode: "open",
	})
	if !strings.Contains(body, "No projects yet.") {
		t.Error("expected the open-mode zero-projects text")
	}
	if strings.Contains(body, `class="homecard"`) {
		t.Error("zero-project open mode must render no homecard")
	}
}

// TestDeliverableChipFollowsReportedState pins the page's one substantive
// judgment: a deliverable stores no state (spec 029 §3.2), so the chip is
// whatever the newest evidence reported about the declared address, and
// "Declared" only while nothing has reported one.
func TestDeliverableChipFollowsReportedState(t *testing.T) {
	for _, c := range []struct{ state, chip, label string }{
		{"", "declared", "Declared"},
		{"published", "ok", "Published"},
		{"updated", "ok", "Updated"},
		{"deprecated", "warn", "Deprecated"},
		{"removed", "crit", "Removed"},
		{"failed", "crit", "Failed"},
	} {
		if got := deliverableChip(c.state); got != c.chip {
			t.Errorf("deliverableChip(%q) = %q, want %q", c.state, got, c.chip)
		}
		if got := deliverableLabel(c.state); got != c.label {
			t.Errorf("deliverableLabel(%q) = %q, want %q", c.state, got, c.label)
		}
	}
}

// TestDeliverablesPageShowsTheReport renders the page with one reported and
// one unreported row: the reported one shows its state and the time the
// emitter reported, the unreported one still says Declared, and the page no
// longer claims that nothing can report at all.
func TestDeliverablesPageShowsTheReport(t *testing.T) {
	reportedAt := time.Date(2026, 8, 19, 9, 12, 0, 0, time.UTC)
	var b strings.Builder
	err := Deliverables(DeliverablesView{
		Page:    PageProps{Title: "Deliverables"},
		Project: CockpitProject{ID: "p", Name: "Project", Key: "P"},
		Groups: []DeliverableGroup{{Rows: []DeliverableRow{
			{
				ID: "P-DEL-1", Name: "Casualties", CreatedAt: reportedAt,
				Artifact:      "bigquery://sunstone-prod/cow/casualties",
				ReportedState: "published", ReportedAt: &reportedAt,
			},
			{ID: "P-DEL-2", Name: "Methodology", CreatedAt: reportedAt},
		}}},
	}).Render(context.Background(), &b)
	if err != nil {
		t.Fatalf("render Deliverables: %v", err)
	}
	body := b.String()

	for _, want := range []string{
		`class="chip ok"`, "Published",
		"bigquery://sunstone-prod/cow/casualties",
		"reported 2026-08-19 09:12",
		`class="chip declared"`, "Declared",
		"poll prober",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	if strings.Contains(body, "are not built yet") {
		t.Error("the page still says no emitter can report a deliverable state")
	}
}

// TestRunBoard renders the run board with one row in every §8 group and
// pins: the six group labels appear in the spec's fixed order; a Running
// row's delegate, lease age, cost and check label render; a Waiting row
// names its blocker; a bounded group's More renders "and N more"; and the
// project-local Work nav item is marked current.
func TestRunBoard(t *testing.T) {
	var b strings.Builder
	err := RunBoard(RunBoardView{
		Page:    PageProps{Title: "Work"},
		Project: CockpitProject{ID: "p", Name: "Project", Key: "P"},
		Groups: []RunGroupView{
			{Label: "Ready", Rows: []RunRowView{
				{TaskID: "P-1", Title: "Ready task", TaskURL: "/tasks/P-1", Owner: "Ada"},
			}},
			{Label: "Running", Rows: []RunRowView{
				{
					TaskID: "P-2", Title: "Running task", TaskURL: "/tasks/P-2",
					Owner: "Ada", Delegate: "claude-code", LeaseAge: "12m",
					LastEvent: "in_progress 12m ago", Costs: []string{"USD 1.23"},
					PRLabel: "#4 open", PRURL: "https://github.com/x/y/pull/4",
					CheckLabel: "success",
				},
			}},
			{Label: "Waiting", Rows: []RunRowView{
				{TaskID: "P-3", Title: "Waiting task", TaskURL: "/tasks/P-3", Owner: "Ada", Holds: "P-9"},
			}},
			{Label: "Needs judgment", Rows: []RunRowView{
				{TaskID: "P-4", Title: "Judgment task", TaskURL: "/tasks/P-4"},
			}},
			{Label: "Failed", Rows: []RunRowView{
				{TaskID: "P-5", Title: "Failed task", TaskURL: "/tasks/P-5"},
			}, More: 2},
			{Label: "Completed", Rows: []RunRowView{
				{TaskID: "P-6", Title: "Completed task", TaskURL: "/tasks/P-6"},
			}},
		},
	}).Render(context.Background(), &b)
	if err != nil {
		t.Fatalf("render RunBoard: %v", err)
	}
	body := b.String()

	last := -1
	for _, label := range []string{"Ready", "Running", "Waiting", "Needs judgment", "Failed", "Completed"} {
		idx := strings.Index(body, ">"+label+"<")
		if idx == -1 {
			t.Fatalf("missing group heading %q", label)
		}
		if idx <= last {
			t.Errorf("group %q rendered out of the pinned §8 order", label)
		}
		last = idx
	}

	for _, want := range []string{
		"claude-code", "12m", "USD 1.23", "success",
		"P-9",
		"and 2 more",
		`aria-current="page"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

// TestRunBoardEmpty pins the honest empty state: no groups means no group
// headings, just the empty-board line.
func TestRunBoardEmpty(t *testing.T) {
	var b strings.Builder
	err := RunBoard(RunBoardView{
		Page:    PageProps{Title: "Work"},
		Project: CockpitProject{ID: "p", Name: "Project", Key: "P"},
	}).Render(context.Background(), &b)
	if err != nil {
		t.Fatalf("render RunBoard: %v", err)
	}
	body := b.String()
	if !strings.Contains(body, "No work in this project yet.") {
		t.Error("empty board is missing the empty-board line")
	}
	if strings.Contains(body, "<h3>") {
		t.Error("empty board should render no group headings")
	}
}

// morningBriefFixture is a Brief carrying one group with all four tiers, for
// TestHomeMorningBrief.
func morningBriefFixture() *MorningBriefView {
	return &MorningBriefView{
		Cutoff:    1234567890,
		CanReview: true,
		Groups: []MorningBriefGroup{
			{
				ProjectID: "p1",
				Name:      "Alpha",
				FocusNote: "Ship the migration first",
				NeedsYou:  []MorningBriefItem{{Text: "Decision needed: schema change", Href: "/tasks/t1"}},
				Outcomes:  []MorningBriefItem{{Text: "Shipped WL-100", Href: "/tasks/t100"}},
				Stopped:   []MorningBriefItem{{Text: "Blocked on review", Href: ""}},
				Routine:   3,
			},
		},
	}
}

// TestHomeMorningBrief pins spec 032 §9/§11's judgment bar: NeedsYou first
// and strongest, routine collapsed to one line with no per-event rendering,
// and the review form wired to /home/reviewed.
func TestHomeMorningBrief(t *testing.T) {
	body := renderHome(t, HomeView{
		Page:  PageProps{Title: "Home"},
		Mode:  "actor",
		Brief: morningBriefFixture(),
	})

	if !strings.Contains(body, "Morning Brief") {
		t.Error("expected the Morning Brief heading")
	}

	needsYou := strings.Index(body, "Decision needed: schema change")
	outcomes := strings.Index(body, "Shipped WL-100")
	stopped := strings.Index(body, "Blocked on review")
	if needsYou < 0 || outcomes < 0 || stopped < 0 {
		t.Fatalf("expected all three tier items to render, got: %s", body)
	}
	if !(needsYou < outcomes && outcomes < stopped) {
		t.Errorf("expected NeedsYou before Outcomes before Stopped, got indices %d, %d, %d", needsYou, outcomes, stopped)
	}

	if !strings.Contains(body, "3 routine updates") {
		t.Error("expected the collapsed routine count \"3 routine updates\"")
	}
	if strings.Count(body, "routine") != 1 {
		t.Errorf("expected the word \"routine\" to appear exactly once (the collapsed count line), no per-event rendering; got %d", strings.Count(body, "routine"))
	}

	if !strings.Contains(body, `action="/home/reviewed"`) {
		t.Error("expected the review form to post to /home/reviewed")
	}
	if !strings.Contains(body, `name="cutoff"`) {
		t.Error("expected the hidden cutoff field")
	}
	if !strings.Contains(body, "Reviewed through now") {
		t.Error("expected the review button label")
	}
}

// TestHomeMorningBriefNil confirms a nil Brief (open mode, or nothing to
// say) renders no morningbrief section at all.
func TestHomeMorningBriefNil(t *testing.T) {
	body := renderHome(t, HomeView{
		Page: PageProps{Title: "Home"},
		Mode: "actor",
	})
	if strings.Contains(body, "morningbrief") {
		t.Error("nil Brief must render no morningbrief section")
	}
	if strings.Contains(body, "Morning Brief") {
		t.Error("nil Brief must render no Morning Brief heading")
	}
}

// TestHomeMorningBriefCanReviewFalse confirms the review form is withheld
// when the cutoff hasn't actually advanced.
func TestHomeMorningBriefCanReviewFalse(t *testing.T) {
	brief := morningBriefFixture()
	brief.CanReview = false
	body := renderHome(t, HomeView{
		Page:  PageProps{Title: "Home"},
		Mode:  "actor",
		Brief: brief,
	})
	if strings.Contains(body, `action="/home/reviewed"`) {
		t.Error("CanReview: false must render no review form")
	}
}

// TestHomeMorningBriefZeroGroups confirms zero groups with CanReview true
// still renders the honest empty line and the review form.
func TestHomeMorningBriefZeroGroups(t *testing.T) {
	body := renderHome(t, HomeView{
		Page: PageProps{Title: "Home"},
		Mode: "actor",
		Brief: &MorningBriefView{
			Cutoff:    1234567890,
			CanReview: true,
		},
	})
	if !strings.Contains(body, "Nothing needing you since your last review.") {
		t.Error("expected the nothing-needing-you line")
	}
	if !strings.Contains(body, `action="/home/reviewed"`) {
		t.Error("expected the review form even with zero groups")
	}
}
