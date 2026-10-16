package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestProjectSummaryAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/summary", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body)
	}
	var got model.ProjectSummary
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rr.Body, err)
	}
	if got.PastDue == nil || got.AwaitingReview == nil || got.Workloads == nil {
		t.Errorf("lists must be [] not null: %s", rr.Body)
	}

	if rr := doReq(t, h, "GET", "/api/v1/projects/nosuch/summary", token, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d, want 404; body %s", rr.Code, rr.Body)
	}
}

func TestProjectSummaryAPIAuth(t *testing.T) {
	st, h, iss := newOIDCServer(t, api.Config{})
	createProject(t, st, "proj")

	// Session cookie.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/proj/summary", nil)
	req.AddCookie(loginSession(t, h, iss, "alice"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie: %d %s", rec.Code, rec.Body)
	}

	// Bearer token.
	token := seedActor(t, st, "bob", "human", "Bob", false)
	if rr := doReq(t, h, "GET", "/api/v1/projects/proj/summary", token, nil); rr.Code != http.StatusOK {
		t.Fatalf("bearer: %d %s", rr.Code, rr.Body)
	}

	// Neither.
	if rr := doReq(t, h, "GET", "/api/v1/projects/proj/summary", "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rr.Code)
	}
}
