package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestProjectCockpit asserts the normalized top-level shape of the cockpit
// projection for an ordinary project with no tasks: every collection is a
// concrete empty slice (never null), pinned_focus and next_decision are JSON
// null (never dummy records), and the mode is the declared-evidence
// Operations basis (Part 1 stores no other lifecycle facts).
func TestProjectCockpit(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}

	var got struct {
		CanonicalURL string `json:"canonical_url"`
		Project      struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Key  string `json:"key"`
		} `json:"project"`
		Mode struct {
			Name  string `json:"name"`
			Basis struct {
				Category string `json:"category"`
				Summary  string `json:"summary"`
			} `json:"basis"`
		} `json:"mode"`
	}
	decodeInto(t, rr, &got)

	if got.CanonicalURL != "/projects/proj" {
		t.Errorf("canonical_url = %q, want /projects/proj", got.CanonicalURL)
	}
	if got.Project.ID != "proj" || got.Project.Key != "WL" {
		t.Errorf("project = %+v, want id=proj key=WL", got.Project)
	}
	if got.Mode.Name != "operations" {
		t.Errorf("mode.name = %q, want operations", got.Mode.Name)
	}
	if got.Mode.Basis.Category != "declared" {
		t.Errorf("mode.basis.category = %q, want declared", got.Mode.Basis.Category)
	}
	const wantSummary = "Existing Worklode project; no intake lifecycle facts are present"
	if got.Mode.Basis.Summary != wantSummary {
		t.Errorf("mode.basis.summary = %q, want %q", got.Mode.Basis.Summary, wantSummary)
	}

	// Marshal-level check: collections serialize as [], never null; optional
	// governed objects serialize as null, never a dummy record.
	body := rr.Body.String()
	for _, want := range []string{
		`"pinned_focus":null`,
		`"ranking_focus":[]`,
		`"next_decision":null`,
		`"work":{"in_progress":[],"in_review":[],"ready":[],"blocked":[]}`,
		`"secondary_concerns":[]`,
		`"repositories":[]`,
		`"cost":{"days":[],"totals":[]}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody: %s", want, body)
		}
	}
}

// pinnedFocusDecode is the JSON shape of pinned_focus and next_decision, used
// by the curated-cards test below.
type pinnedFocusDecode struct {
	PinnedFocus *struct {
		Note     string `json:"note"`
		PinnedBy *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"pinned_by"`
	} `json:"pinned_focus"`
	NextDecision *struct {
		Title       string `json:"title"`
		Accountable string `json:"accountable"`
		Readiness   string `json:"readiness"`
	} `json:"next_decision"`
}

// TestProjectCockpitPinnedFocusAndDecision asserts the curated v0 cards
// (migration 0013) surface in the projection once a lead sets them: a pinned
// focus whose pinner resolves to a real actor's display name, and a next
// decision carrying its title/accountable/readiness.
func TestProjectCockpitPinnedFocusAndDecision(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	ctx := context.Background()
	if err := st.CreateActor(ctx, "stig", "human", "Stig Bakken", true); err != nil {
		t.Fatalf("create actor stig: %v", err)
	}
	if err := st.PinProjectFocus(ctx, "proj", "Ship the cockpit", "stig", st.Now()); err != nil {
		t.Fatalf("PinProjectFocus: %v", err)
	}
	if err := st.SetProjectNextDecision(ctx, "proj", "Pick a datastore", "stig", "blocked on benchmark"); err != nil {
		t.Fatalf("SetProjectNextDecision: %v", err)
	}

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got pinnedFocusDecode
	decodeInto(t, rr, &got)

	if got.PinnedFocus == nil {
		t.Fatalf("pinned_focus = nil, want populated; body %s", rr.Body.String())
	}
	if got.PinnedFocus.Note != "Ship the cockpit" {
		t.Errorf("pinned_focus.note = %q, want %q", got.PinnedFocus.Note, "Ship the cockpit")
	}
	if by := got.PinnedFocus.PinnedBy; by == nil || by.ID != "stig" || by.Name != "Stig Bakken" {
		t.Errorf("pinned_focus.pinned_by = %#v, want stig/Stig Bakken", by)
	}
	if got.NextDecision == nil {
		t.Fatalf("next_decision = nil, want populated")
	}
	if got.NextDecision.Title != "Pick a datastore" ||
		got.NextDecision.Accountable != "stig" ||
		got.NextDecision.Readiness != "blocked on benchmark" {
		t.Errorf("next_decision = %#v, want title/accountable/readiness populated", got.NextDecision)
	}
}

