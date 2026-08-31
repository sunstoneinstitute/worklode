package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// --- GET /projects/{id}/work (page-level, Postgres-backed) ------------------
//
// These tests live in package api (white-box), not package api_test: the
// black-box page-test helpers (createProject, createTaskViaAPI, doReq,
// newTestServer, ...) live in api_test files elsewhere in this directory,
// which are a different compiled package this file cannot reach into. The
// helpers below are the same shape, built from exported store functions and
// NewServer directly.

// rbTestServer opens a fresh migrated store and the web-open handler over
// it — the run board page needs no bearer token, so no token is returned.
func rbTestServer(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()
	st := store.OpenTestStore(t)
	h, _, err := NewServer(st, Config{WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return st, h
}

// rbGet issues a GET against h and returns the recorded response.
func rbGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

// rbExtSeq returns a generator of unique event external ids for one test.
func rbExtSeq(t *testing.T) func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("%s-%d", t.Name(), n)
	}
}

// rbCreateTask creates one ready task in projectID via the same path
// CreateTask's own tests use: RecordEvent driving store.CreateTask.
func rbCreateTask(t *testing.T, st *store.Store, ext func() string, now time.Time, projectID, title string) *model.Task {
	t.Helper()
	var task *model.Task
	_, _, err := st.RecordEvent(context.Background(), "cli", ext(), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			task, err = store.CreateTask(tx, now, store.TaskInput{
				ProjectID: projectID, Title: title, Priority: "medium", Kind: "feature",
			}, eventID)
			return err
		})
	if err != nil {
		t.Fatalf("create task %s: %v", title, err)
	}
	return task
}

// rbBlock records a "blocks" edge: blocker holds open work in front of
// blocked (store.AddEdge's fromTask/toTask order — see project_work_test.go).
func rbBlock(t *testing.T, st *store.Store, ext func() string, now time.Time, blocker, blocked string) {
	t.Helper()
	_, _, err := st.RecordEvent(context.Background(), "cli", ext(), "edge.add", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.AddEdge(tx, now, blocker, blocked, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("block %s on %s: %v", blocked, blocker, err)
	}
}

// rbTransition drives one legal state.Transition directly (bypassing the
// claim/done lifecycle that would normally produce it) — the run board's
// concern is what a task's current state renders as, not how it got there.
func rbTransition(t *testing.T, st *store.Store, ext func() string, now time.Time, taskID, from, to string) {
	t.Helper()
	_, _, err := st.RecordEvent(context.Background(), "cli", ext(), "task.transition", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.Transition(tx, now, taskID, from, to, eventID)
		})
	if err != nil {
		t.Fatalf("transition %s %s->%s: %v", taskID, from, to, err)
	}
}

