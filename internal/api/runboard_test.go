package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

func runGroupFact(state string, lease *store.Lease, blockers []store.TaskRef) store.ProjectWorkFact {
	return store.ProjectWorkFact{
		Task:         model.Task{State: state},
		Lease:        lease,
		OpenBlockers: blockers,
	}
}

func TestRunGroupOf(t *testing.T) {
	lease := &store.Lease{}
	blocker := []store.TaskRef{{ID: "WL-1"}}
	cases := []struct {
		name string
		fact store.ProjectWorkFact
		want runGroup
	}{
		{"ready unblocked", runGroupFact("ready", nil, nil), runGroupReady},
		{"ready blocked", runGroupFact("ready", nil, blocker), runGroupWaiting},
		{"in_progress leased", runGroupFact("in_progress", lease, nil), runGroupRunning},
		{"in_progress orphaned", runGroupFact("in_progress", nil, nil), runGroupJudgment},
		{"in_review", runGroupFact("in_review", nil, nil), runGroupJudgment},
		{"in_review blocked still judgment", runGroupFact("in_review", nil, blocker), runGroupJudgment},
		{"abandoned", runGroupFact("abandoned", nil, nil), runGroupFailed},
		{"merged", runGroupFact("merged", nil, nil), runGroupCompleted},
		{"deployed_prod", runGroupFact("deployed_prod", nil, nil), runGroupCompleted},
		{"released", runGroupFact("released", nil, nil), runGroupCompleted},
		{"draft excluded", runGroupFact("draft", nil, nil), runGroupNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runGroupOf(tc.fact); got != tc.want {
				t.Errorf("runGroupOf(%q) = %v, want %v", tc.fact.Task.State, got, tc.want)
			}
		})
	}
}

// rbTask is a small builder for a fact whose task carries an id, title and
// state; the tests below set whatever else each case needs directly on the
// returned struct.
func rbTask(id, state string) store.ProjectWorkFact {
	return store.ProjectWorkFact{Task: model.Task{ID: id, Title: id + " title", State: state, Assignee: id + "-owner"}}
}