// TestProjectCockpitPinnedByUnresolvedName asserts a pinned-by that is a plain
// seeded display name (no matching actor row) still surfaces as the pinner's
// name — an unknown pinner must not blank out or fail the card.
func TestProjectCockpitPinnedByUnresolvedName(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	ctx := context.Background()
	if err := st.PinProjectFocus(ctx, "proj", "Curated note", "Ada Lovelace", st.Now()); err != nil {
		t.Fatalf("PinProjectFocus: %v", err)
	}

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got pinnedFocusDecode
	decodeInto(t, rr, &got)

	if got.PinnedFocus == nil || got.PinnedFocus.PinnedBy == nil {
		t.Fatalf("pinned_focus/pinned_by = %#v, want a fallback pinner", got.PinnedFocus)
	}
	if by := got.PinnedFocus.PinnedBy; by.ID != "" || by.Name != "Ada Lovelace" {
		t.Errorf("pinned_by = %#v, want empty id and the raw seeded name", by)
	}
}

// TestProjectCockpitRequiresAuth mirrors TestAPIRequiresAuth for the cockpit
// route: a missing bearer token must 401, not fall through to an unmatched
// route (404) or an anonymous read.
func TestProjectCockpitRequiresAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// TestProjectCockpitUnknownProject asserts GetProject's ErrNotFound maps to
// 404 through mapStoreErr, same as every other project-scoped endpoint.
func TestProjectCockpitUnknownProject(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/projects/nosuch/cockpit", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body %s", rr.Code, rr.Body.String())
	}
}

// TestProjectCockpitReadyTask asserts a ready task is adapted from the board
// into the ready work bucket with its declared fields intact and blocked
// left false (it has no blocking edge).
func TestProjectCockpitReadyTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Ready task", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}

	var got struct {
		Work struct {
			Ready []struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Priority string `json:"priority"`
				State    string `json:"state"`
				Blocked  bool   `json:"blocked"`
				URL      string `json:"url"`
			} `json:"ready"`
			InProgress []any `json:"in_progress"`
			InReview   []any `json:"in_review"`
			Blocked    []any `json:"blocked"`
		} `json:"work"`
	}
	decodeInto(t, rr, &got)

	if len(got.Work.Ready) != 1 {
		t.Fatalf("ready tasks = %d, want 1: %+v", len(got.Work.Ready), got.Work.Ready)
	}
	task := got.Work.Ready[0]
	if task.ID != "WL-1" || task.Title != "Ready task" || task.Priority != "medium" ||
		task.State != "ready" || task.Blocked || task.URL != "/tasks/WL-1" {
		t.Errorf("ready task = %+v", task)
	}
	if len(got.Work.InProgress) != 0 || len(got.Work.InReview) != 0 || len(got.Work.Blocked) != 0 {
		t.Errorf("other buckets non-empty: %+v", got.Work)
	}
}

