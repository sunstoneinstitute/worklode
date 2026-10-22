package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// setupRepoConfig creates a fake $HOME containing a repo directory with a
// .worklode/config.toml holding current_project, chdirs into it, and returns
// the repo path.
func setupRepoConfig(t *testing.T, currentProject string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir repo config dir: %v", err)
	}
	content := ""
	if currentProject != "" {
		content = "current_project = \"" + currentProject + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(repo, ".worklode", "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
	t.Chdir(repo)
	return repo
}

// addTask runs `lode task add` and returns the created task.
func addTask(t *testing.T, args ...string) model.Task {
	t.Helper()
	out, err := runLode(t, append([]string{"task", "add", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("lode task add: %v\noutput: %s", err, out)
	}
	var task model.Task
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	return task
}

func TestTaskAddUsesCurrentProject(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	setupRepoConfig(t, "proj")

	task := addTask(t, "--title", "From the repo config")
	if task.Project != "proj" {
		t.Fatalf("project = %q; want proj (from current_project)", task.Project)
	}
}

func TestTaskAddProjectFlagBeatsCurrentProject(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	if _, _, err := c.CreateProject(context.Background(), model.CreateProjectInput{ID: "other", Name: "Other", Key: "OTHR"}); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	setupRepoConfig(t, "proj")

	task := addTask(t, "--project", "other", "--title", "Explicit flag")
	if task.Project != "other" {
		t.Fatalf("project = %q; want other (the explicit flag wins)", task.Project)
	}
}

func TestTaskListDefaultsToCurrentProject(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "other", Name: "Other", Key: "OTHR"}); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	mine := createTestTask(t, c, "Mine")
	theirs, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "other", Title: "Theirs", Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("create task in other project: %v", err)
	}
	setupRepoConfig(t, "proj")

	if got := taskListIDs(t); len(got) != 1 || got[0] != mine.ID {
		t.Fatalf("task ids = %v; want only %s (current_project)", got, mine.ID)
	}

	// An explicitly empty --project opts back out to every project.
	want := []string{mine.ID, theirs.ID}
	sort.Strings(want)
	got := taskListIDs(t, "--project=")
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("task ids = %v; want %v (--project= means all projects)", got, want)
	}
}
