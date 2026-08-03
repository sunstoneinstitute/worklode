package graphserver_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"LODE_GRAPHSERVER_URL", "LODE_GRAPHSERVER_TOKEN_URL",
		"LODE_GRAPHSERVER_CLIENT_ID", "LODE_GRAPHSERVER_CLIENT_SECRET",
	} {
		t.Setenv(k, "")
	}
}

func TestFromEnvUnset(t *testing.T) {
	clearEnv(t)
	if _, err := graphserver.FromEnv(); err == nil {
		t.Fatal("FromEnv without LODE_GRAPHSERVER_URL: want an error")
	}
}

func TestFromEnvBadURL(t *testing.T) {
	clearEnv(t)
	for _, bad := range []string{"graph.example", "://graph.example", "file:///tmp/x", "https://"} {
		t.Setenv("LODE_GRAPHSERVER_URL", bad)
		if _, err := graphserver.FromEnv(); err == nil {
			t.Errorf("FromEnv with LODE_GRAPHSERVER_URL=%q: want an error", bad)
		}
	}
}

func TestFromEnvPartialAuth(t *testing.T) {
	clearEnv(t)
	t.Setenv("LODE_GRAPHSERVER_URL", "https://graph.example")
	t.Setenv("LODE_GRAPHSERVER_CLIENT_ID", "dataplatform-svc")
	if _, err := graphserver.FromEnv(); err == nil {
		t.Fatal("FromEnv with a partial credential set: want an error")
	}
}

func TestFromEnvPartialAuthNamesMissingVar(t *testing.T) {
	clearEnv(t)
	t.Setenv("LODE_GRAPHSERVER_URL", "https://graph.example")
	t.Setenv("LODE_GRAPHSERVER_CLIENT_ID", "dataplatform-svc")
	t.Setenv("LODE_GRAPHSERVER_CLIENT_SECRET", "s3cret")
	_, err := graphserver.FromEnv()
	if err == nil {
		t.Fatal("FromEnv with a partial credential set: want an error")
	}
	if !strings.Contains(err.Error(), "LODE_GRAPHSERVER_TOKEN_URL") {
		t.Fatalf("error = %v; want it to name LODE_GRAPHSERVER_TOKEN_URL", err)
	}
	if strings.Contains(err.Error(), "LODE_GRAPHSERVER_CLIENT_ID") {
		t.Fatalf("error = %v; want it to not name LODE_GRAPHSERVER_CLIENT_ID, which was set", err)
	}
}

func TestFromEnvUnauthenticated(t *testing.T) {
	clearEnv(t)
	srv, rec := recordingServer(t, http.StatusCreated, "")
	t.Setenv("LODE_GRAPHSERVER_URL", srv.URL)
	c, err := graphserver.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if _, err := c.PutGraph(context.Background(), "main", graphIRI, nil); err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if rec.auth != "" {
		t.Fatalf("auth = %q; want none without token config", rec.auth)
	}
}

func TestFromEnvClientCredentials(t *testing.T) {
	clearEnv(t)
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q; want client_credentials", got)
		}
		id, sec, ok := r.BasicAuth()
		if !ok {
			id, sec = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		if id != "dataplatform-svc" || sec != "s3cret" {
			t.Errorf("credentials = %q/%q; want dataplatform-svc/s3cret", id, sec)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"cc-token","token_type":"Bearer","expires_in":3600}`)
	}))
	t.Cleanup(tok.Close)
	srv, rec := recordingServer(t, http.StatusCreated, "")
	t.Setenv("LODE_GRAPHSERVER_URL", srv.URL)
	t.Setenv("LODE_GRAPHSERVER_TOKEN_URL", tok.URL)
	t.Setenv("LODE_GRAPHSERVER_CLIENT_ID", "dataplatform-svc")
	t.Setenv("LODE_GRAPHSERVER_CLIENT_SECRET", "s3cret")
	c, err := graphserver.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if _, err := c.PutGraph(context.Background(), "main", graphIRI, nil); err != nil {
		t.Fatalf("PutGraph: %v", err)
	}
	if rec.auth != "Bearer cc-token" {
		t.Fatalf("auth = %q; want the client-credentials token", rec.auth)
	}
}