// TestProjectCockpitForbidsLegacyKeys guards the plan's binding constraint:
// the cockpit is a projection, never a workflow column, and must never emit
// a stored/editable health field, completion percentage, stage, or
// variant-driven mode.
func TestProjectCockpitForbidsLegacyKeys(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, forbidden := range []string{
		`"completion_percentage"`, `"health"`, `"stage"`, `"variant"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body contains forbidden key %s:\n%s", forbidden, body)
		}
	}
}

// TestProjectCockpitVariantQueryIgnored asserts the plan's binding
// constraint that ?variant=A|B|C must never change the mode: Part 1 does not
// even read the query string, so this proves the negative rather than
// merely assuming it.
func TestProjectCockpitVariantQueryIgnored(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	for _, variant := range []string{"", "A", "B", "C"} {
		path := "/api/v1/projects/proj/cockpit"
		if variant != "" {
			path += "?variant=" + variant
		}
		rr := doReq(t, h, "GET", path, token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("variant %q: status = %d, body %s", variant, rr.Code, rr.Body.String())
		}
		var got struct {
			Mode struct {
				Name string `json:"name"`
			} `json:"mode"`
		}
		decodeInto(t, rr, &got)
		if got.Mode.Name != "operations" {
			t.Errorf("variant %q: mode.name = %q, want operations", variant, got.Mode.Name)
		}
	}
}

// cockpitWorkItemDecode is the JSON shape of one cockpitWorkItem, shared by
// the owner/delegate/evidence tests below.
type cockpitWorkItemDecode struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Blocked bool   `json:"blocked"`
	URL     string `json:"url"`
	Owner   *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"owner"`
	Delegate *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"delegate"`
	StatusEvidence struct {
		Category string `json:"category"`
		Summary  string `json:"summary"`
	} `json:"status_evidence"`
}

type cockpitWorkDecode struct {
	InProgress []cockpitWorkItemDecode `json:"in_progress"`
	InReview   []cockpitWorkItemDecode `json:"in_review"`
	Ready      []cockpitWorkItemDecode `json:"ready"`
	Blocked    []cockpitWorkItemDecode `json:"blocked"`
}

// getCockpit fetches and decodes the cockpit projection's work + secondary
// concerns, failing the test on any non-200 or decode error.
func getCockpit(t *testing.T, h http.Handler, token, project string) (cockpitWorkDecode, []struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	URL   string `json:"url"`
}) {
	t.Helper()
	rr := doReq(t, h, "GET", "/api/v1/projects/"+project+"/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("cockpit status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Work              cockpitWorkDecode `json:"work"`
		SecondaryConcerns []struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"secondary_concerns"`
	}
	decodeInto(t, rr, &got)
	return got.Work, got.SecondaryConcerns
}

