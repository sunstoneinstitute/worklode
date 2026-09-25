// pageacts_journey_test.go drives the document and task pages' Accept and
// Publish buttons (WL-901) through a live OIDC session, the way
// progress_journey_test.go drives the Progress page's writes.
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestDocAndTaskPageActsJourney(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
	createProject(t, st, "proj")

	dana := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "dana", "name": "Dana",
		"groups": []string{"user"}, "github_username": "danah",
	})
	erin := sessionFor(t, h, iss, map[string]any{
		"preferred_username": "erin", "name": "Erin",
		"groups": []string{"user"}, "github_username": "erinh",
	})
	token, err := st.CreateToken(context.Background(), "dana", "journey token", nil)
	if err != nil {
		t.Fatalf("create token for dana: %v", err)
	}
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "acts-plan", Body: progressPlanBody,
	})
	docPage := "/projects/proj/plan/" + strconv.Itoa(plan.Number)
	acceptRoute := `data-route="/projects/proj/progress/accept"`
	planBody := `{"doc":` + strconv.FormatInt(plan.ID, 10) + `}`

	// The owner sees Accept enabled; anyone else sees it disabled, naming the owner.
	body := pageAs(t, h, docPage, dana)
	for _, want := range []string{acceptRoute, `data-confirm="Accept `, `src="/assets/act.js`} {
		if !strings.Contains(body, want) {
			t.Fatalf("owner's draft plan page lacks %s:\n%s", want, body)
		}
	}
	if body := pageAs(t, h, docPage, erin); !strings.Contains(body, "is owned by dana") || strings.Contains(body, `data-confirm="Accept `) {
		t.Fatalf("non-owner's page should carry a disabled Accept naming dana:\n%s", body)
	}

	// The write gate holds: a request without the page header is refused.
	req := httptest.NewRequest(http.MethodPost, "/projects/proj/progress/accept", strings.NewReader(planBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: "wl_session", Value: dana})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("accept without the page header = %d, want 403", rr.Code)
	}

	// A non-owner's accept is the store's refusal, and nothing changes.
	if rr := jsonPost(t, h, "/projects/proj/progress/accept", erin, planBody); rr.Code != http.StatusForbidden {
		t.Fatalf("non-owner accept = %d, body %s; want 403", rr.Code, rr.Body.String())
	}
	if rr := jsonPost(t, h, "/projects/proj/progress/accept", dana, planBody); rr.Code != http.StatusOK {
		t.Fatalf("accept = %d, body %s", rr.Code, rr.Body.String())
	}

	// Accepting mints ready tasks, so the drafts the plan's bulk Publish
	// takes are two draft tasks linked to it afterwards.
	var drafts []string
	for _, title := range []string{"Draft one", "Draft two"} {
		task := createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": title, "priority": "medium", "kind": "chore", "draft": true})
		id := task["id"].(string)
		if rr := doReq(t, h, "PATCH", "/api/v1/tasks/"+id, token, map[string]any{"plan": "acts-plan"}); rr.Code != http.StatusOK {
			t.Fatalf("link %s to the plan = %d, body %s", id, rr.Code, rr.Body.String())
		}
		drafts = append(drafts, id)
	}
	body = pageAs(t, h, docPage, dana)
	if strings.Contains(body, acceptRoute) || !strings.Contains(body, "Publish 2 draft tasks") {
		t.Fatalf("accepted plan page should offer Publish, not Accept:\n%s", body)
	}

	first := drafts[0]
	taskBody := `{"task":"` + first + `"}`

	// The task page's Publish, then a stale second click.
	if body := pageAs(t, h, "/tasks/"+first, erin); !strings.Contains(body, `data-route="/projects/proj/tasks/publish"`) {
		t.Fatalf("draft task page lacks Publish:\n%s", body)
	}
	if rr := jsonPost(t, h, "/projects/proj/tasks/publish", erin, taskBody); rr.Code != http.StatusOK {
		t.Fatalf("publish = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := jsonPost(t, h, "/projects/proj/tasks/publish", erin, taskBody); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "not draft") {
		t.Fatalf("second publish = %d, body %s; want 409 not draft", rr.Code, rr.Body.String())
	}
	if body := pageAs(t, h, "/tasks/"+first, erin); strings.Contains(body, `/tasks/publish"`) {
		t.Fatalf("ready task page still offers Publish")
	}

	// The plan's bulk Publish takes the remaining draft, then has none left.
	bulk := `{"plan":` + strconv.FormatInt(plan.ID, 10) + `}`
	rr = jsonPost(t, h, "/projects/proj/tasks/publish", dana, bulk)
	if rr.Code != http.StatusOK {
		t.Fatalf("bulk publish = %d, body %s", rr.Code, rr.Body.String())
	}
	var got model.TaskPublishResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || len(got.Published) != 1 || got.Published[0] != drafts[1] {
		t.Fatalf("bulk publish reply = %s, %v; want [%s]", rr.Body.String(), err, drafts[1])
	}
	if rr := jsonPost(t, h, "/projects/proj/tasks/publish", dana, bulk); rr.Code != http.StatusConflict {
		t.Fatalf("second bulk publish = %d, body %s; want 409", rr.Code, rr.Body.String())
	}
	if body := pageAs(t, h, docPage, dana); strings.Contains(body, "draft task") {
		t.Fatalf("plan page still offers Publish after every task is ready")
	}

	// Another project's task is not found on this project's route.
	createProject(t, st, "other")
	if rr := jsonPost(t, h, "/projects/other/tasks/publish", dana, taskBody); rr.Code != http.StatusNotFound {
		t.Fatalf("publish via another project = %d, body %s; want 404", rr.Code, rr.Body.String())
	}
}

// pageAs GETs a cockpit page as a session, following one redirect to its
// canonical URL, and fails unless it renders.
func pageAs(t *testing.T, h http.Handler, path, session string) string {
	t.Helper()
	rr := withSession(t, h, "GET", path, session, "")
	if rr.Code == http.StatusFound {
		path = rr.Header().Get("Location")
		rr = withSession(t, h, "GET", path, session, "")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, body %s", path, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}
