package cmd

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// taskListIDs runs `lode task list` with the given extra args and returns the
// listed task IDs, sorted.
func taskListIDs(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := runLode(t, append([]string{"task", "list", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("lode task list: %v\noutput: %s", err, out)
	}
	var resp struct {
		Tasks []model.Task `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	ids := make([]string, 0, len(resp.Tasks))
	for _, task := range resp.Tasks {
		ids = append(ids, task.ID)
	}
	sort.Strings(ids)
	return ids
}

// taskTitleBody returns the stored title and body of a task via
// `lode task show --json`.
func taskTitleBody(t *testing.T, id string) (string, string) {
	t.Helper()
	out, err := runLode(t, "task", "show", "--json", id)
	if err != nil {
		t.Fatalf("lode task show: %v\noutput: %s", err, out)
	}
	var task struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	return task.Title, task.Body
}

// taskBody returns just the stored body of a task.
func taskBody(t *testing.T, id string) string {
	t.Helper()
	_, body := taskTitleBody(t, id)
	return body
}

func TestTaskEditTitle(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Original title")

	if _, err := runLode(t, "task", "edit", task.ID, "--title", "Renamed"); err != nil {
		t.Fatalf("edit --title: %v", err)
	}
	if got, _ := taskTitleBody(t, task.ID); got != "Renamed" {
		t.Fatalf("title after --title = %q, want %q", got, "Renamed")
	}

	// Title and body in one edit.
	if _, err := runLode(t, "task", "edit", task.ID, "--title", "Both", "--body", "new body"); err != nil {
		t.Fatalf("edit --title --body: %v", err)
	}
	title, body := taskTitleBody(t, task.ID)
	if title != "Both" || body != "new body" {
		t.Fatalf("title, body = %q, %q; want %q, %q", title, body, "Both", "new body")
	}

	// An unrelated edit leaves the title alone.
	if _, err := runLode(t, "task", "edit", task.ID, "--priority", "low"); err != nil {
		t.Fatalf("edit --priority: %v", err)
	}
	if got, _ := taskTitleBody(t, task.ID); got != "Both" {
		t.Fatalf("title after unrelated edit = %q, want %q", got, "Both")
	}

	// A blank title is refused, and the stored title survives.
	if _, err := runLode(t, "task", "edit", task.ID, "--title", "   "); err == nil {
		t.Fatal("--title with blank value: want error, got nil")
	}
	if got, _ := taskTitleBody(t, task.ID); got != "Both" {
		t.Fatalf("title after rejected edit = %q, want %q", got, "Both")
	}
}

func TestTaskEditBody(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Body task")

	if _, err := runLode(t, "task", "edit", task.ID, "--body", "from flag"); err != nil {
		t.Fatalf("edit --body: %v", err)
	}
	if got := taskBody(t, task.ID); got != "from flag" {
		t.Fatalf("body after --body = %q, want %q", got, "from flag")
	}

	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte("# From file\n\nmulti\nline\n"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	if _, err := runLode(t, "task", "edit", task.ID, "--body-file", path); err != nil {
		t.Fatalf("edit --body-file: %v", err)
	}
	if got := taskBody(t, task.ID); got != "# From file\n\nmulti\nline\n" {
		t.Fatalf("body after --body-file = %q", got)
	}

	rootCmd.SetIn(strings.NewReader("from stdin"))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	if _, err := runLode(t, "task", "edit", task.ID, "--body-file", "-"); err != nil {
		t.Fatalf("edit --body-file -: %v", err)
	}
	if got := taskBody(t, task.ID); got != "from stdin" {
		t.Fatalf("body after --body-file - = %q, want %q", got, "from stdin")
	}

	// Editing another field leaves the body alone.
	if _, err := runLode(t, "task", "edit", task.ID, "--priority", "low"); err != nil {
		t.Fatalf("edit --priority: %v", err)
	}
	if got := taskBody(t, task.ID); got != "from stdin" {
		t.Fatalf("body after unrelated edit = %q, want %q", got, "from stdin")
	}
}

func TestTaskEditBodyErrors(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Body error task")

	if _, err := runLode(t, "task", "edit", task.ID, "--body", "x", "--body-file", "y"); err == nil {
		t.Fatal("--body with --body-file: want error, got nil")
	}
	if _, err := runLode(t, "task", "edit", task.ID, "--body-file", filepath.Join(t.TempDir(), "missing.md")); err == nil {
		t.Fatal("--body-file with missing file: want error, got nil")
	}
}

func TestTaskListStatusFiltering(t *testing.T) {
	st, c := lifecycleTestServer(t)
	// This repo's own .worklode/config.toml (current_project = "worklode")
	// would otherwise leak into `lode task list`'s default scope when the
	// test binary's cwd falls under this checkout; pin cwd/HOME to an
	// isolated repo scoped to "proj" instead, matching
	// TestTaskHierarchyCommands below.
	setupRepoConfig(t, "proj")
	setupProject(t, c)
	ctx := context.Background()

	ready := createTestTask(t, c, "Ready task")
	doing := createTestTask(t, c, "Doing task")
	done := createTestTask(t, c, "Done task")
	abandoned := createTestTask(t, c, "Abandoned task")

	if _, _, err := c.ClaimTask(ctx, doing.ID, "host:/.worktrees/doing", 0); err != nil {
		t.Fatalf("claim doing: %v", err)
	}
	if _, _, err := c.ClaimTask(ctx, done.ID, "host:/.worktrees/done", 0); err != nil {
		t.Fatalf("claim done: %v", err)
	}
	moveToReview(t, st, done.ID)
	if _, _, err := c.DoneTask(ctx, done.ID); err != nil {
		t.Fatalf("done: %v", err)
	}
	if _, _, err := c.AbandonTask(ctx, abandoned.ID); err != nil {
		t.Fatalf("abandon: %v", err)
	}

	// Isolate from whatever repo config this test happens to run inside
	// (e.g. this repo's own .worklode/config.toml scopes to a different
	// project) so `lode task list`'s --project default resolves to "proj"
	// as the test intends.
	setupRepoConfig(t, "proj")
	t.Cleanup(func() { resetProjectFlag(t, "task", "list") })
	resetProjectFlag(t, "task", "list")

	sorted := func(ids ...string) []string {
		sort.Strings(ids)
		return ids
	}
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"default hides merged and abandoned", nil, sorted(ready.ID, doing.ID)},
		{"status merged", []string{"--status", "merged"}, sorted(done.ID)},
		{"status repeatable", []string{"--status", "merged", "--status", "abandoned"}, sorted(done.ID, abandoned.ID)},
		{"status all", []string{"--status", "all"}, sorted(ready.ID, doing.ID, done.ID, abandoned.ID)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := taskListIDs(t, tc.args...)
			if len(got) != len(tc.want) {
				t.Fatalf("task ids = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("task ids = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// taskAssignee returns the stored assignee of a task via `lode task show
// --json`.
func taskAssignee(t *testing.T, id string) string {
	t.Helper()
	out, err := runLode(t, "task", "show", "--json", id)
	if err != nil {
		t.Fatalf("lode task show: %v\noutput: %s", err, out)
	}
	var task struct {
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	return task.Assignee
}

func TestTaskAssignUnassign(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	if err := st.CreateActor(context.Background(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	task := createTestTask(t, c, "Assignable task")

	// No --to: assigns to the caller (alice, per lifecycleTestServer).
	if _, err := runLode(t, "task", "assign", task.ID); err != nil {
		t.Fatalf("task assign (self): %v", err)
	}
	if got := taskAssignee(t, task.ID); got != "alice" {
		t.Fatalf("assignee after self-assign = %q, want alice", got)
	}

	// --to reassigns to a named actor.
	if _, err := runLode(t, "task", "assign", task.ID, "--to", "bob"); err != nil {
		t.Fatalf("task assign --to bob: %v", err)
	}
	if got := taskAssignee(t, task.ID); got != "bob" {
		t.Fatalf("assignee after --to bob = %q, want bob", got)
	}

	if _, err := runLode(t, "task", "unassign", task.ID); err != nil {
		t.Fatalf("task unassign: %v", err)
	}
	if got := taskAssignee(t, task.ID); got != "" {
		t.Fatalf("assignee after unassign = %q, want empty", got)
	}
}

func TestTaskStartStopSubmit(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Lease-free task")

	// start assigns the caller (unassigned task) and moves it to in_progress.
	out, err := runLode(t, "task", "start", task.ID, "--json")
	if err != nil {
		t.Fatalf("task start: %v\noutput: %s", err, out)
	}
	var started model.Task
	if err := json.Unmarshal([]byte(out), &started); err != nil {
		t.Fatalf("decode task start output %q: %v", out, err)
	}
	if started.State != "in_progress" || started.Assignee != "alice" {
		t.Fatalf("task start result = %+v, want in_progress/alice", started)
	}

	// stop returns it to ready, keeping the assignment.
	out, err = runLode(t, "task", "stop", task.ID, "--json")
	if err != nil {
		t.Fatalf("task stop: %v\noutput: %s", err, out)
	}
	var stopped model.Task
	if err := json.Unmarshal([]byte(out), &stopped); err != nil {
		t.Fatalf("decode task stop output %q: %v", out, err)
	}
	if stopped.State != "ready" || stopped.Assignee != "alice" {
		t.Fatalf("task stop result = %+v, want ready/alice (assignment kept)", stopped)
	}

	// Re-start, then submit moves it to in_review.
	if _, err := runLode(t, "task", "start", task.ID); err != nil {
		t.Fatalf("task re-start: %v", err)
	}
	out, err = runLode(t, "task", "submit", task.ID, "--json")
	if err != nil {
		t.Fatalf("task submit: %v\noutput: %s", err, out)
	}
	var submitted model.Task
	if err := json.Unmarshal([]byte(out), &submitted); err != nil {
		t.Fatalf("decode task submit output %q: %v", out, err)
	}
	if submitted.State != "in_review" {
		t.Fatalf("task submit result state = %q, want in_review", submitted.State)
	}
}

func TestTaskListAssigneeFilterAndRendering(t *testing.T) {
	st, c := lifecycleTestServer(t)
	// See the comment in TestTaskListStatusFiltering: `task list` needs a
	// scoped project, or this repo's own ambient .worklode/config.toml wins.
	setupRepoConfig(t, "proj")
	setupProject(t, c)
	if err := st.CreateActor(context.Background(), "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	mine := createTestTask(t, c, "Alice's task")
	bobs := createTestTask(t, c, "Bob's task")
	if _, err := runLode(t, "task", "assign", mine.ID); err != nil {
		t.Fatalf("assign mine: %v", err)
	}
	if _, err := runLode(t, "task", "assign", bobs.ID, "--to", "bob"); err != nil {
		t.Fatalf("assign bobs: %v", err)
	}

	if got := taskListIDs(t, "--assignee", "bob"); len(got) != 1 || got[0] != bobs.ID {
		t.Fatalf("list --assignee bob = %v, want [%s]", got, bobs.ID)
	}
	if got := taskListIDs(t, "--assignee", "alice"); len(got) != 1 || got[0] != mine.ID {
		t.Fatalf("list --assignee alice = %v, want [%s]", got, mine.ID)
	}

	// Rendered (non-JSON) list shows an ASSIGNEE column with the right value.
	out, err := runLode(t, "task", "list")
	if err != nil {
		t.Fatalf("task list: %v", err)
	}
	if !strings.Contains(out, "ASSIGNEE") {
		t.Fatalf("task list output missing ASSIGNEE column:\n%s", out)
	}
	// The row must exist *and* carry "bob" in the ASSIGNEE column. Checking
	// the whole line would also match the title, and a loop with no `found`
	// flag would pass if the row disappeared entirely. Columns are
	// tabwriter-padded and all single-token up to TITLE, so field 5 is
	// ASSIGNEE.
	var found bool
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, bobs.ID) {
			continue
		}
		found = true
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[5] != "bob" {
			t.Fatalf("task list row for %s has assignee column %v, want bob:\n%s", bobs.ID, fields, out)
		}
	}
	if !found {
		t.Fatalf("task list output has no row for %s:\n%s", bobs.ID, out)
	}

	// Rendered (non-JSON) show has an "assignee:" line.
	show, err := runLode(t, "task", "show", bobs.ID)
	if err != nil {
		t.Fatalf("task show: %v", err)
	}
	if !strings.Contains(show, "assignee: bob") {
		t.Fatalf("task show output missing assignee line:\n%s", show)
	}
}

func TestTaskHierarchyCommands(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	setupRepoConfig(t, "proj") // so a bare task number resolves

	container, _, err := c.CreateTask(context.Background(), cli.CreateTaskInput{
		Project: "proj", Title: "Container", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	loose := createTestTask(t, c, "Childless")

	// add --parent files the new task under the parent in one round trip.
	out, err := runLode(t, "task", "add", "--json", "--project", "proj",
		"--title", "Piece", "--parent", container.ID)
	if err != nil {
		t.Fatalf("task add --parent: %v\noutput: %s", err, out)
	}
	var child model.Task
	if err := json.Unmarshal([]byte(out), &child); err != nil {
		t.Fatalf("decode add output %q: %v", out, err)
	}

	if got := taskListIDs(t, "--parent", container.ID); len(got) != 1 || got[0] != child.ID {
		t.Fatalf("list --parent = %v, want [%s]", got, child.ID)
	}

	show, err := runLode(t, "task", "show", child.ID)
	if err != nil {
		t.Fatalf("task show: %v", err)
	}
	if !strings.Contains(show, "parent:   "+container.ID) {
		t.Fatalf("show has no parent line naming %s:\n%s", container.ID, show)
	}

	tree, err := runLode(t, "task", "tree", "--project", "proj")
	if err != nil {
		t.Fatalf("task tree: %v", err)
	}
	if !strings.Contains(tree, container.ID) || !strings.Contains(tree, child.ID) {
		t.Fatalf("tree missing parent or child:\n%s", tree)
	}
	// Only tasks with children are roots (029 §2 left no kind to select on):
	// a childless task is neither a parent nor a child, so it must not appear.
	if strings.Contains(tree, loose.ID) {
		t.Fatalf("tree lists the childless %s:\n%s", loose.ID, tree)
	}

	if _, err := runLode(t, "task", "unparent", child.ID); err != nil {
		t.Fatalf("task unparent: %v", err)
	}
	if got := taskListIDs(t, "--parent", container.ID); len(got) != 0 {
		t.Fatalf("list --parent after unparent = %v, want []", got)
	}

	// parent --under re-files it, expanding a bare number for the parent.
	parentNumber := container.ID[strings.LastIndex(container.ID, "-")+1:]
	if out, err := runLode(t, "task", "parent", child.ID, "--under", parentNumber); err != nil {
		t.Fatalf("task parent --under %s: %v\noutput: %s", parentNumber, err, out)
	}
	if got := taskListIDs(t, "--parent", container.ID); len(got) != 1 || got[0] != child.ID {
		t.Fatalf("list --parent after parent --under = %v, want [%s]", got, child.ID)
	}

	// decompose splits a task in place and creates its children as drafts.
	big := createTestTask(t, c, "Too big")
	out, err = runLode(t, "task", "decompose", big.ID, "--into", "A", "--into", "B")
	if err != nil {
		t.Fatalf("task decompose: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "now has 2 children") {
		t.Fatalf("decompose output:\n%s", out)
	}
	if got := taskListIDs(t, "--parent", big.ID); len(got) != 2 {
		t.Fatalf("children of %s = %v, want 2", big.ID, got)
	}
}

func TestTaskFollowUpCommands(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	setupRepoConfig(t, "proj") // so a bare task number resolves

	origin := createTestTask(t, c, "Origin")

	// add --follow-up-to records the origin edge in one round trip.
	out, err := runLode(t, "task", "add", "--json", "--project", "proj",
		"--title", "Follow-up", "--follow-up-to", origin.ID)
	if err != nil {
		t.Fatalf("task add --follow-up-to: %v\noutput: %s", err, out)
	}
	var followUp model.Task
	if err := json.Unmarshal([]byte(out), &followUp); err != nil {
		t.Fatalf("decode add output %q: %v", out, err)
	}

	detail, _, err := c.GetTask(context.Background(), followUp.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	found := false
	for _, e := range detail.Edges.Out {
		if e.Type == "follow_up_to" && e.To == origin.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("out edges after add --follow-up-to = %+v, want a follow_up_to edge to %s", detail.Edges.Out, origin.ID)
	}

	// follow-up --of records the same edge on an already-created task.
	other := createTestTask(t, c, "Second origin")
	spun := createTestTask(t, c, "Spun out")
	if out, err := runLode(t, "task", "follow-up", spun.ID, "--of", other.ID); err != nil {
		t.Fatalf("task follow-up --of: %v\noutput: %s", err, out)
	}
	detail, _, err = c.GetTask(context.Background(), spun.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	found = false
	for _, e := range detail.Edges.Out {
		if e.Type == "follow_up_to" && e.To == other.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("out edges after follow-up --of = %+v, want a follow_up_to edge to %s", detail.Edges.Out, other.ID)
	}

	// unfollow-up reads the origin back and drops the edge.
	if out, err := runLode(t, "task", "unfollow-up", spun.ID); err != nil {
		t.Fatalf("task unfollow-up: %v\noutput: %s", err, out)
	}
	detail, _, err = c.GetTask(context.Background(), spun.ID)
	if err != nil {
		t.Fatalf("get task after unfollow-up: %v", err)
	}
	for _, e := range detail.Edges.Out {
		if e.Type == "follow_up_to" {
			t.Fatalf("out edges after unfollow-up = %+v, want no follow_up_to edge", detail.Edges.Out)
		}
	}

	// unfollow-up on a task with no origin hits the error branch.
	loose := createTestTask(t, c, "No origin")
	if _, err := runLode(t, "task", "unfollow-up", loose.ID); err == nil ||
		!strings.Contains(err.Error(), "is not a follow-up to anything") {
		t.Fatalf("task unfollow-up %s = %v, want \"is not a follow-up to anything\" error", loose.ID, err)
	}
}

func TestResolveBody(t *testing.T) {
	f := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(f, []byte("from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		body, bodyFile, stdin, want string
		wantErr                     bool
	}{
		"inline":       {body: "inline", want: "inline"},
		"file":         {bodyFile: f, want: "from file\n"},
		"stdin":        {bodyFile: "-", stdin: "from stdin", want: "from stdin"},
		"missing file": {bodyFile: filepath.Join(t.TempDir(), "nope.md"), wantErr: true},
		"neither":      {want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := resolveBody(tc.body, tc.bodyFile, strings.NewReader(tc.stdin))
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("resolveBody = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestTaskAddBodyFlagsMutuallyExclusive(t *testing.T) {
	cmd := newTaskAddCmd()
	cmd.SetArgs([]string{"--title", "t", "--body", "x", "--body-file", "y"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
		t.Fatalf("err = %v; want cobra mutual-exclusion error", err)
	}
}
