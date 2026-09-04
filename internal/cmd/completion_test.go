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
	setupCompletionRoutes(t, project, map[string]http.HandlerFunc{"/api/v1/tasks": handler})
}

// setupCompletionRoutes is setupCompletion over more than one endpoint: the
// doc, project and Crew completers each read a different path, and the
// `lode show` union reads two at once. A path routes have no entry for
// answers 404, which is what a completer must survive silently.
func setupCompletionRoutes(t *testing.T, project string, routes map[string]http.HandlerFunc) {
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
		if h, ok := routes[r.URL.Path]; ok {
			h(w, r)
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

// TestTaskIDCompletionFiresAtTheRightPosition is 061 §3 C1 for the commands
// whose task id is not the first argument. Wiring a completion function that
// only ever fires at position 0 would leave `lode task set state merged WL-…`
// silently uncompletable while still looking wired, so the position is a
// property of the wiring (taskIDAt/taskIDLast) and is checked here per shape:
// ref-first, ref-mid, and the trailing ref of `task set`.
func TestTaskIDCompletionFiresAtTheRightPosition(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "attach, ref first", args: []string{"task", "attach", "WL-"}, want: true},
		{name: "attach, file argument is not a ref", args: []string{"task", "attach", "WL-1", "WL-"}},
		{name: "detach, ref first", args: []string{"task", "detach", "WL-"}, want: true},
		{name: "assign, ref first", args: []string{"task", "assign", "WL-"}, want: true},
		{name: "inbox link, ref third", args: []string{"inbox", "link", "acme/repo", "7", "WL-"}, want: true},
		{name: "inbox link, repo argument is not a ref", args: []string{"inbox", "link", "WL-"}},
		{name: "set state, ref last", args: []string{"task", "set", "state", "merged", "WL-"}, want: true},
		{name: "set checklist, ref last", args: []string{"task", "set", "checklist", "0", "true", "WL-"}, want: true},
		{name: "set, value argument is not a ref", args: []string{"task", "set", "state", "WL-"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupCompletion(t, "proj", func(w http.ResponseWriter, r *http.Request) {
				writeTestJSON(t, w, tasksResponse("WL-1"))
			})
			out, err := runLode(t, append([]string{"__complete"}, tc.args...)...)
			if err != nil {
				t.Fatalf("__complete %v: %v\noutput: %s", tc.args, err, out)
			}
			got, _, _ := strings.Cut(out, ":")
			if tc.want && got != "WL-1\tt WL-1\n" {
				t.Fatalf("candidates = %q, want the project's task ids", got)
			}
			if !tc.want && got != "" {
				t.Fatalf("candidates = %q, want none at this position", got)
			}
		})
	}
}

// docsResponse builds a doc list stub. Every entry is a spec in project key
// "WL", so cli.DocRef renders the WL-SPEC-<n> shorthand the completer offers
// alongside the slug.
func docsResponse(docs ...model.Doc) model.DocListResponse {
	for i := range docs {
		docs[i].Project, docs[i].ProjectKey, docs[i].Kind = "proj", "WL", "spec"
	}
	return model.DocListResponse{Docs: docs}
}

// TestDocRefCompletionOffersSlugAndShorthand is 061 §3 C1 for documents: a
// document is named either way (026 §4.2), so both are candidates, ordered by
// the shorthand's numeric suffix rather than lexically — WL-SPEC-9 before
// WL-SPEC-10, the same discipline task ids get.
func TestDocRefCompletionOffersSlugAndShorthand(t *testing.T) {
	docs := func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, docsResponse(
			model.Doc{Number: 10, Slug: "ten-doc", Title: "ten"},
			model.Doc{Number: 9, Slug: "nine-doc", Title: "nine"},
		))
	}
	setupCompletionRoutes(t, "proj", map[string]http.HandlerFunc{"/api/v1/docs": docs})

	out, err := runLode(t, "__complete", "doc", "show", "WL-SPEC-")
	if err != nil {
		t.Fatalf("__complete doc show: %v\noutput: %s", err, out)
	}
	got, _, _ := strings.Cut(out, ":")
	if got != "WL-SPEC-9\tnine\nWL-SPEC-10\tten\n" {
		t.Fatalf("shorthand candidates = %q, want WL-SPEC-9 then WL-SPEC-10", got)
	}

	out, err = runLode(t, "__complete", "doc", "show", "nine")
	if err != nil {
		t.Fatalf("__complete doc show: %v\noutput: %s", err, out)
	}
	got, _, _ = strings.Cut(out, ":")
	if got != "nine-doc\tnine\n" {
		t.Fatalf("slug candidates = %q, want the matching slug", got)
	}
}