func TestAssembleRunBoard(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	t.Run("order", func(t *testing.T) {
		in := runBoardInputs{
			Facts: []store.ProjectWorkFact{
				rbTask("WL-1", "ready"),
				func() store.ProjectWorkFact {
					f := rbTask("WL-2", "in_progress")
					f.Lease = &store.Lease{AcquiredAt: now.Add(-time.Hour)}
					return f
				}(),
				func() store.ProjectWorkFact {
					f := rbTask("WL-3", "ready")
					f.OpenBlockers = []store.TaskRef{{ID: "WL-99"}}
					return f
				}(),
				rbTask("WL-4", "in_review"),
				rbTask("WL-5", "abandoned"),
				rbTask("WL-6", "merged"),
			},
			Now: now,
		}
		got := assembleRunBoard(in)
		if got == nil {
			t.Fatal("assembleRunBoard() = nil, want a board")
		}
		wantLabels := []string{"Ready", "Running", "Waiting", "Needs judgment", "Failed", "Completed"}
		if len(got.Groups) != len(wantLabels) {
			t.Fatalf("len(Groups) = %d, want %d", len(got.Groups), len(wantLabels))
		}
		for i, label := range wantLabels {
			if got.Groups[i].Label != label {
				t.Errorf("Groups[%d].Label = %q, want %q", i, got.Groups[i].Label, label)
			}
			if len(got.Groups[i].Rows) != 1 {
				t.Errorf("Groups[%d] (%s) has %d rows, want 1", i, label, len(got.Groups[i].Rows))
			}
		}
	})

	t.Run("omission", func(t *testing.T) {
		in := runBoardInputs{
			Facts: []store.ProjectWorkFact{rbTask("WL-1", "ready")},
			Now:   now,
		}
		got := assembleRunBoard(in)
		if got == nil || len(got.Groups) != 1 || got.Groups[0].Label != "Ready" {
			t.Fatalf("assembleRunBoard() = %+v, want exactly one Ready group", got)
		}

		draftOnly := runBoardInputs{
			Facts: []store.ProjectWorkFact{rbTask("WL-2", "draft")},
			Now:   now,
		}
		if got := assembleRunBoard(draftOnly); got != nil {
			t.Fatalf("assembleRunBoard(draft only) = %+v, want nil", got)
		}
	})

	t.Run("active detail", func(t *testing.T) {
		running := rbTask("WL-1", "in_progress")
		running.Lease = &store.Lease{ActorID: "agent-1", AcquiredAt: now.Add(-90 * time.Minute)}
		running.StateEvent = &store.EventFact{Type: "claimed", At: now.Add(-90 * time.Minute)}

		pr := store.PullRequest{Repo: "sunstoneinstitute/worklode", Number: 42, State: "open", TaskID: strPtr("WL-1"), HeadSHA: "abc123", URL: "https://github.com/pr/42"}
		concl := "success"
		in := runBoardInputs{
			Facts:    []store.ProjectWorkFact{running, rbTask("WL-2", "ready")},
			Sessions: []store.ProjectAgentSession{{AgentSession: model.AgentSession{Agent: "claude", AgentVersion: "5"}, TaskID: "WL-1"}},
			PRs:      []store.PullRequest{pr},
			CI: map[store.RepoSHA][]store.CIRun{
				{Repo: "sunstoneinstitute/worklode", SHA: "abc123"}: {
					{Repo: "sunstoneinstitute/worklode", HeadSHA: "abc123", Status: "completed", Conclusion: &concl, StartedAt: now.Add(-time.Hour)},
				},
			},
			Costs: map[string][]store.CostTotal{
				"WL-1": {{Currency: "USD", Cost: "1.50"}},
			},
			Now: now,
		}
		got := assembleRunBoard(in)
		if got == nil {
			t.Fatal("assembleRunBoard() = nil")
		}
		var runningRow, readyRow *ui.RunRowView
		for gi := range got.Groups {
			for ri := range got.Groups[gi].Rows {
				r := &got.Groups[gi].Rows[ri]
				switch r.TaskID {
				case "WL-1":
					runningRow = r
				case "WL-2":
					readyRow = r
				}
			}
		}
		if runningRow == nil || readyRow == nil {
			t.Fatalf("missing rows: running=%v ready=%v", runningRow, readyRow)
		}
		if runningRow.Owner != "WL-1-owner" {
			t.Errorf("Owner = %q, want %q", runningRow.Owner, "WL-1-owner")
		}
		if runningRow.Delegate != "claude v5" {
			t.Errorf("Delegate = %q, want %q", runningRow.Delegate, "claude v5")
		}
		if runningRow.LeaseAge == "" {
			t.Error("LeaseAge is empty, want a relative age")
		}
		if runningRow.LastEvent == "" {
			t.Error("LastEvent is empty, want a relative event summary")
		}
		if len(runningRow.Costs) != 1 || runningRow.Costs[0] != "USD 1.50" {
			t.Errorf("Costs = %v, want [\"USD 1.50\"]", runningRow.Costs)
		}
		if runningRow.PRLabel == "" || runningRow.PRURL != pr.URL {
			t.Errorf("PRLabel/PRURL = %q/%q, want non-empty label and URL %q", runningRow.PRLabel, runningRow.PRURL, pr.URL)
		}
		if runningRow.CheckLabel != "success" {
			t.Errorf("CheckLabel = %q, want %q", runningRow.CheckLabel, "success")
		}

		if readyRow.Delegate != "" || readyRow.LeaseAge != "" || readyRow.LastEvent != "" ||
			readyRow.PRLabel != "" || readyRow.PRURL != "" || readyRow.CheckLabel != "" || len(readyRow.Costs) != 0 {
			t.Errorf("Ready row carries active-only fields: %+v", readyRow)
		}
	})

	t.Run("waiting holds", func(t *testing.T) {
		f := rbTask("WL-1", "ready")
		f.OpenBlockers = []store.TaskRef{{ID: "WL-9"}}
		f.BlockingPlans = []model.DocRef{{ID: 25}}
		in := runBoardInputs{Facts: []store.ProjectWorkFact{f}, Now: now}
		got := assembleRunBoard(in)
		if got == nil || len(got.Groups) != 1 || len(got.Groups[0].Rows) != 1 {
			t.Fatalf("assembleRunBoard() = %+v, want one Waiting row", got)
		}
		holds := got.Groups[0].Rows[0].Holds
		if !strings.Contains(holds, "WL-9") || !strings.Contains(holds, "25") {
			t.Errorf("Holds = %q, want it to name blocker WL-9 and plan 25", holds)
		}
	})

	t.Run("bounds", func(t *testing.T) {
		var facts []store.ProjectWorkFact
		for i := 0; i < 11; i++ {
			f := rbTask(fmt.Sprintf("WL-%d", i), "merged")
			f.StateEvent = &store.EventFact{Type: "state", At: now.Add(-time.Duration(i) * time.Hour)}
			facts = append(facts, f)
		}
		// One task with no state event at all — sorts last.
		facts = append(facts, rbTask("WL-noevent", "merged"))

		in := runBoardInputs{Facts: facts, Now: now}
		got := assembleRunBoard(in)
		if got == nil || len(got.Groups) != 1 {
			t.Fatalf("assembleRunBoard() = %+v, want one Completed group", got)
		}
		g := got.Groups[0]
		if len(g.Rows) != 10 {
			t.Fatalf("len(Rows) = %d, want 10", len(g.Rows))
		}
		if g.More != 2 {
			t.Fatalf("More = %d, want 2", g.More)
		}
		if g.Rows[0].TaskID != "WL-0" {
			t.Errorf("Rows[0].TaskID = %q, want %q (newest event first)", g.Rows[0].TaskID, "WL-0")
		}
		for _, r := range g.Rows {
			if r.TaskID == "WL-noevent" {
				t.Error("the no-event task should not appear among the newest 10")
			}
		}
	})

	t.Run("orphan wording", func(t *testing.T) {
		f := rbTask("WL-1", "in_progress")
		f.StateEvent = &store.EventFact{Type: "claimed", At: now.Add(-time.Hour)}
		in := runBoardInputs{Facts: []store.ProjectWorkFact{f}, Now: now}
		got := assembleRunBoard(in)
		if got == nil || len(got.Groups) != 1 || got.Groups[0].Label != "Needs judgment" {
			t.Fatalf("assembleRunBoard() = %+v, want one Needs judgment group", got)
		}
		if le := got.Groups[0].Rows[0].LastEvent; le != "lease expired" {
			t.Errorf("LastEvent = %q, want %q", le, "lease expired")
		}
	})
}

func strPtr(s string) *string { return &s }
