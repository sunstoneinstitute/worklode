package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
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
