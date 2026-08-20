package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestWhoami(t *testing.T) {
	_, h, token := newTestServer(t)

	rec := doReq(t, h, http.MethodGet, "/api/v1/whoami", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("whoami: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Admin bool   `json:"admin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" || got.Kind == "" || !got.Admin {
		t.Fatalf("whoami = %+v; want the bootstrap admin actor", got)
	}
}

func TestWhoamiRequiresAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	if rec := doReq(t, h, http.MethodGet, "/api/v1/whoami", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d; want 401", rec.Code)
	}
}

// TestWhoamiNonAdmin: permWhoAmI is granted to {RoleUser, RoleAdmin}, not
// admin-only — a non-admin token must still get 200 with admin: false, not
// 403.
func TestWhoamiNonAdmin(t *testing.T) {
	st, h, token := newTestServer(t)
	nonAdmin := makeNonAdminToken(t, st, h, token)

	rec := doReq(t, h, http.MethodGet, "/api/v1/whoami", nonAdmin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("whoami: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Admin bool   `json:"admin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "dev" || got.Kind != "human" || got.Admin {
		t.Fatalf("whoami = %+v; want the non-admin dev actor with admin: false", got)
	}
}

func TestReposDoctor(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "demo", "WL", "acme/app")

	// One pre-mapping-style unapplied event for the mapped repo, one event
	// from a repo nothing maps.
	seedGitHubEvent(t, st, "d-1", "push.ignored", `{"repository":{"full_name":"acme/app"}}`)
	seedGitHubEvent(t, st, "d-2", "push.ignored", `{"repository":{"full_name":"acme/unmapped"}}`)

	rec := doReq(t, h, http.MethodGet, "/api/v1/repos/doctor", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("repos doctor: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Repos []struct {
			Repo            string  `json:"repo"`
			Project         string  `json:"project"`
			AppInstalled    *bool   `json:"app_installed"`
			LastEventAt     *string `json:"last_event_at"`
			UnappliedEvents int     `json:"unapplied_events"`
			Stale           bool    `json:"stale"`
		} `json:"repos"`
		UnmappedSenders []struct {
			Repo   string `json:"repo"`
			Events int    `json:"events"`
		} `json:"unmapped_senders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Repos) != 1 || resp.Repos[0].Repo != "acme/app" {
		t.Fatalf("repos = %+v; want acme/app", resp.Repos)
	}
	r := resp.Repos[0]
	if r.AppInstalled != nil {
		t.Fatalf("app_installed = %v; want null (app auth unconfigured in tests)", *r.AppInstalled)
	}
	if r.UnappliedEvents != 1 {
		t.Fatalf("unapplied = %d; want 1", r.UnappliedEvents)
	}
	if len(resp.UnmappedSenders) != 1 || resp.UnmappedSenders[0].Repo != "acme/unmapped" {
		t.Fatalf("unmapped senders = %+v; want acme/unmapped", resp.UnmappedSenders)
	}
}

// TestReposDoctorStale: a mapped repo with no deliveries at all is stale —
// the signal that sends an operator to lode reconcile.
func TestReposDoctorStale(t *testing.T) {
	_, h, token := newTestServer(t)
	mapRepo(t, h, token, "demo", "WL", "acme/silent")

	rec := doReq(t, h, http.MethodGet, "/api/v1/repos/doctor?repo=acme/silent", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("repos doctor: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Repos []struct {
			Stale bool `json:"stale"`
		} `json:"repos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Repos) != 1 || !resp.Repos[0].Stale {
		t.Fatalf("repos = %+v; want one stale repo", resp.Repos)
	}
}

func TestReposDoctorRequiresAdmin(t *testing.T) {
	st, h, token := newTestServer(t)
	nonAdmin := makeNonAdminToken(t, st, h, token)
	if rec := doReq(t, h, http.MethodGet, "/api/v1/repos/doctor", nonAdmin, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d; want 403", rec.Code)
	}
}

func TestReconcileReplaysIgnoredEvents(t *testing.T) {
	st, h, token := newTestServer(t)
	// Delivery recorded before mapping...
	seedGitHubEvent(t, st, "d-1", "issues.opened.ignored", `{
		"action": "opened",
		"repository": {"full_name": "acme/app"},
		"issue": {"number": 7, "title": "late", "state": "open", "html_url": "u"}
	}`)
	// ...then the repo is mapped.
	mapRepo(t, h, token, "demo", "WL", "acme/app")

	rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", token, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		RunID  string `json:"run_id"`
		DryRun bool   `json:"dry_run"`
		Replay struct {
			Candidates int `json:"candidates"`
			Replayed   int `json:"replayed"`
		} `json:"replay"`
		PollSkipped string `json:"poll_skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RunID == "" || resp.Replay.Replayed != 1 {
		t.Fatalf("response = %+v; want a run id and 1 replayed", resp)
	}
	if resp.PollSkipped == "" {
		t.Fatalf("poll_skipped empty; want the no-github-app explanation")
	}
}

func TestReconcileValidation(t *testing.T) {
	_, h, token := newTestServer(t)
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"repo and task together", map[string]any{"repo": "a/b", "task": "WL-1"}, http.StatusUnprocessableEntity},
		{"bad since", map[string]any{"since": "yesterday-ish"}, http.StatusUnprocessableEntity},
		{"duration since", map[string]any{"since": "720h", "dry_run": true}, http.StatusOK},
		{"rfc3339 since", map[string]any{"since": "2026-07-01T00:00:00Z", "dry_run": true}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", token, tc.body); rec.Code != tc.want {
				t.Fatalf("%s: %d %s; want %d", tc.name, rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

func TestReconcileRequiresAdmin(t *testing.T) {
	st, h, token := newTestServer(t)
	nonAdmin := makeNonAdminToken(t, st, h, token)
	if rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", nonAdmin, map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d; want 403", rec.Code)
	}
}

// seedGitHubEvent records one github event with a nil apply (applied_at NULL).
func seedGitHubEvent(t *testing.T, st *store.Store, externalID, typ, payload string) {
	t.Helper()
	if _, _, err := st.RecordEvent(context.Background(), "github", externalID, typ,
		[]byte(payload), nil); err != nil {
		t.Fatalf("seed event %s: %v", externalID, err)
	}
}

// makeNonAdminToken creates a non-admin actor and mints a token for it via
// the admin API.
func makeNonAdminToken(t *testing.T, st *store.Store, h http.Handler, adminToken string) string {
	t.Helper()
	rec := doReq(t, h, http.MethodPost, "/api/v1/actors", adminToken,
		map[string]any{"id": "dev", "kind": "human", "display_name": "Dev", "admin": false})
	if rec.Code >= 300 {
		t.Fatalf("create actor: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/actors/dev/tokens", adminToken,
		map[string]any{"description": "test"})
	if rec.Code >= 300 {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body.String())
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil || tok.Token == "" {
		t.Fatalf("decode token: %v (%s)", err, rec.Body.String())
	}
	return tok.Token
}
