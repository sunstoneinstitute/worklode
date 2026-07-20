package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/sunstoneinstitute/work-tracker/internal/cli"
)

func TestRunLoginServerMediated(t *testing.T) {
	// Stub work-tracker server: discovery + token exchange.
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/wt-login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"authorize_url": "http://" + r.Host + "/auth/cli/login",
			"token_url":     "http://" + r.Host + "/auth/cli/token",
			"providers":     []string{"github"},
		})
	})
	mux.HandleFunc("/auth/cli/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "THECODE" || body["state"] == "" {
			http.Error(w, `{"error":"bad code"}`, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"token": "wt_minted", "actor_id": "github:7", "expires_at": "2026-08-19T00:00:00Z",
		})
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	// Browser stub: parse redirect_uri + state from the authorize URL and hit
	// the loopback with a code, as the server would after a web login.
	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		cb := q.Get("redirect_uri") + "?code=THECODE&state=" + url.QueryEscape(q.Get("state"))
		go http.Get(cb)
		return nil
	}

	res, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: openBrowser,
	})
	if err != nil {
		t.Fatalf("RunLogin: %v", err)
	}
	if res.Token != "wt_minted" || res.ActorID != "github:7" {
		t.Fatalf("result = %+v; want wt_minted/github:7", res)
	}
}

func TestRunLoginNoInteractiveLogin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/wt-login", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no interactive login configured", http.StatusNotFound)
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	_, err := cli.RunLogin(context.Background(), cli.LoginOptions{
		Server: wt.URL, OpenBrowser: func(string) error { return nil },
	})
	if err == nil {
		t.Fatal("expected error when server has no interactive login")
	}
}

func TestRunLoginStateMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/wt-login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"authorize_url": "http://" + r.Host + "/auth/cli/login",
			"token_url":     "http://" + r.Host + "/auth/cli/token",
		})
	})
	wt := httptest.NewServer(mux)
	defer wt.Close()

	openBrowser := func(authURL string) error {
		u, _ := url.Parse(authURL)
		cb := u.Query().Get("redirect_uri") + "?code=X&state=WRONG"
		go http.Get(cb)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := cli.RunLogin(ctx, cli.LoginOptions{Server: wt.URL, OpenBrowser: openBrowser})
	if err == nil {
		t.Fatal("expected state-mismatch error")
	}
}
