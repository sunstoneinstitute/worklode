package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func renderTask(t *testing.T, v TaskView) string {
	t.Helper()
	var b strings.Builder
	if err := Task(v).Render(context.Background(), &b); err != nil {
		t.Fatalf("render Task: %v", err)
	}
	return b.String()
}

func renderCockpit(t *testing.T, v CockpitView) string {
	t.Helper()
	var b strings.Builder
	if err := Cockpit(v).Render(context.Background(), &b); err != nil {
		t.Fatalf("render Cockpit: %v", err)
	}
	return b.String()
}

func TestTaskPageShowsAgentSessions(t *testing.T) {
	body := renderTask(t, TaskView{
		Page: PageProps{Title: "WL-7"},
		AgentSessions: []AgentSessionRow{
			{Agent: "claude-code", AgentVersion: "2.1.231", ActorID: "worker-01",
				Started: "3h ago", LastSeen: "2m ago", Running: true},
		},
	})
	for _, want := range []string{"Agent sessions", "Claude Code", "2.1.231", "worker-01", "3h ago", "2m ago", "running"} {
		if !strings.Contains(body, want) {
			t.Errorf("task page missing %q:\n%s", want, body)
		}
	}
}

// The cockpit's own rule: honest empty states, never a card claiming
// something is running when nothing is.
func TestPagesOmitAgentSessionsCardWhenThereAreNone(t *testing.T) {
	if body := renderTask(t, TaskView{Page: PageProps{Title: "WL-7"}}); strings.Contains(body, "Agent sessions") {
		t.Errorf("task page rendered an empty Agent sessions card:\n%s", body)
	}
	if body := renderCockpit(t, CockpitView{Page: PageProps{Title: "p"}}); strings.Contains(body, "Agent sessions") {
		t.Errorf("cockpit rendered an empty Agent sessions card:\n%s", body)
	}
}

// On the project page a session has to name its task: the page lists work
// from every task, so a row without one places nothing.
func TestCockpitAgentSessionNamesItsTask(t *testing.T) {
	body := renderCockpit(t, CockpitView{
		Page: PageProps{Title: "p"},
		AgentSessions: []AgentSessionRow{
			{Agent: "codex", ActorID: "stig", Task: "WL-9", TaskURL: "/tasks/WL-9",
				Started: "1d ago", LastSeen: "5m ago", Running: true},
		},
	})
	if !strings.Contains(body, `href="/tasks/WL-9"`) {
		t.Errorf("cockpit session row does not link its task:\n%s", body)
	}
	if !strings.Contains(body, "Codex") {
		t.Errorf("cockpit session row missing the agent label:\n%s", body)
	}
}

// A finished session still belongs on a task's page, but must not be dressed
// up as running.
func TestEndedSessionRendersAsEnded(t *testing.T) {
	body := renderTask(t, TaskView{
		Page:          PageProps{Title: "WL-7"},
		AgentSessions: []AgentSessionRow{{Agent: "codex", ActorID: "stig", Started: "2d ago", LastSeen: "1d ago"}},
	})
	if !strings.Contains(body, "ended") {
		t.Errorf("ended session not marked ended:\n%s", body)
	}
	// last-seen on a finished session is just its end time restated.
	if strings.Contains(body, "last seen") {
		t.Errorf("ended session repeats last-seen:\n%s", body)
	}
}

// A lease that outlives its agent's session must not keep the Activity card
// up on the strength of that ended session (spec 071 §4, WL-862).
func TestTaskPageOmitsActivityCardForEndedSessionOnly(t *testing.T) {
	body := renderTask(t, TaskView{
		Page:          PageProps{Title: "WL-7"},
		AgentSessions: []AgentSessionRow{{Agent: "codex", ActorID: "stig", Started: "2d ago", LastSeen: "1d ago", Running: false}},
	})
	if strings.Contains(body, "id=\"activity\"") {
		t.Errorf("task page rendered the Activity card for an ended session with no rows:\n%s", body)
	}
}

// An open session with no activity rows yet is still a reason to keep the
// card: that is where the live stream prepends.
func TestTaskPageKeepsActivityCardForRunningSession(t *testing.T) {
	body := renderTask(t, TaskView{
		Page:          PageProps{Title: "WL-7"},
		AgentSessions: []AgentSessionRow{{Agent: "codex", ActorID: "stig", Started: "2d ago", LastSeen: "1m ago", Running: true}},
	})
	if !strings.Contains(body, "id=\"activity\"") {
		t.Errorf("task page omitted the Activity card for a running session:\n%s", body)
	}
}

func TestAgentLabel(t *testing.T) {
	for in, want := range map[string]string{
		"claude-code": "Claude Code",
		"codex":       "Codex",
		// An unfamiliar harness a client folded onto "other" is shown, not
		// hidden: a blank row would be worse than an unpolished one.
		"other": "other",
		"":      "agent",
	} {
		if got := agentLabel(in); got != want {
			t.Errorf("agentLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// The task page collapses every section after the body into a <details>, and
// activity.js must still find its target inside the collapsed Activity card:
// getElementById("activity"), its data-task, and the ol.activity it prepends
// into. The body renders before any of them.
func TestTaskPageCollapsedActivityKeepsStreamTarget(t *testing.T) {
	body := renderTask(t, TaskView{
		Page:     PageProps{Title: "WL-7"},
		Task:     model.Task{ID: "WL-7"},
		Activity: []ActivityRow{{ID: 3, Event: "tool_result"}},
	})
	open := `<details class="card" id="activity" aria-labelledby="activity-heading" data-task="WL-7">`
	start := strings.Index(body, open)
	if start < 0 {
		t.Fatalf("no collapsed Activity card with its id and data-task:\n%s", body)
	}
	card := body[start : start+strings.Index(body[start:], "</details>")]
	if !strings.Contains(card, `<ol class="activity">`) {
		t.Errorf("ol.activity is not inside the Activity card:\n%s", card)
	}
	if b := strings.Index(body, `id="body-heading"`); b < 0 || b > start {
		t.Errorf("Body does not render before the collapsed sections")
	}
	for _, id := range []string{"edges-heading", "timeline-heading"} {
		if !strings.Contains(body, `<details class="card" aria-labelledby="`+id+`">`) {
			t.Errorf("%s section is not a collapsed <details>", id)
		}
	}
}
