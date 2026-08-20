//go:build e2e

// crew_test.go proves the whole Crew journey (spec 029 §6.1, spec 032 §6)
// end to end, through public surfaces only: the bearer-token API and the
// read-only web UI. It never calls a store writer directly.
package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// findCrewMember returns the member with the given actor id in members, or
// fails the test.
func findCrewMember(t *testing.T, members []model.CrewMember, actor string) model.CrewMember {
	t.Helper()
	for _, m := range members {
		if m.Actor == actor {
			return m
		}
	}
	t.Fatalf("crew member %s not found in %+v", actor, members)
	return model.CrewMember{}
}

// crewHasMember reports whether members contains actor.
func crewHasMember(members []model.CrewMember, actor string) bool {
	for _, m := range members {
		if m.Actor == actor {
			return true
		}
	}
	return false
}

// TestCrewLifecycle proves the Crew journey end to end: add a lead and a
// member through the public API, see both on the JSON roster and the
// rendered page, prove the removal guard refuses while the member owns open
// work (naming the task), then prove it succeeds once the work is
// reassigned away, and prove the lead can never be removed at all (lead
// handoff is deliberately not implemented).
func TestCrewLifecycle(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken:      bootstrapToken,
		GitHubWebhookSecret: githubSecret,
		// This test drives web pages anonymously; without a login provider
		// the cockpit refuses to serve unless the deployment opted in.
		WebOpen: true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})

	// --- Step 1: project, two actors, both added to the Crew ---------------

	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "crewproj", Name: "Crew Proj", Key: "CRW",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "lucy", Kind: "human", DisplayName: "Lucy Lead",
	}); err != nil {
		t.Fatalf("create actor lucy: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "mo", Kind: "human", DisplayName: "Mo Member",
	}); err != nil {
		t.Fatalf("create actor mo: %v", err)
	}

	if _, _, err := admin.AddCrewMember(ctx, "crewproj", "lucy", "lead", true); err != nil {
		t.Fatalf("add lucy as lead: %v", err)
	}
	if _, _, err := admin.AddCrewMember(ctx, "crewproj", "mo", "member", false); err != nil {
		t.Fatalf("add mo as member: %v", err)
	}

	// --- Step 2: the participants API contract ------------------------------

	crew, _, err := admin.ListCrew(ctx, "crewproj")
	if err != nil {
		t.Fatalf("list crew: %v", err)
	}
	if len(crew) != 2 {
		t.Fatalf("crew = %+v, want exactly 2 members", crew)
	}
	lucy := findCrewMember(t, crew, "lucy")
	if !lucy.Lead {
		t.Fatalf("lucy.Lead = %v, want true", lucy.Lead)
	}
	if len(lucy.Roles) != 1 || lucy.Roles[0] != "lead" {
		t.Fatalf("lucy.Roles = %+v, want [lead]", lucy.Roles)
	}
	mo := findCrewMember(t, crew, "mo")
	if mo.Lead {
		t.Fatalf("mo.Lead = %v, want false", mo.Lead)
	}
	if len(mo.Roles) != 1 || mo.Roles[0] != "member" {
		t.Fatalf("mo.Roles = %+v, want [member]", mo.Roles)
	}

	// --- Step 3: the rendered roster page ------------------------------------

	code, body := getPage(t, srv.URL+"/projects/crewproj/crew")
	if code != http.StatusOK {
		t.Fatalf("GET /projects/crewproj/crew: status = %d, want 200", code)
	}
	if !strings.Contains(body, "Lucy Lead") {
		t.Fatalf("crew page missing display name Lucy Lead:\n%s", body)
	}
	if !strings.Contains(body, "Mo Member") {
		t.Fatalf("crew page missing display name Mo Member:\n%s", body)
	}
	if got := strings.Count(body, `<span class="chip lead">`); got != 1 {
		t.Fatalf("Lead badge count = %d, want exactly 1:\n%s", got, body)
	}
	if strings.Contains(body, "No Crew yet") {
		t.Fatalf("crew page renders the empty-state placeholder despite a populated roster:\n%s", body)
	}

	// --- Step 4: removal refused while the member owns open work ------------

	task, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "crewproj", Title: "Write the onboarding doc", Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, _, err := admin.AssignTask(ctx, task.ID, "mo"); err != nil {
		t.Fatalf("assign task to mo: %v", err)
	}

	_, err = admin.RemoveCrewMember(ctx, "crewproj", "mo")
	if err == nil {
		t.Fatal("remove mo while she owns an open task: want error, got success")
	}
	var clientErr *cli.ClientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("remove mo: error = %v (%T), want a *cli.ClientError", err, err)
	}
	if clientErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("remove mo: status = %d, want 422", clientErr.Status)
	}
	if !strings.Contains(clientErr.Msg, task.ID) {
		t.Fatalf("remove mo: error body = %q, want it to name the open task %s", clientErr.Msg, task.ID)
	}

	// --- Step 5: unassign, then removal succeeds -----------------------------

	if _, _, err := admin.UnassignTask(ctx, task.ID); err != nil {
		t.Fatalf("unassign task: %v", err)
	}
	if _, err := admin.RemoveCrewMember(ctx, "crewproj", "mo"); err != nil {
		t.Fatalf("remove mo after unassign: %v", err)
	}

	crew, _, err = admin.ListCrew(ctx, "crewproj")
	if err != nil {
		t.Fatalf("list crew after removing mo: %v", err)
	}
	if crewHasMember(crew, "mo") {
		t.Fatalf("crew = %+v, want mo gone", crew)
	}
	if !crewHasMember(crew, "lucy") {
		t.Fatalf("crew = %+v, want lucy still present", crew)
	}

	code, body = getPage(t, srv.URL+"/projects/crewproj/crew")
	if code != http.StatusOK {
		t.Fatalf("GET /projects/crewproj/crew after removing mo: status = %d, want 200", code)
	}
	if strings.Contains(body, "Mo Member") {
		t.Fatalf("crew page still shows Mo Member after her removal:\n%s", body)
	}
	if !strings.Contains(body, "Lucy Lead") {
		t.Fatalf("crew page missing Lucy Lead after mo's removal:\n%s", body)
	}
	if got := strings.Count(body, `<span class="chip lead">`); got != 1 {
		t.Fatalf("Lead badge count after mo's removal = %d, want exactly 1:\n%s", got, body)
	}

	// --- Step 6: the lead can never be removed -------------------------------

	_, err = admin.RemoveCrewMember(ctx, "crewproj", "lucy")
	if err == nil {
		t.Fatal("remove the lead: want error, got success")
	}
	if !errors.As(err, &clientErr) {
		t.Fatalf("remove the lead: error = %v (%T), want a *cli.ClientError", err, err)
	}
	if clientErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("remove the lead: status = %d, want 422", clientErr.Status)
	}
}
