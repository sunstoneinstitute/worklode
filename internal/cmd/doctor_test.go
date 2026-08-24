package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/githooks"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

// fakeLode serves whoami and the project list the way lode doctor consumes
// them, and points LODE_SERVER/LODE_TOKEN at itself.
func fakeLode(t *testing.T, whoamiStatus int, projects string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/whoami":
			w.WriteHeader(whoamiStatus)
			if whoamiStatus == http.StatusOK {
				io.WriteString(w, `{"id":"stig","kind":"human","admin":true}`)
			} else {
				io.WriteString(w, `{"error":"unauthorized"}`)
			}
		case "/api/v1/projects":
			io.WriteString(w, `{"projects":[`+projects+`]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
}

func TestDoctorHealthySetup(t *testing.T) {
	repo := setupRepoConfig(t, "demo")
	initGitRepoInDir(t, repo)
	fakeLode(t, http.StatusOK, `{"id":"demo","name":"Demo","key":"WL","repos":[],"focus":[]}`)
	if _, _, err := githooks.Install(repo); err != nil {
		t.Fatalf("install hooks: %v", err)
	}

	out, err := runLode(t, "doctor")
	if err != nil {
		t.Fatalf("healthy doctor exited non-zero: %v\n%s", err, out)
	}
	for _, want := range []string{"config", "server", "token", "current_project", "hooks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q check:\n%s", want, out)
		}
	}
}

func TestDoctorFailuresNameTheirFix(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T)
		wantFix string
	}{
		{
			name: "missing config",
			setup: func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				t.Chdir(t.TempDir())
				t.Setenv("LODE_SERVER", "")
				t.Setenv("LODE_TOKEN", "")
			},
			wantFix: "config.toml",
		},
		{
			name: "unreachable server",
			setup: func(t *testing.T) {
				setupRepoConfig(t, "demo")
				// A closed port: connection refused, not a slow timeout.
				t.Setenv("LODE_SERVER", "http://127.0.0.1:1")
				t.Setenv("LODE_TOKEN", "wl_test")
			},
			wantFix: "server",
		},
		{
			name: "invalid token",
			setup: func(t *testing.T) {
				setupRepoConfig(t, "demo")
				fakeLode(t, http.StatusUnauthorized, ``)
			},
			wantFix: "lode login",
		},
		{
			name: "unset current_project",
			setup: func(t *testing.T) {
				setupRepoConfig(t, "")
				fakeLode(t, http.StatusOK, ``)
			},
			wantFix: "current_project",
		},
		{
			name: "missing git hooks",
			setup: func(t *testing.T) {
				repo := setupRepoConfig(t, "demo")
				initGitRepoInDir(t, repo)
				fakeLode(t, http.StatusOK, `{"id":"demo","name":"Demo","key":"WL","repos":[],"focus":[]}`)
			},
			wantFix: "lode install",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			out, err := runLode(t, "doctor")
			if err == nil {
				t.Fatalf("doctor exited zero on a broken setup:\n%s", out)
			}
			if !strings.Contains(out, tc.wantFix) {
				t.Fatalf("doctor output does not name the fix %q:\n%s", tc.wantFix, out)
			}
		})
	}
}

// secretsDoctorServer is a fake backbone for the secrets sweep: whoami and
// the project list keep the earlier checks green, and taskStatus maps a task
// id to the status GET /api/v1/tasks/{id} answers with. A body is served only
// for 200s; leased names the ids that answer with a live lease.
func secretsDoctorServer(t *testing.T, taskStatus map[string]int, leased map[string]bool) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/whoami":
			io.WriteString(w, `{"id":"stig","kind":"human","admin":true}`)
		case r.URL.Path == "/api/v1/projects":
			io.WriteString(w, `{"projects":[{"id":"demo","name":"Demo","key":"WL","repos":[],"focus":[]}]}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/tasks/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
			status, known := taskStatus[id]
			if !known {
				status = http.StatusNotFound
			}
			w.WriteHeader(status)
			switch {
			case status != http.StatusOK:
				io.WriteString(w, `{"error":"nope"}`)
			case leased[id]:
				io.WriteString(w, `{"id":"`+id+`","lease":{"task_id":"`+id+
					`","actor_id":"stig","expires_at":"2099-01-01T00:00:00Z"}}`)
			default:
				io.WriteString(w, `{"id":"`+id+`"}`)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
}

// materialize fakes a completed claim-time ceremony for taskID: a keystore
// item plus the manifest that records it.
func materialize(t *testing.T, taskID string) {
	t.Helper()
	if err := secrets.Put(taskID, "A_TOKEN", "v-"+taskID); err != nil {
		t.Fatalf("put %s: %v", taskID, err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{
		Task: taskID, Materialized: []string{"A_TOKEN"}, At: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save manifest %s: %v", taskID, err)
	}
}

func materialized(t *testing.T, taskID string) bool {
	t.Helper()
	_, ok := secrets.LoadManifest(taskID)
	return ok
}

// TestDoctorSweepsSecretsForGoneLeases: the lease-expiry sweeper is
// server-side and cannot reach a laptop keystore, so `lode doctor` is what
// reaps a worktree that was abandoned rather than removed (017 §3). A task
// the backbone answers for definitively — no lease, or gone entirely — is
// purged; one with a live lease is left alone.
func TestDoctorSweepsSecretsForGoneLeases(t *testing.T) {
	keyring.MockInit()
	repo := setupRepoConfig(t, "demo")
	initGitRepoInDir(t, repo)
	if _, _, err := githooks.Install(repo); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	secretsDoctorServer(t,
		map[string]int{"WL-7": http.StatusOK, "WL-8": http.StatusOK, "WL-9": http.StatusNotFound},
		map[string]bool{"WL-8": true})

	for _, id := range []string{"WL-7", "WL-8", "WL-9"} {
		materialize(t, id)
	}

	out, err := runLode(t, "doctor")
	if err != nil {
		t.Fatalf("doctor exited non-zero: %v\n%s", err, out)
	}
	if materialized(t, "WL-7") {
		t.Errorf("WL-7 has no lease and was not purged:\n%s", out)
	}
	if materialized(t, "WL-9") {
		t.Errorf("WL-9 is gone from the backbone and was not purged:\n%s", out)
	}
	if !materialized(t, "WL-8") {
		t.Errorf("WL-8 holds a live lease and must not be purged:\n%s", out)
	}
	if v, err := secrets.Fetch("WL-7", "A_TOKEN"); err == nil {
		t.Errorf("WL-7 keystore item survived the purge with value %q", v)
	}
	if !strings.Contains(out, "WL-7") || !strings.Contains(out, "WL-9") {
		t.Errorf("doctor swept WL-7 and WL-9 without saying so:\n%s", out)
	}
}

// TestDoctorNeverPurgesOnUncertainty: "is the lease gone" is a backbone
// question asked from a laptop, so it is three-valued. Only a definite answer
// may purge — a secret wrongly reaped costs a fresh consent and Touch ID,
// while one reaped a run later costs nothing.
func TestDoctorNeverPurgesOnUncertainty(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
	}{
		{
			name: "server unreachable",
			setup: func(t *testing.T) {
				// A closed port: connection refused, not a slow timeout.
				t.Setenv("LODE_SERVER", "http://127.0.0.1:1")
				t.Setenv("LODE_TOKEN", "wl_test")
			},
		},
		{
			name: "task lookup fails",
			setup: func(t *testing.T) {
				secretsDoctorServer(t, map[string]int{"WL-7": http.StatusInternalServerError}, nil)
			},
		},
		{
			name: "token rejected",
			setup: func(t *testing.T) {
				secretsDoctorServer(t, map[string]int{"WL-7": http.StatusUnauthorized}, nil)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyring.MockInit()
			setupRepoConfig(t, "demo")
			tc.setup(t)
			materialize(t, "WL-7")

			out, _ := runLode(t, "doctor")
			if !materialized(t, "WL-7") {
				t.Fatalf("doctor purged WL-7 without a definite answer about its lease:\n%s", out)
			}
		})
	}
}
