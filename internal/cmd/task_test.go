package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
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
		Tasks []cli.Task `json:"tasks"`
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
	setupProject(t, c)
	ctx := context.Background()

	ready := createTestTask(t, c, "Ready task")
	doing := createTestTask(t, c, "Doing task")
	done := createTestTask(t, c, "Done task")
	abandoned := createTestTask(t, c, "Abandoned task")

	if _, _, err := c.ClaimTask(ctx, doing.ID, "host:/wt/doing", 0); err != nil {
		t.Fatalf("claim doing: %v", err)
	}
	if _, _, err := c.ClaimTask(ctx, done.ID, "host:/wt/done", 0); err != nil {
		t.Fatalf("claim done: %v", err)
	}
	moveToReview(t, st, done.ID)
	if _, _, err := c.DoneTask(ctx, done.ID); err != nil {
		t.Fatalf("done: %v", err)
	}
	if _, _, err := c.AbandonTask(ctx, abandoned.ID); err != nil {
		t.Fatalf("abandon: %v", err)
	}

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

func TestTaskHierarchyCommands(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	epic, _, err := c.CreateTask(context.Background(), cli.CreateTaskInput{
		Project: "proj", Title: "Container", Priority: "high", Kind: "epic",
	})
	if err != nil {
		t.Fatalf("create epic: %v", err)
	}

	// add --parent files the new task under the epic in one round trip.
	out, err := runLode(t, "task", "add", "--json", "--project", "proj",
		"--title", "Piece", "--parent", epic.ID)
	if err != nil {
		t.Fatalf("task add --parent: %v\noutput: %s", err, out)
	}
	var child cli.Task
	if err := json.Unmarshal([]byte(out), &child); err != nil {
		t.Fatalf("decode add output %q: %v", out, err)
	}

	if got := taskListIDs(t, "--parent", epic.ID); len(got) != 1 || got[0] != child.ID {
		t.Fatalf("list --parent = %v, want [%s]", got, child.ID)
	}

	show, err := runLode(t, "task", "show", child.ID)
	if err != nil {
		t.Fatalf("task show: %v", err)
	}
	if !strings.Contains(show, "parent:") || !strings.Contains(show, epic.ID) {
		t.Fatalf("show has no parent line:\n%s", show)
	}

	tree, err := runLode(t, "task", "tree", "--project", "proj")
	if err != nil {
		t.Fatalf("task tree: %v", err)
	}
	if !strings.Contains(tree, epic.ID) || !strings.Contains(tree, child.ID) {
		t.Fatalf("tree missing epic or child:\n%s", tree)
	}

	if _, err := runLode(t, "task", "unparent", child.ID); err != nil {
		t.Fatalf("task unparent: %v", err)
	}
	if got := taskListIDs(t, "--parent", epic.ID); len(got) != 0 {
		t.Fatalf("list --parent after unparent = %v, want []", got)
	}

	// decompose converts a task in place and creates its children as drafts.
	big := createTestTask(t, c, "Too big")
	out, err = runLode(t, "task", "decompose", big.ID, "--into", "A", "--into", "B")
	if err != nil {
		t.Fatalf("task decompose: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "is now an epic") {
		t.Fatalf("decompose output:\n%s", out)
	}
	if got := taskListIDs(t, "--parent", big.ID); len(got) != 2 {
		t.Fatalf("children of %s = %v, want 2", big.ID, got)
	}
}
