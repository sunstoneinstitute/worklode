package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedCrewActors creates the actors the Crew tests add to a project. The
// display name is what the roster shows, so it differs from the id on
// purpose.
func seedCrewActors(t *testing.T, st *store.Store, ids ...string) {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		if err := st.CreateActor(ctx, id, "human", strings.ToUpper(id[:1])+id[1:]+" Person", false); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
}

// crewEvents polls for crew.member_added events.
func crewEvents(t *testing.T, st *store.Store, want int) []store.Event {
	t.Helper()
	return storeEventsOfType(t, st, "crew.member_added", want)
}

// crewPayload is the shape WL-REQ-126's subscribers read off both
// crew.member_added and crew.member_removed.
type crewPayload struct {
	Project string   `json:"project"`
	Actor   string   `json:"actor"`
	Roles   []string `json:"roles"`
	Lead    bool     `json:"lead"`
	By      string   `json:"by"`
}

// TestAddCrewMemberAPI covers POST /api/v1/projects/{id}/participants: the
// 201 body is the member as the roster shows them (every role they hold, not
// just the one just added), the role defaults to "member", and the event the
// write is recorded under is WL-REQ-126's crew.member_added.
func TestAddCrewMemberAPI(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	seedCrewActors(t, st, "ada", "bob")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/participants", token,
		map[string]any{"actor": "ada", "role": "editor", "lead": true})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	var member model.CrewMember
	if err := json.Unmarshal(rr.Body.Bytes(), &member); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	if member.Actor != "ada" || member.DisplayName != "Ada Person" || !member.Lead {
		t.Fatalf("member = %+v, want ada/Ada Person/lead", member)
	}
	if len(member.Roles) != 1 || member.Roles[0] != "editor" {
		t.Fatalf("roles = %v, want [editor]", member.Roles)
	}
	if member.AddedAt.IsZero() {
		t.Error("added_at is zero")
	}

	// A second role for the same actor answers with both roles: the response
	// is the roster's view of the member, not an echo of the request.
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/participants", token,
		map[string]any{"actor": "ada", "role": "reporter"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("second role status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &member); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	if strings.Join(member.Roles, ",") != "editor,reporter" {
		t.Fatalf("roles = %v, want [editor reporter]", member.Roles)
	}

	// An omitted role is "member", not a refusal.
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/participants", token,
		map[string]any{"actor": "bob"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("default role status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &member); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	if len(member.Roles) != 1 || member.Roles[0] != "member" {
		t.Fatalf("default roles = %v, want [member]", member.Roles)
	}

	// Every add is one crew.member_added event from the "cli" surface,
	// carrying the payload WL-REQ-126's subscribers read.
	events := crewEvents(t, st, 3)
	if len(events) != 3 {
		t.Fatalf("crew.member_added events = %d, want 3", len(events))
	}
	last := events[len(events)-1]
	if last.Source != "cli" {
		t.Errorf("event source = %q, want cli", last.Source)
	}
	var payload crewPayload
	if err := json.Unmarshal(last.Payload, &payload); err != nil {
		t.Fatalf("decode payload %s: %v", last.Payload, err)
	}
	if payload.Project != "proj" || payload.Actor != "bob" || payload.By != "alice" ||
		payload.Lead || len(payload.Roles) != 1 || payload.Roles[0] != "member" {
		t.Fatalf("payload = %+v, want project proj, actor bob, roles [member], by alice", payload)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_crew_changes_total{action="add",outcome="ok",surface="api"} 3`) {
		t.Errorf("metrics missing the api add counter:\n%s", metrics)
	}
}

// TestAddCrewMemberAPIRefusals covers every way an add is refused, and that
// each one is counted as "rejected" rather than as a fault.
func TestAddCrewMemberAPIRefusals(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	seedCrewActors(t, st, "ada", "bob")

	ok := doReq(t, h, "POST", "/api/v1/projects/proj/participants", token,
		map[string]any{"actor": "ada", "role": "editor", "lead": true})
	if ok.Code != http.StatusCreated {
		t.Fatalf("seed add status = %d; body %s", ok.Code, ok.Body.String())
	}

	cases := []struct {
		name string
		path string
		body map[string]any
		want int
		msg  string
	}{
		{"missing actor", "/api/v1/projects/proj/participants",
			map[string]any{"role": "editor"}, http.StatusUnprocessableEntity, "actor is required"},
		{"unknown actor", "/api/v1/projects/proj/participants",
			map[string]any{"actor": "nosuch"}, http.StatusNotFound, "not found"},
		{"unknown project", "/api/v1/projects/nosuch/participants",
			map[string]any{"actor": "ada"}, http.StatusNotFound, "not found"},
		{"duplicate role", "/api/v1/projects/proj/participants",
			map[string]any{"actor": "ada", "role": "editor"}, http.StatusUnprocessableEntity, "already holds role"},
		{"second lead", "/api/v1/projects/proj/participants",
			map[string]any{"actor": "bob", "role": "domain-expert", "lead": true}, http.StatusUnprocessableEntity, "already has a lead"},
		{"blank role", "/api/v1/projects/proj/participants",
			map[string]any{"actor": "bob", "role": "   "}, http.StatusCreated, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", tc.path, token, tc.body)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.want, rr.Body.String())
			}
			if tc.msg != "" && !strings.Contains(rr.Body.String(), tc.msg) {
				t.Fatalf("body = %s, want it to name %q", rr.Body.String(), tc.msg)
			}
		})
	}

	// Five refusals above (the blank role is the "" case: it defaults to
	// "member" and succeeds, so it is not counted here).
	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_crew_changes_total{action="add",outcome="rejected",surface="api"} 5`) {
		t.Errorf("metrics missing the rejected counter:\n%s", metrics)
	}
	// Pre-initialised, so an instance nobody has used reads as a flat zero.
	if !strings.Contains(metrics, `worklode_crew_changes_total{action="add",outcome="error",surface="api"} 0`) {
		t.Errorf("crew counter is not pre-initialised to zero:\n%s", metrics)
	}
}

// seedCrewTask creates one task in the project and assigns it to actor,
// through the public API, so the removal guard has real open work to find.
func seedCrewTask(t *testing.T, h http.Handler, token, project, actor, title string) string {
	t.Helper()
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": project, "title": title, "body": "b",
		"priority": "medium", "kind": "feature",
	})
	id, _ := task["id"].(string)
	if id == "" {
		t.Fatalf("created task has no id: %v", task)
	}
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/assign", token,
		map[string]any{"assignee": actor}); rr.Code != http.StatusOK {
		t.Fatalf("assign %s to %s: status %d, body %s", id, actor, rr.Code, rr.Body.String())
	}
	return id
}