// TestProjectCockpitOwnerAndDelegate asserts a human assignee resolves as
// owner (real display name, not the bare id) and an agent that holds the
// unreleased lease resolves as delegate — the closing-the-deferred-item
// behavior Task 1 left provisional.
func TestProjectCockpitOwnerAndDelegate(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	ctx := context.Background()
	if err := st.CreateActor(ctx, "dana", "human", "Dana", false); err != nil {
		t.Fatalf("create actor dana: %v", err)
	}
	if err := st.CreateActor(ctx, "agent-one", "agent", "Agent One", false); err != nil {
		t.Fatalf("create actor agent-one: %v", err)
	}
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Owned and delegated", "priority": "medium", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, map[string]any{"assignee": "dana"})
	if rr.Code != http.StatusOK {
		t.Fatalf("assign status = %d, body %s", rr.Code, rr.Body.String())
	}
	// Claim runs as the bearer token's actor, not agent-one, unless we
	// authenticate as agent-one; mint a token for it so the lease holder
	// really is the agent.
	agentToken, err := st.CreateToken(ctx, "agent-one", "test token", nil)
	if err != nil {
		t.Fatalf("create token for agent-one: %v", err)
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", agentToken, map[string]any{"worktree": "host:/wt-agent-one"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	work, _ := getCockpit(t, h, token, "proj")
	if len(work.InProgress) != 1 {
		t.Fatalf("in_progress = %#v, want 1 item", work.InProgress)
	}
	item := work.InProgress[0]
	if item.Owner == nil || item.Owner.ID != "dana" || item.Owner.Name != "Dana" {
		t.Errorf("owner = %#v, want dana/Dana", item.Owner)
	}
	if item.Delegate == nil || item.Delegate.ID != "agent-one" || item.Delegate.Name != "Agent One" {
		t.Errorf("delegate = %#v, want agent-one/Agent One", item.Delegate)
	}
	// Claim's own event is a cli-sourced lease.claimed: observed evidence.
	if item.StatusEvidence.Category != "observed" {
		t.Errorf("status_evidence.category = %q, want observed", item.StatusEvidence.Category)
	}
}

// TestProjectCockpitHumanLeaseIsNotDelegate asserts a human (or service)
// lease holder is real technical evidence but never surfaces as a delegate
// or Crew member.
func TestProjectCockpitHumanLeaseIsNotDelegate(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	ctx := context.Background()
	if err := st.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	bobToken, err := st.CreateToken(ctx, "bob", "test token", nil)
	if err != nil {
		t.Fatalf("create token for bob: %v", err)
	}
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Human-leased", "priority": "medium", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", bobToken, map[string]any{"worktree": "host:/wt-bob"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	work, _ := getCockpit(t, h, token, "proj")
	if len(work.InProgress) != 1 {
		t.Fatalf("in_progress = %#v, want 1 item", work.InProgress)
	}
	if got := work.InProgress[0].Delegate; got != nil {
		t.Errorf("delegate = %#v, want nil for a human lease holder", got)
	}
}

// TestProjectCockpitMissingDisplayNameFallsBackToID asserts an actor with an
// empty display name renders its id as the owner's name rather than "".
func TestProjectCockpitMissingDisplayNameFallsBackToID(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	ctx := context.Background()
	if err := st.CreateActor(ctx, "svc-1", "human", "", false); err != nil {
		t.Fatalf("create actor svc-1: %v", err)
	}
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "No display name", "priority": "medium", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, map[string]any{"assignee": "svc-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("assign status = %d, body %s", rr.Code, rr.Body.String())
	}

	work, _ := getCockpit(t, h, token, "proj")
	if len(work.Ready) != 1 {
		t.Fatalf("ready = %#v, want 1 item", work.Ready)
	}
	if got := work.Ready[0].Owner; got == nil || got.Name != "svc-1" {
		t.Errorf("owner = %#v, want name svc-1 (fallback to id)", got)
	}
}

// TestProjectCockpitBlockedSecondaryConcerns asserts a ready task with an
// open blocker lands in work.blocked (not work.ready) and its blocker
// becomes a secondary concern.
func TestProjectCockpitBlockedSecondaryConcerns(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Blocker task", "priority": "high", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Dependent task", "priority": "medium", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add blocking edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	work, secondary := getCockpit(t, h, token, "proj")
	// The blocker itself (WL-1) is unblocked and belongs in Ready; only the
	// dependent (WL-2) must be excluded from Ready and land in Blocked.
	for _, item := range work.Ready {
		if item.ID == "WL-2" {
			t.Fatalf("WL-2 unexpectedly in work.ready: %#v", item)
		}
	}
	if len(work.Blocked) != 1 || work.Blocked[0].ID != "WL-2" || !work.Blocked[0].Blocked {
		t.Fatalf("blocked = %#v, want [WL-2] with blocked=true", work.Blocked)
	}
	found := false
	for _, c := range secondary {
		if c.Kind == "blocker" && c.Title == "Blocker task" && c.URL == "/tasks/WL-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("secondary_concerns = %#v, want an entry for WL-1 (Blocker task)", secondary)
	}
}

// TestProjectCockpitObservedGithubEvent asserts a github-sourced state event
// classifies as observed evidence, regardless of the event's own type.
func TestProjectCockpitObservedGithubEvent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "GitHub-observed", "priority": "medium", "kind": "feature",
	})
	seedEvent(t, st, "gh-state", func(tx *sql.Tx, eventID int64) error {
		return store.LogChange(tx, "task", "WL-1", eventID,
			map[string]string{"field": "state", "old": "ready", "new": "ready"})
	})

	work, _ := getCockpit(t, h, token, "proj")
	if len(work.Ready) != 1 {
		t.Fatalf("ready = %#v, want 1 item", work.Ready)
	}
	if got := work.Ready[0].StatusEvidence.Category; got != "observed" {
		t.Errorf("status_evidence.category = %q, want observed", got)
	}
}

// TestProjectCockpitUserReportedHumanStart asserts a human /start (cli
// task.started, not a lease event) classifies as user-reported evidence.
func TestProjectCockpitUserReportedHumanStart(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	ctx := context.Background()
	if err := st.CreateActor(ctx, "erin", "human", "Erin", false); err != nil {
		t.Fatalf("create actor erin: %v", err)
	}
	erinToken, err := st.CreateToken(ctx, "erin", "test token", nil)
	if err != nil {
		t.Fatalf("create token for erin: %v", err)
	}
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Human-started", "priority": "medium", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/start", erinToken, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start status = %d, body %s", rr.Code, rr.Body.String())
	}

	work, _ := getCockpit(t, h, token, "proj")
	if len(work.InProgress) != 1 {
		t.Fatalf("in_progress = %#v, want 1 item", work.InProgress)
	}
	item := work.InProgress[0]
	if item.Owner == nil || item.Owner.ID != "erin" {
		t.Errorf("owner = %#v, want erin (auto-assigned by start)", item.Owner)
	}
	if item.StatusEvidence.Category != "user_reported" {
		t.Errorf("status_evidence.category = %q, want user_reported", item.StatusEvidence.Category)
	}
}
