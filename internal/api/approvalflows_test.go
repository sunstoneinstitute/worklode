package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func flowByName(t *testing.T, flows []model.ApprovalFlow, name string) model.ApprovalFlow {
	t.Helper()
	for _, f := range flows {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no flow named %q in %d flows", name, len(flows))
	return model.ApprovalFlow{}
}

// writeFlow drops one flow file into a fresh dir and returns the dir.
func writeFlow(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadApprovalFlowsShipsTheStoryDefault(t *testing.T) {
	flows, err := api.LoadApprovalFlows("")
	if err != nil {
		t.Fatal(err)
	}
	story := flowByName(t, flows, "story")
	if story.Rev != "1" || story.Match["kind"] != "sunstone-story" {
		t.Errorf("story = rev %q match %v", story.Rev, story.Match)
	}
	if len(story.Requirements) != 6 {
		t.Fatalf("story has %d lanes, want 6", len(story.Requirements))
	}
	want := map[string][2]string{
		"analysis/peer":             {"analysis-reviewers", "Reproducible analysis"},
		"methodology/science-lead":  {"science-leads", "Methodology"},
		"methodology/domain-expert": {"domain-experts", "Methodology"},
		"report/buddy":              {"report-buddies", "Scientific report"},
		"report/expert":             {"domain-experts", "Scientific report"},
		"report/journalist":         {"journalists", "Scientific report"},
	}
	for _, r := range story.Requirements {
		w, ok := want[r.Lane]
		if !ok {
			t.Errorf("unexpected lane %q", r.Lane)
			continue
		}
		if r.Role != w[0] || r.Target != w[1] {
			t.Errorf("lane %s = role %q target %q, want %q / %q", r.Lane, r.Role, r.Target, w[0], w[1])
		}
		if r.EntityKind != "deliverable" {
			t.Errorf("lane %s entity_kind = %q, want deliverable", r.Lane, r.EntityKind)
		}
		delete(want, r.Lane)
	}
	if len(want) != 0 {
		t.Errorf("lanes missing from the shipped flow: %v", want)
	}
}

func TestLoadApprovalFlowsDirOverridesByName(t *testing.T) {
	dir := writeFlow(t, "story.json", `{
		"name": "story", "rev": "9",
		"requirements": [{"lane": "only", "entity_kind": "doc", "role": "editors"}]
	}`)
	flows, err := api.LoadApprovalFlows(dir)
	if err != nil {
		t.Fatal(err)
	}
	story := flowByName(t, flows, "story")
	if story.Rev != "9" || len(story.Requirements) != 1 {
		t.Errorf("story = rev %q with %d lanes, want the dir flow", story.Rev, len(story.Requirements))
	}
	// Replaced, not appended.
	n := 0
	for _, f := range flows {
		if f.Name == "story" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d flows named story, want 1", n)
	}
}

func TestLoadApprovalFlowsRefusesInvalidFiles(t *testing.T) {
	cases := map[string]string{
		"unknown entity kind": `{"name": "x", "rev": "1", "requirements":
			[{"lane": "a", "entity_kind": "milestone", "role": "r"}]}`,
		"pr requirement": `{"name": "x", "rev": "1", "requirements":
			[{"lane": "a", "entity_kind": "pr", "role": "r"}]}`,
		"duplicate lane": `{"name": "x", "rev": "1", "requirements":
			[{"lane": "a", "entity_kind": "task", "role": "r"},
			 {"lane": "a", "entity_kind": "task", "role": "r2"}]}`,
		"empty role": `{"name": "x", "rev": "1", "requirements":
			[{"lane": "a", "entity_kind": "task", "role": ""}]}`,
		"empty name": `{"name": "", "rev": "1", "requirements": []}`,
		"empty rev":  `{"name": "x", "rev": "", "requirements": []}`,
		"not json":   `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeFlow(t, "bad.json", body)
			_, err := api.LoadApprovalFlows(dir)
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), "bad.json") {
				t.Errorf("error does not name the file: %v", err)
			}
		})
	}
}

func TestLoadApprovalFlowsRefusesAnUnreadableFile(t *testing.T) {
	dir := writeFlow(t, "locked.json", `{"name": "x", "rev": "1", "requirements": []}`)
	if err := os.Chmod(filepath.Join(dir, "locked.json"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := api.LoadApprovalFlows(dir); err == nil {
		t.Fatal("want an error for an unreadable flow file, got none")
	} else if !strings.Contains(err.Error(), "locked.json") {
		t.Errorf("error does not name the file: %v", err)
	}
}

func TestLoadApprovalFlowsIgnoresNonJSONAndMissingDir(t *testing.T) {
	dir := writeFlow(t, "notes.txt", "not a flow")
	if _, err := api.LoadApprovalFlows(dir); err != nil {
		t.Errorf("a non-.json file must be ignored: %v", err)
	}
	if _, err := api.LoadApprovalFlows(filepath.Join(dir, "absent")); err == nil {
		t.Error("a configured but missing dir must fail the boot")
	}
}

// TestNewServerLoadsApprovalFlows pins the boot wiring: the flow set is read
// at startup, a bad configured directory fails the boot, and the system actor
// the rules credit their rows to exists before the first request.
func TestNewServerLoadsApprovalFlows(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)

	bad := writeFlow(t, "broken.json", `{"name": "x", "rev": "1", "requirements":
		[{"lane": "a", "entity_kind": "pr", "role": "r"}]}`)
	if _, _, err := api.NewServer(st, api.Config{ApprovalFlowsDir: bad}); err == nil {
		t.Fatal("NewServer booted with an invalid approval flow")
	} else if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("boot error does not name the file: %v", err)
	}

	if _, _, err := api.NewServer(st, api.Config{}); err != nil {
		t.Fatalf("NewServer with the shipped defaults: %v", err)
	}
	actor, err := st.GetActor(t.Context(), "worklode")
	if err != nil {
		t.Fatalf("GetActor(worklode): %v", err)
	}
	if actor == nil {
		t.Fatal("NewServer did not ensure the worklode service actor")
	}
}

// --- applying a flow (029 §7.2) ---------------------------------------

// flowApproval is one materialized row, read straight from the table: the
// awaiting queue's joins do not carry deliverable-kind rows yet (WL-719).
type flowApproval struct{ entityID, lane, state, role, actor, createdBy string }

// flowApprovalRows returns every deliverable-kind approval row, lane-ordered.
func flowApprovalRows(t *testing.T, st *store.Store) []flowApproval {
	t.Helper()
	rows, err := st.DBForTests().Query(
		`SELECT entity_id, lane, state, COALESCE(required_role, ''),
		        COALESCE(required_actor, ''), COALESCE(created_by, '')
		   FROM approvals WHERE entity_kind = 'deliverable'
		  ORDER BY entity_id, lane`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []flowApproval
	for rows.Next() {
		var r flowApproval
		if err := rows.Scan(&r.entityID, &r.lane, &r.state, &r.role, &r.actor, &r.createdBy); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// countEvents returns how many events of one type the log holds.
func countEvents(t *testing.T, st *store.Store, typ string) int {
	t.Helper()
	var n int
	if err := st.DBForTests().QueryRow(
		`SELECT count(*) FROM events WHERE type = $1`, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// createDeliverableNamed declares one deliverable through the JSON API and
// returns its id.
func createDeliverableNamed(t *testing.T, h http.Handler, token, project, name string) string {
	t.Helper()
	rr := doReq(t, h, "POST", "/api/v1/projects/"+project+"/deliverables", token,
		model.CreateDeliverableInput{Name: name})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create deliverable %s = %d, body %s", name, rr.Code, rr.Body.String())
	}
	id, _ := decodeMap(t, rr)["id"].(string)
	if id == "" {
		t.Fatalf("created deliverable has no id: %s", rr.Body.String())
	}
	return id
}

// TestApplyFlowStampsSnapshotAndBackfills is 029 §7.2's apply act: the
// project carries the flow it was stamped with, the deliverables it already
// held owe the lanes that flow demands, a named reviewer replaces the lane's
// role, and applying the same flow again is idempotent in rows while still
// recording that it happened.
func TestApplyFlowStampsSnapshotAndBackfills(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	report := createDeliverableNamed(t, h, token, "proj", "Scientific report")
	// A deliverable no lane targets: the backfill must leave it alone.
	createDeliverableNamed(t, h, token, "proj", "Interview notes")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/approval-flow", token,
		model.ApplyApprovalFlowInput{
			Name:      "story",
			Reviewers: map[string]string{"report/journalist": "alice"},
		})
	if rr.Code != http.StatusOK {
		t.Fatalf("apply status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var out model.ApplyApprovalFlowResponse
	decodeInto(t, rr, &out)
	if out.Materialized != 3 || out.Project.ApprovalFlowName != "story" || out.Project.ApprovalFlowRev != "1" {
		t.Errorf("materialized=%d flow=%q rev=%q, want 3 / story / 1",
			out.Materialized, out.Project.ApprovalFlowName, out.Project.ApprovalFlowRev)
	}

	// The reviewer template replaces the role on the lane it names, and only
	// there; every row is the system actor's, since the rule filed it.
	want := []flowApproval{
		{report, "report/buddy", "awaiting", "report-buddies", "", "worklode"},
		{report, "report/expert", "awaiting", "domain-experts", "", "worklode"},
		{report, "report/journalist", "awaiting", "", "alice", "worklode"},
	}
	got := flowApprovalRows(t, st)
	if len(got) != len(want) {
		t.Fatalf("materialized %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Re-apply: no new rows, but the act is recorded again — an apply is an
	// explicit, event-logged decision, not a diff.
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/approval-flow", token,
		model.ApplyApprovalFlowInput{
			Name:      "story",
			Reviewers: map[string]string{"report/journalist": "alice"},
		})
	if rr.Code != http.StatusOK {
		t.Fatalf("re-apply status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &out)
	if out.Materialized != 0 {
		t.Errorf("re-apply materialized %d rows, want 0", out.Materialized)
	}
	if n := len(flowApprovalRows(t, st)); n != 3 {
		t.Errorf("after re-apply %d rows, want 3", n)
	}
	if n := countEvents(t, st, "approval_flow.applied"); n != 2 {
		t.Errorf("approval_flow.applied events = %d, want 2", n)
	}
}

// TestApprovalFlowMetric: worklode_approval_flow_applied_total carries one
// series per outcome, pre-initialised to zero, and an unknown flow name is a
// 404 that writes nothing — the flow vocabulary is instance configuration, so
// a name nothing defines is a missing resource, not a bad field.
func TestApprovalFlowMetric(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	createDeliverableNamed(t, h, token, "proj", "Scientific report")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/approval-flow", token,
		model.ApplyApprovalFlowInput{Name: "no-such-flow"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown flow = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	if n := len(flowApprovalRows(t, st)); n != 0 {
		t.Errorf("an unknown flow materialized %d rows, want none", n)
	}
	if n := countEvents(t, st, "approval_flow.applied"); n != 0 {
		t.Errorf("an unknown flow recorded %d events, want none", n)
	}

	if rr := doReq(t, h, "POST", "/api/v1/projects/proj/approval-flow", token,
		model.ApplyApprovalFlowInput{Name: "story"}); rr.Code != http.StatusOK {
		t.Fatalf("apply = %d, want 200; body %s", rr.Code, rr.Body.String())
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`worklode_approval_flow_applied_total{outcome="applied"} 1`,
		`worklode_approval_flow_applied_total{outcome="unknown_flow"} 1`,
		// Pre-initialised, so an instance where no apply has failed reads as
		// zero rather than as no-data.
		`worklode_approval_flow_applied_total{outcome="error"} 0`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}
