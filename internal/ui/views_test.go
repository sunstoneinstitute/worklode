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
		Page: PageProps{Title: "Home", ActiveGlobal: "home"},
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
		Page: PageProps{Title: "Home", ActiveGlobal: "home"},
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
		Page: PageProps{Title: "Home", ActiveGlobal: "home"},
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
		Page: PageProps{Title: "Home", ActiveGlobal: "home"},
		Mode: "open",
	})
	if !strings.Contains(body, "No projects yet.") {
		t.Error("expected the open-mode zero-projects text")
	}
	if strings.Contains(body, `class="homecard"`) {
		t.Error("zero-project open mode must render no homecard")
	}
}