// TestDocUndeleteCompletesTombstonedDocs: `lode doc undelete` acts only on
// documents that have left every live list, so completing it from the live
// corpus would offer exactly the wrong set.
func TestDocUndeleteCompletesTombstonedDocs(t *testing.T) {
	var gotDeleted string
	setupCompletionRoutes(t, "proj", map[string]http.HandlerFunc{
		"/api/v1/docs": func(w http.ResponseWriter, r *http.Request) {
			gotDeleted = r.URL.Query().Get("deleted")
			writeTestJSON(t, w, docsResponse(model.Doc{Number: 3, Slug: "gone-doc", Title: "gone"}))
		},
	})

	out, err := runLode(t, "__complete", "doc", "undelete", "gone")
	if err != nil {
		t.Fatalf("__complete doc undelete: %v\noutput: %s", err, out)
	}
	if gotDeleted != "true" {
		t.Fatalf("doc list ?deleted = %q, want the tombstoned corpus", gotDeleted)
	}
	if got, _, _ := strings.Cut(out, ":"); got != "gone-doc\tgone\n" {
		t.Fatalf("candidates = %q, want the tombstoned document", got)
	}
}

// TestProjectKeyCompletionNeedsNoProjectScope is the reason project lookups
// do not go through completionScope: GET /api/v1/projects is global, and a
// checkout with no project scoped is exactly where a user reaches for the
// list of them.
func TestProjectKeyCompletionNeedsNoProjectScope(t *testing.T) {
	setupCompletionRoutes(t, "", map[string]http.HandlerFunc{
		"/api/v1/projects": func(w http.ResponseWriter, r *http.Request) {
			writeTestJSON(t, w, model.ProjectListResponse{Projects: []model.Project{
				{ID: "worklode", Name: "Worklode", Key: "WL"},
				{ID: "acme", Name: "Acme", Key: "AC"},
			}})
		},
	})

	// `project focus`, not `project crew`: cobra offers a group's subcommand
	// names alongside the ValidArgsFunction's candidates, which would make the
	// assertion about cobra rather than about the lookup.
	out, err := runLode(t, "__complete", "project", "focus", "")
	if err != nil {
		t.Fatalf("__complete project focus: %v\noutput: %s", err, out)
	}
	got, _, _ := strings.Cut(out, ":")
	if got != "acme\tAcme\nworklode\tWorklode\n" {
		t.Fatalf("candidates = %q, want both project ids in lexical order", got)
	}
}

// TestActorCompletionReadsTheNamedProjectsCrew: the actor of
// `lode project crew add|remove <project> <actor>` comes from the roster of
// the project named in the previous argument, not from the scoped one — the
// two are routinely different.
func TestActorCompletionReadsTheNamedProjectsCrew(t *testing.T) {
	setupCompletionRoutes(t, "proj", map[string]http.HandlerFunc{
		"/api/v1/projects/acme/participants": func(w http.ResponseWriter, r *http.Request) {
			writeTestJSON(t, w, model.ParticipantListResponse{Participants: []model.CrewMember{
				{Actor: "bob", DisplayName: "Bob"},
				{Actor: "ada", DisplayName: "Ada"},
			}})
		},
	})

	for _, verb := range []string{"add", "remove"} {
		t.Run(verb, func(t *testing.T) {
			out, err := runLode(t, "__complete", "project", "crew", verb, "acme", "")
			if err != nil {
				t.Fatalf("__complete project crew %s: %v\noutput: %s", verb, err, out)
			}
			got, _, _ := strings.Cut(out, ":")
			if got != "ada\tAda\nbob\tBob\n" {
				t.Fatalf("candidates = %q, want acme's Crew", got)
			}
		})
	}
}

