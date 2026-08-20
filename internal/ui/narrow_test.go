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
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// pages renders every page component this package exports, with just enough
// data that the optional regions — the ones holding tables — render.
func pages(t *testing.T) map[string]string {
	t.Helper()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	proj := CockpitProject{ID: "p", Name: "Project", Key: "P"}
	comps := map[string]templ.Component{
		"board": Board(BoardView{
			Page:           PageProps{Title: "Home", ActiveGlobal: "home"},
			IsHome:         true,
			Projects:       []BoardProject{{ID: "p", Name: "Project", Ready: []model.BoardTask{{Task: model.Task{ID: "T-1", Title: "t"}}}}},
			RecentFailures: []BoardFailure{{OccurredAt: now, Cluster: "c", Kind: "k", Workload: "w", Message: "m"}},
		}),
		"cockpit": Cockpit(CockpitView{
			Page: PageProps{Title: "Project"}, Project: proj, ModeName: "operations",
			Work: CockpitWork{Ready: []WorkRow{{ID: "T-1", Title: "t", State: "ready", URL: "/tasks/T-1"}}},
		}),
		"task": Task(TaskView{
			Page: PageProps{Title: "T-1"}, Task: model.Task{ID: "T-1", Title: "t"},
			Timeline: []TimelineRow{{At: now, Type: "pr", Label: "Pull request", Summary: "s"}},
		}),
		"docs": Docs(DocsView{
			Page: PageProps{Title: "Knowledge", ActiveGlobal: "knowledge"},
			Docs: []DocRow{{Doc: model.Doc{ID: 1, Slug: "s", Title: "T"}, URL: "/docs/1", Ref: "spec 1"}},
		}),
		"doc": Doc(DocView{
			Page: PageProps{Title: "T"}, Doc: model.Doc{ID: 1, Slug: "s", Title: "T"},
			Sections: []model.DocSection{{Anchor: "sec-1", Heading: "1. H"}},
		}),
		"projects":     Projects(ProjectsView{Page: PageProps{Title: "Projects", ActiveGlobal: "projects"}, Projects: []model.Project{{ID: "p", Name: "Project"}}}),
		"deliverables": Deliverables(DeliverablesView{Page: PageProps{Title: "Deliverables"}, Project: proj, Deliverables: []DeliverableRow{{ID: "d", Name: "D", URL: "https://example.org/x", CreatedAt: now}}}),
		"newtask":      NewTask(NewTaskView{Form: FormShell{Page: PageProps{Title: "New task"}, Project: proj}}),
		"placeholder":  Placeholder(PlaceholderView{Page: PageProps{Title: "Crew"}, Heading: "Crew", Project: &proj}),
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
		{"scroll-padding-top:64px", "an in-page jump must clear the 56px sticky topbar (WCAG 2.4.11)"},
		{".tablewrap{overflow-x:auto", "a data table scrolls inside its own container (WCAG 1.4.10)"},
		{"pre{overflow-x:auto", "a stored document body cannot be re-wrapped, so it scrolls inside itself (WCAG 1.4.10)"},
		{".prose{overflow-wrap:anywhere", "an unbroken token in a task body must not widen the page (WCAG 1.4.10)"},
		{".wlrow.tl.t{white-space:normal", "a work row's title wraps below 880px instead of truncating to nothing (WCAG 1.4.10)"},
		{".fieldrow.checkinput{width:24px;height:24px", "the draft checkbox meets the minimum target size (WCAG 2.5.8)"},
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
