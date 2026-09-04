package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// setupCompletion stands up a task-list stub and a repo scoped to project
// "proj", chdir'd into. handler answers GET /api/v1/tasks.
func setupCompletion(t *testing.T, project string, handler http.HandlerFunc) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir repo config dir: %v", err)
	}
	cfg := ""
	if project != "" {
		cfg = "current_project = \"" + project + "\"\nproject_key = \"WL\"\n"
	}
	if err := os.WriteFile(filepath.Join(repo, ".worklode", "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
	t.Chdir(repo)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/tasks" {
			handler(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
}

// captureStderr redirects the process's stderr — where cobra.CompErrorln
// writes, straight into the prompt the user is typing — for the duration of
// the test, and returns what was written to it.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	return func() string {
		os.Stderr = orig
		w.Close()
		b, _ := io.ReadAll(r)
		r.Close()
		return string(b)
	}
}

func tasksResponse(ids ...string) model.TaskListResponse {
	var resp model.TaskListResponse
	for _, id := range ids {
		resp.Tasks = append(resp.Tasks, model.Task{ID: id, Project: "proj", Title: "t " + id})
	}
	return resp
}

// TestTaskIDCompletionSanitizesTitle is 061 §3 C3: a task title is free text
// and will eventually contain a tab or newline. Either would corrupt the
// "id\tdescription" line a shell splits on, so both are replaced before
// joining, and a long title is truncated to keep one candidate on one line.
func TestTaskIDCompletionSanitizesTitle(t *testing.T) {
	longTitle := strings.Repeat("x", candidateTitleWidth+20)
	setupCompletion(t, "proj", func(w http.ResponseWriter, r *http.Request) {
		var resp model.TaskListResponse
		resp.Tasks = []model.Task{
			{ID: "WL-1", Project: "proj", Title: "fix\tthe\nthing\r\n"},
			{ID: "WL-2", Project: "proj", Title: longTitle},
		}
		writeTestJSON(t, w, resp)
	})

	out, err := runLode(t, "__complete", "task", "show", "WL-")
	if err != nil {
		t.Fatalf("__complete task show: %v\noutput: %s", err, out)
	}
	got, _, _ := strings.Cut(out, ":")
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("candidates = %q, want exactly 2 lines", got)
	}
	if lines[0] != "WL-1\tfix the thing" {
		t.Fatalf("candidate[0] = %q, want tab/newline replaced with spaces", lines[0])
	}
	id, desc, ok := strings.Cut(lines[1], "\t")
	if !ok || id != "WL-2" {
		t.Fatalf("candidate[1] = %q, want a single id/description split on WL-2", lines[1])
	}
	if want := strings.Repeat("x", candidateTitleWidth-1) + "…"; desc != want {
		t.Fatalf("candidate[1] description = %q, want truncated to %d runes: %q", desc, candidateTitleWidth, want)
	}
	if strings.Contains(desc, "\t") || strings.Contains(desc, "\n") {
		t.Fatalf("candidate[1] description = %q, still contains a raw tab or newline", desc)
	}
}

// TestTaskIDCompletionOffersScopedIDsInOrder covers the happy path: the
// candidates are the project's tasks matching what has been typed, ordered by
// model.CompareTaskIDs (061 §4), so WL-9 precedes WL-10.
func TestTaskIDCompletionOffersScopedIDsInOrder(t *testing.T) {
	setupCompletion(t, "proj", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, tasksResponse("WL-10", "WL-9", "WL-91", "XX-1"))
	})

	out, err := runLode(t, "__complete", "task", "show", "WL-9")
	if err != nil {
		t.Fatalf("__complete task show: %v\noutput: %s", err, out)
	}
	got, _, _ := strings.Cut(out, ":")
	if got != "WL-9\tt WL-9\nWL-91\tt WL-91\n" {
		t.Fatalf("completion candidates = %q, want WL-9 then WL-91, each with its title", got)
	}
	if !strings.Contains(out, "ShellCompDirectiveNoFileComp") {
		t.Fatalf("directive not NoFileComp: %q", out)
	}
}

// TestTaskIDCompletionIsSilentOnFailure is 061 §3 C2: pressing TAB while
// logged out, offline, unscoped or against a slow server offers nothing and
// prints nothing — never ShellCompDirectiveError, never CompErrorln.
func TestTaskIDCompletionIsSilentOnFailure(t *testing.T) {
	slow := func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeTestJSON(t, w, tasksResponse("WL-1"))
	}
	tests := []struct {
		name    string
		project string
		timeout time.Duration
		handler http.HandlerFunc
	}{
		{name: "client error", project: "proj", handler: func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		}},
		{name: "no project scope", handler: func(w http.ResponseWriter, r *http.Request) {
			writeTestJSON(t, w, tasksResponse("WL-1"))
		}},
		{name: "past the deadline", project: "proj", timeout: 5 * time.Millisecond, handler: slow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupCompletion(t, tc.project, tc.handler)
			if tc.timeout != 0 {
				restore := completionTimeout
				completionTimeout = tc.timeout
				t.Cleanup(func() { completionTimeout = restore })
			}
			stderr := captureStderr(t)
			out, err := runLode(t, "__complete", "task", "show", "WL-")
			errText := stderr()
			if err != nil {
				t.Fatalf("__complete task show: %v\noutput: %s", err, out)
			}
			if candidates, _, _ := strings.Cut(out, ":"); candidates != "" {
				t.Fatalf("offered candidates %q, want none", candidates)
			}
			if !strings.Contains(out, "ShellCompDirectiveNoFileComp") {
				t.Fatalf("directive = %q, want NoFileComp", out)
			}
			if errText != "" {
				t.Fatalf("wrote %q to the user's prompt, want nothing", errText)
			}
		})
	}
}