// TestRemoveCrewMemberAPI covers DELETE
// /api/v1/projects/{id}/participants/{actor} in the order WL-REQ-123's
// rules fire: open work refuses with the items named, the lead is never
// removable, and a clean removal drops every role the member held.
func TestRemoveCrewMemberAPI(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	seedCrewActors(t, st, "ada", "bob")

	for _, body := range []map[string]any{
		{"actor": "ada", "role": "editor", "lead": true},
		{"actor": "bob", "role": "reporter"},
		{"actor": "bob", "role": "data-scientist"},
	} {
		if rr := doReq(t, h, "POST", "/api/v1/projects/proj/participants", token, body); rr.Code != http.StatusCreated {
			t.Fatalf("seed add %v: status %d, body %s", body, rr.Code, rr.Body.String())
		}
	}
	taskID := seedCrewTask(t, h, token, "proj", "bob", "bob's open task")

	// Open work refuses the removal, and the message carries the
	// responsibility list the caller has to act on.
	rr := doReq(t, h, "DELETE", "/api/v1/projects/proj/participants/bob", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blocked removal status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), taskID+" (task, ready)") {
		t.Fatalf("body = %s, want it to list %s (task, ready)", rr.Body.String(), taskID)
	}

	// The lead is never removable while lead handoff is unimplemented.
	rr = doReq(t, h, "DELETE", "/api/v1/projects/proj/participants/ada", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("lead removal status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "lead handoff is not implemented") {
		t.Fatalf("body = %s, want it to say why the lead cannot go", rr.Body.String())
	}

	// An actor who is not on the Crew at all.
	rr = doReq(t, h, "DELETE", "/api/v1/projects/proj/participants/nosuch", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown member status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}

	// With the work unassigned, the removal succeeds and takes both roles.
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+taskID+"/unassign", token, nil); rr.Code != http.StatusOK {
		t.Fatalf("unassign status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "DELETE", "/api/v1/projects/proj/participants/bob", token, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("removal status = %d, want 204; body %s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("204 carried a body: %s", rr.Body.String())
	}
	crew, err := st.ListParticipants(context.Background(), "proj")
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(crew) != 1 || crew[0].ActorID != "ada" {
		t.Fatalf("crew = %+v, want only ada", crew)
	}

	// The removal is one crew.member_removed event from the "cli" surface,
	// naming the roles it removed — the fact a subscriber cannot recover
	// once the rows are gone.
	events := storeEventsOfType(t, st, "crew.member_removed", 1)
	if len(events) != 1 {
		t.Fatalf("crew.member_removed events = %d, want 1", len(events))
	}
	if events[0].Source != "cli" {
		t.Errorf("event source = %q, want cli", events[0].Source)
	}
	var payload crewPayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload %s: %v", events[0].Payload, err)
	}
	if payload.Project != "proj" || payload.Actor != "bob" || payload.By != "alice" || payload.Lead {
		t.Fatalf("payload = %+v, want project proj, actor bob, by alice, not lead", payload)
	}
	if strings.Join(payload.Roles, ",") != "data-scientist,reporter" {
		t.Fatalf("payload roles = %v, want both roles that were removed", payload.Roles)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_crew_changes_total{action="remove",outcome="ok",surface="api"} 1`) {
		t.Errorf("metrics missing the api remove counter:\n%s", metrics)
	}
	if !strings.Contains(metrics, `worklode_crew_changes_total{action="remove",outcome="rejected",surface="api"} 3`) {
		t.Errorf("metrics missing the api remove rejections:\n%s", metrics)
	}
	// Pre-initialised, so an instance where nobody has removed anyone reads
	// as a flat zero rather than as no-data.
	if !strings.Contains(metrics, `worklode_crew_changes_total{action="remove",outcome="error",surface="api"} 0`) {
		t.Errorf("the remove action is not pre-initialised to zero:\n%s", metrics)
	}
}

// TestListCrewMembersAPI covers GET /api/v1/projects/{id}/participants: the
// roster comes back lead-first, each member's roles sorted, the empty case is
// an empty list (never null), and an unknown project 404s.
func TestListCrewMembersAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "empty")
	seedCrewActors(t, st, "ada", "bob")

	// An empty roster is [] on the wire, not null.
	rr := doReq(t, h, "GET", "/api/v1/projects/empty/participants", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("empty roster status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := strings.TrimSpace(rr.Body.String()); got != `{"participants":[]}` {
		t.Fatalf("empty roster body = %s, want {\"participants\":[]}", got)
	}

	for _, body := range []map[string]any{
		{"actor": "bob", "role": "reporter"},
		{"actor": "bob", "role": "editor"},
		{"actor": "ada", "role": "science-lead", "lead": true},
	} {
		if rr := doReq(t, h, "POST", "/api/v1/projects/proj/participants", token, body); rr.Code != http.StatusCreated {
			t.Fatalf("seed add %v: status %d, body %s", body, rr.Code, rr.Body.String())
		}
	}

	rr = doReq(t, h, "GET", "/api/v1/projects/proj/participants", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var resp model.ParticipantListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	if len(resp.Participants) != 2 {
		t.Fatalf("participants = %+v, want 2 members", resp.Participants)
	}
	// Lead first, regardless of add order.
	lead, other := resp.Participants[0], resp.Participants[1]
	if lead.Actor != "ada" || !lead.Lead || lead.DisplayName != "Ada Person" {
		t.Fatalf("first member = %+v, want ada, lead, Ada Person", lead)
	}
	if len(lead.Roles) != 1 || lead.Roles[0] != "science-lead" {
		t.Fatalf("lead roles = %v, want [science-lead]", lead.Roles)
	}
	if other.Actor != "bob" || other.Lead || other.DisplayName != "Bob Person" {
		t.Fatalf("second member = %+v, want bob, not lead, Bob Person", other)
	}
	// Roles come back sorted, not in add order (editor before reporter).
	if strings.Join(other.Roles, ",") != "editor,reporter" {
		t.Fatalf("bob roles = %v, want [editor reporter] sorted", other.Roles)
	}
	if lead.AddedAt.IsZero() || other.AddedAt.IsZero() {
		t.Errorf("added_at is zero: lead=%+v other=%+v", lead, other)
	}

	// Unknown project 404s.
	rr = doReq(t, h, "GET", "/api/v1/projects/nosuch/participants", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// validCrewRole mirrors the store vocabulary for the selection assertion
// above; an out-of-vocabulary submission has no option to re-select.
func validCrewRole(role string) bool {
	for _, r := range store.ParticipantRoles() {
		if r == role {
			return true
		}
	}
	return false
}

// TestListInstanceCrewAPI covers GET /api/v1/crew (WL-REQ-1795): the humans,
// in name order, over a bearer token and over a session cookie (WL-REQ-1307).
func TestListInstanceCrewAPI(t *testing.T) {
	t.Parallel()
	st, h, _, token := newTestServerWithAdmin(t)
	seedCrewActors(t, st, "bob", "ada")
	if err := st.CreateActor(context.Background(), "svc", "service", "Aaa Service", false); err != nil {
		t.Fatalf("create service actor: %v", err)
	}

	rr := doReq(t, h, "GET", "/api/v1/crew", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("bearer status = %d: %s", rr.Code, rr.Body)
	}
	var resp model.CrewListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var ids []string
	for _, m := range resp.Crew {
		if m.Actor == "svc" {
			t.Fatalf("service actor listed: %+v", resp.Crew)
		}
		ids = append(ids, m.Actor)
	}
	ia, ib := slices.Index(ids, "ada"), slices.Index(ids, "bob")
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("want ada before bob, got %v", ids)
	}

	_, h2, iss := newOIDCServer(t, api.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/crew", nil)
	req.AddCookie(loginSession(t, h2, iss, "alice"))
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"actor":"alice"`) {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
}
