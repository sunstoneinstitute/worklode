package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestListInboxProjectFilter(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "alpha", "AL", "acme/alpha-app")
	mapRepo(t, h, token, "beta", "BE", "acme/beta-app")
	seedIssue(t, st, "acme/alpha-app", 1, "issue")
	seedIssue(t, st, "acme/beta-app", 2, "issue")

	rec := doReq(t, h, http.MethodGet, "/api/v1/inbox?project=alpha", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list inbox: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Issues []struct {
			Repo   string `json:"repo"`
			Number int64  `json:"number"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Issues) != 1 || resp.Issues[0].Repo != "acme/alpha-app" {
		t.Fatalf("project=alpha returned %+v; want only acme/alpha-app#1", resp.Issues)
	}
}