// TestShowCompletionUnionsTasksAndDocs covers the polymorphic argument of
// `lode show`: classify() routes a positional to a task or a document, so the
// candidates are both kinds at once. The second case is why the two lookups
// run concurrently and independently — one failing must not take the other's
// candidates with it.
func TestShowCompletionUnionsTasksAndDocs(t *testing.T) {
	tasks := func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, tasksResponse("WL-1"))
	}
	docs := func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, docsResponse(model.Doc{Number: 2, Slug: "a-doc", Title: "a doc"}))
	}
	broken := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}

	t.Run("both kinds", func(t *testing.T) {
		setupCompletionRoutes(t, "proj", map[string]http.HandlerFunc{
			"/api/v1/tasks": tasks, "/api/v1/docs": docs,
		})
		out, err := runLode(t, "__complete", "show", "")
		if err != nil {
			t.Fatalf("__complete show: %v\noutput: %s", err, out)
		}
		got, _, _ := strings.Cut(out, ":")
		if got != "WL-1\tt WL-1\nWL-SPEC-2\ta doc\na-doc\ta doc\n" {
			t.Fatalf("candidates = %q, want the task ids then the doc refs", got)
		}
	})

	t.Run("one kind failing leaves the other", func(t *testing.T) {
		setupCompletionRoutes(t, "proj", map[string]http.HandlerFunc{
			"/api/v1/tasks": tasks, "/api/v1/docs": broken,
		})
		stderr := captureStderr(t)
		out, err := runLode(t, "__complete", "show", "")
		errText := stderr()
		if err != nil {
			t.Fatalf("__complete show: %v\noutput: %s", err, out)
		}
		if got, _, _ := strings.Cut(out, ":"); got != "WL-1\tt WL-1\n" {
			t.Fatalf("candidates = %q, want the task ids the working lookup still returned", got)
		}
		if errText != "" {
			t.Fatalf("wrote %q to the user's prompt, want nothing", errText)
		}
	})
}

// TestDocAndProjectCompletionFireAtTheRightPosition is the position half of
// 061 §3 C1 for the other kinds: which argument holds the ref is a property
// of the command, and a helper wired at the wrong one looks wired while
// completing nothing useful.
func TestDocAndProjectCompletionFireAtTheRightPosition(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "doc accept, ref first", args: []string{"doc", "accept", ""}, want: "doc"},
		// --to is passed because cobra offers an unset required flag as a
		// candidate of its own, which says nothing about this wiring.
		{name: "doc transfer, refs are variadic", args: []string{"doc", "transfer", "--to", "ada", "WL-SPEC-2", ""}, want: "doc"},
		{name: "doc set, ref last", args: []string{"doc", "set", "reviewers", "ada", ""}, want: "doc"},
		{name: "doc set, value argument is not a ref", args: []string{"doc", "set", "reviewers", ""}},
		{name: "project repo add, project first", args: []string{"project", "repo", "add", ""}, want: "project"},
		{name: "project repo add, repo argument is not a project", args: []string{"project", "repo", "add", "acme", ""}},
		{name: "project set focus-note", args: []string{"project", "set", "focus-note", ""}, want: "project"},
		{name: "project set decision", args: []string{"project", "set", "decision", ""}, want: "project"},
		{name: "project focus", args: []string{"project", "focus", ""}, want: "project"},
		{name: "project set focus, id last", args: []string{"project", "set", "focus", "cost", ""}, want: "project"},
		{name: "project set focus, first argument is a concern", args: []string{"project", "set", "focus", ""}},
	}
	want := map[string]string{
		"doc":     "WL-SPEC-2\ta doc\na-doc\ta doc\n",
		"project": "acme\tAcme\n",
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupCompletionRoutes(t, "proj", map[string]http.HandlerFunc{
				"/api/v1/docs": func(w http.ResponseWriter, r *http.Request) {
					writeTestJSON(t, w, docsResponse(model.Doc{Number: 2, Slug: "a-doc", Title: "a doc"}))
				},
				"/api/v1/projects": func(w http.ResponseWriter, r *http.Request) {
					writeTestJSON(t, w, model.ProjectListResponse{Projects: []model.Project{
						{ID: "acme", Name: "Acme", Key: "AC"},
					}})
				},
			})
			out, err := runLode(t, append([]string{"__complete"}, tc.args...)...)
			if err != nil {
				t.Fatalf("__complete %v: %v\noutput: %s", tc.args, err, out)
			}
			got, _, _ := strings.Cut(out, ":")
			if got != want[tc.want] {
				t.Fatalf("candidates = %q, want %q", got, want[tc.want])
			}
		})
	}
}
