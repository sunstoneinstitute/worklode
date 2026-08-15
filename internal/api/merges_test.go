package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedMergeRepo maps a repo to the test project so the report has something
// to attribute against.
func seedMergeRepo(t *testing.T, st *store.Store, project, repo string) {
	t.Helper()
	if err := st.AddRepo(context.Background(), project, repo); err != nil {
		t.Fatalf("AddRepo %s: %v", repo, err)
	}
}

// mergeResults decodes the results array into task -> result.
func mergeResults(t *testing.T, body map[string]any) map[string]string {
	t.Helper()
	raw, ok := body["results"].([]any)
	if !ok {
		t.Fatalf("no results array in %v", body)
	}
	out := map[string]string{}
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("result entry is not an object: %v", r)
		}
		out[m["task"].(string)] = m["result"].(string)
	}
	return out
}

// TestReportMergeAdvancesTask: the whole feature. A merge that never reached
// GitHub still closes the task.
func TestReportMergeAdvancesTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedMergeRepo(t, st, "proj", "acme/app")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Landed locally", "priority": "high", "kind": "feature"})

	rr := doReq(t, h, "POST", "/api/v1/merges", token, map[string]any{
		"repo": "git@github.com:acme/app.git", "sha": "abc1234", "tasks": []string{"WL-1"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("report merge status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := decodeMap(t, rr)
	// The remote URL was normalized to the stored owner/name form.
	if body["repo"] != "acme/app" {
		t.Fatalf("repo = %v, want acme/app", body["repo"])
	}
	if got := mergeResults(t, body)["WL-1"]; got != "advanced" {
		t.Fatalf("result = %q, want advanced", got)
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	if got := decodeMap(t, rr); got["state"] != "merged" {
		t.Fatalf("state = %v, want merged", got["state"])
	}
}

// TestReportMergeDuplicate: a second reporter of the same merge is the
// healthy case, not an error.
func TestReportMergeDuplicate(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedMergeRepo(t, st, "proj", "acme/app")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Landed twice", "priority": "high", "kind": "feature"})

	report := func() map[string]string {
		rr := doReq(t, h, "POST", "/api/v1/merges", token, map[string]any{
			"repo": "acme/app", "sha": "abc1234", "tasks": []string{"WL-1"},
		})
		if rr.Code != http.StatusOK {
			t.Fatalf("report merge status = %d, body %s", rr.Code, rr.Body.String())
		}
		return mergeResults(t, decodeMap(t, rr))
	}
	if got := report()["WL-1"]; got != "advanced" {
		t.Fatalf("first report = %q, want advanced", got)
	}
	if got := report()["WL-1"]; got != "duplicate" {
		t.Fatalf("second report = %q, want duplicate", got)
	}
}

// TestReportMergeUnknownTask: a laptop's guess about which branches landed
// can name a task this backbone has never heard of. That is a reported
// outcome, not a 4xx — the rest of the report must still land.
func TestReportMergeUnknownTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedMergeRepo(t, st, "proj", "acme/app")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Real", "priority": "high", "kind": "feature"})

	rr := doReq(t, h, "POST", "/api/v1/merges", token, map[string]any{
		"repo": "acme/app", "sha": "abc1234", "tasks": []string{"WL-1", "WL-404"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := mergeResults(t, decodeMap(t, rr))
	if got["WL-404"] != "unknown_task" {
		t.Fatalf("WL-404 = %q, want unknown_task", got["WL-404"])
	}
	if got["WL-1"] != "advanced" {
		t.Fatalf("WL-1 = %q, want advanced", got["WL-1"])
	}
}

func TestReportMergeValidation(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	seedMergeRepo(t, st, "proj", "acme/app")

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"no repo", map[string]any{"sha": "abc1234", "tasks": []string{"WL-1"}}},
		{"repo is not a repo", map[string]any{"repo": "nope", "sha": "abc1234", "tasks": []string{"WL-1"}}},
		{"no sha", map[string]any{"repo": "acme/app", "tasks": []string{"WL-1"}}},
		{"sha is not hex", map[string]any{"repo": "acme/app", "sha": "not-a-sha", "tasks": []string{"WL-1"}}},
		{"no tasks", map[string]any{"repo": "acme/app", "sha": "abc1234"}},
		{"empty tasks", map[string]any{"repo": "acme/app", "sha": "abc1234", "tasks": []string{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/merges", token, tc.body)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestReportMergeRequiresAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/merges", "", map[string]any{
		"repo": "acme/app", "sha": "abc1234", "tasks": []string{"WL-1"},
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rr.Code)
	}
}
