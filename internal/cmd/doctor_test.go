package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if _, _, err := installGitHooks(repo); err != nil {
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