// TestRunBoardPage covers the full convergence: one project with a task in
// every live group the page composes facts for — Ready, Waiting (blocked),
// Running (claimed, an open agent session, priced usage), Needs judgment
// (in_review, an open PR and a completed CI run), and Completed (merged) —
// asserting each pinned group heading renders exactly once and the active
// rows carry the facts assembleRunBoard attaches to them.
func TestRunBoardPage(t *testing.T) {
	t.Parallel()
	st, h := rbTestServer(t)
	ctx := context.Background()
	ext := rbExtSeq(t)
	now := time.Now().UTC().Truncate(time.Second)

	if err := st.CreateProject(ctx, "proj", "Project", "PRJ"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	rbCreateTask(t, st, ext, now, "proj", "Ready task")

	blocker := rbCreateTask(t, st, ext, now, "proj", "Blocker task")
	blocked := rbCreateTask(t, st, ext, now, "proj", "Blocked task")
	rbBlock(t, st, ext, now, blocker.ID, blocked.ID)

	if err := st.CreateActor(ctx, "agent-one", "agent", "Agent One", false); err != nil {
		t.Fatalf("create actor agent-one: %v", err)
	}
	running := rbCreateTask(t, st, ext, now, "proj", "Running task")
	if _, err := st.Claim(ctx, running.ID, "agent-one", "host:/wt-running", 0); err != nil {
		t.Fatalf("claim running task: %v", err)
	}
	// 1e5 output tokens at claude-sonnet-5's seeded $10/MTok output rate
	// (migration 0008) prices to exactly $1.00 — see agentsessions_test.go's
	// sonnetUsagePrevDay, the same bucket shape.
	if _, err := st.TouchAgentSession(ctx, running.ID, "agent-one", "claude-code", "2.1", "sess-1",
		[]store.SessionUsageBucket{{Day: now, Model: "claude-sonnet-5", Tokens: store.TokenCounts{Output: 100_000}}}); err != nil {
		t.Fatalf("touch agent session: %v", err)
	}

	judgment := rbCreateTask(t, st, ext, now, "proj", "Judgment task")
	rbTransition(t, st, ext, now, judgment.ID, "ready", "in_progress")
	rbTransition(t, st, ext, now, judgment.ID, "in_progress", "in_review")
	const (
		prRepo = "acme/widgets"
		prSHA  = "sha-judgment"
	)
	_, _, err := st.RecordEvent(ctx, "github", ext(), "pull_request", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, _, err := store.UpsertPR(tx, store.PullRequest{
				Repo: prRepo, Number: 42, Title: "Judgment task PR", State: "open",
				HeadRef: judgment.ID + "-judgment-task", HeadSHA: prSHA,
				URL: "https://github.com/" + prRepo + "/pull/42", OpenedAt: now,
			}, "")
			return err
		})
	if err != nil {
		t.Fatalf("upsert PR: %v", err)
	}
	concl := "success"
	_, _, err = st.RecordEvent(ctx, "github", ext(), "workflow_run", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.UpsertCIRun(tx, store.CIRun{
				Repo: prRepo, HeadSHA: prSHA, Workflow: "ci", Status: "completed",
				Conclusion: &concl, URL: "https://ci/1", StartedAt: now,
			})
		})
	if err != nil {
		t.Fatalf("upsert CI run: %v", err)
	}

	merged := rbCreateTask(t, st, ext, now, "proj", "Merged task")
	rbTransition(t, st, ext, now, merged.ID, "ready", "merged")

	rr := rbGet(t, h, "/projects/proj/work")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()

	for _, label := range []string{"Ready", "Running", "Waiting", "Needs judgment", "Completed"} {
		heading := "<h3>" + label + "</h3>"
		if n := strings.Count(body, heading); n != 1 {
			t.Errorf("heading %q count = %d, want 1:\n%s", heading, n, body)
		}
	}

	if !strings.Contains(body, "claude-code v2.1") {
		t.Error("Running row is missing the delegate label")
	}
	if !strings.Contains(body, "USD 1.00") {
		t.Error("Running row is missing its priced cost")
	}

	if !strings.Contains(body, `href="https://github.com/`+prRepo+`/pull/42"`) {
		t.Error("Needs-judgment row does not link the PR")
	}
	if !strings.Contains(body, "success") {
		t.Error("Needs-judgment row does not show the CI conclusion")
	}

	completedAt := strings.Index(body, "<h3>Completed</h3>")
	mergedAt := strings.Index(body, merged.ID)
	if completedAt == -1 || mergedAt == -1 || mergedAt < completedAt {
		t.Errorf("merged task %s does not appear under Completed", merged.ID)
	}
}

// TestRunBoardPageEmpty covers a project with no tasks: 200, the honest
// empty-board line, and no group heading rendered.
func TestRunBoardPageEmpty(t *testing.T) {
	t.Parallel()
	st, h := rbTestServer(t)
	if err := st.CreateProject(context.Background(), "empty", "Empty", "EMP"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	rr := rbGet(t, h, "/projects/empty/work")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "No work in this project yet.") {
		t.Errorf("empty board line missing:\n%s", body)
	}
	if strings.Contains(body, "<h3>") {
		t.Errorf("empty board rendered a group heading:\n%s", body)
	}
}

// TestRunBoardPageUnknownProject covers the same 404 every project page
// gives an unknown id (see projectPage).
func TestRunBoardPageUnknownProject(t *testing.T) {
	t.Parallel()
	_, h := rbTestServer(t)
	rr := rbGet(t, h, "/projects/nosuch/work")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}
