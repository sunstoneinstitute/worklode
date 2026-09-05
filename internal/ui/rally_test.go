package ui

import (
	"strings"
	"testing"
)

// The cockpit's own rule: honest empty states, never a card claiming a rally
// is open when the project has none.
func TestCockpitOmitsRallyCardWhenAbsent(t *testing.T) {
	body := renderCockpit(t, CockpitView{Page: PageProps{Title: "p"}})
	if strings.Contains(body, "rally-heading") {
		t.Errorf("cockpit rendered a rally card with no rally:\n%s", body)
	}
}

func TestCockpitShowsRallyCard(t *testing.T) {
	body := renderCockpit(t, CockpitView{
		Page: PageProps{Title: "p"},
		Rally: &CockpitRally{
			ID: "WL-667", Title: "Rally the release", URL: "/tasks/WL-667",
			Done: 1, Total: 3,
			Members: []CockpitRallyMember{
				{ID: "WL-140", Title: "Fix the audit", URL: "/tasks/WL-140"},
			},
		},
	})
	for _, want := range []string{"WL-667", "Rally the release", "1", "3", `href="/tasks/WL-667"`, "WL-140", `href="/tasks/WL-140"`} {
		if !strings.Contains(body, want) {
			t.Errorf("cockpit rally card missing %q:\n%s", want, body)
		}
	}
}
