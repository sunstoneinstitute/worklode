package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// mapRepo creates a project and maps a repo to it.
func mapRepo(t *testing.T, h http.Handler, token, project, key, repo string) {
	t.Helper()
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": project, "name": project, "key": key})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/"+project+"/repos", token,
		map[string]string{"repo": repo})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add repo: %d %s", rec.Code, rec.Body.String())
	}
}

func TestResolveRemoteFindsProject(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)
	mapRepo(t, h, token, "worklode", "WL", "sunstoneinstitute/worklode")

	for _, remote := range []string{
		"git@github.com:sunstoneinstitute/worklode.git",
		"https://github.com/sunstoneinstitute/worklode",
		"sunstoneinstitute/worklode",
	} {
		rec := doReq(t, h, http.MethodGet,
			"/api/v1/projects/resolve?remote="+url.QueryEscape(remote), token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("resolve %q: %d %s", remote, rec.Code, rec.Body.String())
		}
		var got struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != "worklode" || got.Key != "WL" {
			t.Fatalf("resolve %q = %+v; want worklode/WL", remote, got)
		}
	}
}

func TestResolveRemoteUnmapped(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodGet,
		"/api/v1/projects/resolve?remote="+url.QueryEscape("git@github.com:acme/nope.git"), token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unmapped repo: %d %s; want 404", rec.Code, rec.Body.String())
	}
}

func TestResolveRemoteInvalid(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)
	for _, remote := range []string{"", "worklode", "https://github.com/a/b/c"} {
		rec := doReq(t, h, http.MethodGet,
			"/api/v1/projects/resolve?remote="+url.QueryEscape(remote), token, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("remote %q: %d %s; want 422", remote, rec.Code, rec.Body.String())
		}
	}
}

func TestResolveRemoteRequiresAuth(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)
	rec := doReq(t, h, http.MethodGet,
		"/api/v1/projects/resolve?remote=a%2Fb", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d; want 401", rec.Code)
	}
}
