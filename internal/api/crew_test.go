package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

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

// crewEvents polls for the recorded crew.member_added events, newest last,
// until at least want of them are readable. ListEvents is bounded by the
// cluster-wide commit horizon (see pollEvents in events_test.go), so a
// freshly committed event can take a moment to become visible.
func crewEvents(t *testing.T, st *store.Store, want int) []store.Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		events, err := st.ListEvents(context.Background(), store.EventFilter{Type: "crew.member_added"})
		if err != nil {
			t.Fatalf("list crew events: %v", err)
		}
		if len(events) >= want {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("crew.member_added events = %d after polling, want %d "+
				"(commit horizon held back by a concurrent transaction elsewhere on the instance?)",
				len(events), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAddCrewMemberAPI covers POST /api/v1/projects/{id}/participants: the
// 201 body is the member as the roster shows them (every role they hold, not
// just the one just added), the role defaults to "member", and the event the
// write is recorded under is spec 029 §8.4's crew.member_added.
func TestAddCrewMemberAPI(t *testing.T) {
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
	// carrying the payload spec 029 §8.4's subscribers read.
	events := crewEvents(t, st, 3)
	if len(events) != 3 {
		t.Fatalf("crew.member_added events = %d, want 3", len(events))
	}
	last := events[len(events)-1]
	if last.Source != "cli" {
		t.Errorf("event source = %q, want cli", last.Source)
	}
	var payload struct {
		Project string   `json:"project"`
		Actor   string   `json:"actor"`
		Roles   []string `json:"roles"`
		Lead    bool     `json:"lead"`
		By      string   `json:"by"`
	}
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
			map[string]any{"actor": "bob", "role": "co-lead", "lead": true}, http.StatusUnprocessableEntity, "already has a lead"},
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
	if !strings.Contains(metrics, `worklode_crew_changes_total{action="add",outcome="error",surface="web"} 0`) {
		t.Errorf("crew counter is not pre-initialised to zero:\n%s", metrics)
	}
}

// TestAddCrewMemberForm covers the roster page's own add affordance: a good
// submit lands the member and 303s back to the roster, and the write is
// recorded under the same event type from the "web" surface.
func TestAddCrewMemberForm(t *testing.T) {
	st, h, admin, _ := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	seedCrewActors(t, st, "ada")

	rr := doForm(t, h, "/projects/proj/crew",
		url.Values{"actor": {"ada"}, "role": {"editor"}, "lead": {"1"}}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/projects/proj/crew" {
		t.Fatalf("Location = %q, want /projects/proj/crew", loc)
	}

	page := doReq(t, h, "GET", "/projects/proj/crew", "", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("roster status = %d, want 200", page.Code)
	}
	bodyContains(t, page.Body.String(), "Ada Person", "editor", "Lead")

	events := crewEvents(t, st, 1)
	if len(events) != 1 {
		t.Fatalf("crew.member_added events = %d, want 1", len(events))
	}
	if events[0].Source != "web" {
		t.Errorf("event source = %q, want web", events[0].Source)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_crew_changes_total{action="add",outcome="ok",surface="web"} 1`) {
		t.Errorf("metrics missing the web add counter:\n%s", metrics)
	}
	if !strings.Contains(metrics, `worklode_web_form_submissions_total{form="crew_add",outcome="created"} 1`) {
		t.Errorf("metrics missing the crew_add form counter:\n%s", metrics)
	}
}

// TestAddCrewMemberFormRejected checks a refused submit comes back as the
// roster page with the message and everything that was typed — nothing
// typed is lost, and the refusal is not dressed up as an error page.
func TestAddCrewMemberFormRejected(t *testing.T) {
	st, h, admin, _ := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	seedCrewActors(t, st, "ada")

	if rr := doForm(t, h, "/projects/proj/crew",
		url.Values{"actor": {"ada"}, "role": {"editor"}, "lead": {"1"}}, nil); rr.Code != http.StatusSeeOther {
		t.Fatalf("seed add status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}

	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{"unknown actor", url.Values{"actor": {"nosuch"}, "role": {"reviewer"}}, "No actor with that id"},
		{"duplicate role", url.Values{"actor": {"ada"}, "role": {"editor"}}, "already holds role"},
		{"second lead", url.Values{"actor": {"ada"}, "role": {"co-lead"}, "lead": {"1"}}, "already has a lead"},
		{"no actor", url.Values{"actor": {"  "}, "role": {"reviewer"}}, "Actor is required."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doForm(t, h, "/projects/proj/crew", tc.form, nil)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
			}
			body := rr.Body.String()
			bodyContains(t, body, tc.want)
			// The typed values come back in the form, and the roster is
			// still rendered around it.
			bodyContains(t, body, `value="`+strings.TrimSpace(tc.form.Get("actor"))+`"`,
				`value="`+tc.form.Get("role")+`"`, "Ada Person")
			if tc.form.Get("lead") != "" && !strings.Contains(body, `id="lead" name="lead" type="checkbox" value="1" checked`) {
				t.Errorf("the lead checkbox did not come back checked:\n%s", body)
			}
		})
	}

	// Nothing was written by the four refusals: the roster still holds the
	// one seeded row.
	crew, err := st.ListParticipants(context.Background(), "proj")
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(crew) != 1 || len(crew[0].Roles) != 1 {
		t.Fatalf("crew = %+v, want one member with one role", crew)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_crew_changes_total{action="add",outcome="rejected",surface="web"} 4`) {
		t.Errorf("metrics missing the web rejected counter:\n%s", metrics)
	}
}

// TestAddCrewMemberFormCrossOrigin checks the write route is same-origin
// only, like the cockpit's other forms.
func TestAddCrewMemberFormCrossOrigin(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")
	seedCrewActors(t, st, "ada")

	rr := doForm(t, h, "/projects/proj/crew", url.Values{"actor": {"ada"}},
		map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
	crew, err := st.ListParticipants(context.Background(), "proj")
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(crew) != 0 {
		t.Fatalf("crew = %+v, want nothing written", crew)
	}
}
