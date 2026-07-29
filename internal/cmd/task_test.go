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

// taskBody returns the stored body of a task via `lode task show --json`.
func taskBody(t *testing.T, id string) string {
	t.Helper()
	out, err := runLode(t, "task", "show", "--json", id)
	if err != nil {
		t.Fatalf("lode task show: %v\noutput: %s", err, out)
	}
	var task struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	return task.Body
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
