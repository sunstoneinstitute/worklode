package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// setupGitRepo creates a fake $HOME containing a git repo with the given
// origin remote and no .worklode config, chdirs into it, and returns its path.
func setupGitRepo(t *testing.T, origin string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if origin != "" {
		run("remote", "add", "origin", origin)
	}
	t.Chdir(repo)
	return repo
}

// mapProjectRepo maps a GitHub repo to a project on the test server.
func mapProjectRepo(t *testing.T, c *cli.Client, project, repo string) {
	t.Helper()
	if _, err := c.AddRepo(context.Background(), project, repo, ""); err != nil {
		t.Fatalf("map %s to %s: %v", repo, project, err)
	}
}

// createOtherProjectTask creates a task in a second project, so scoping has
// something to exclude.
func createOtherProjectTask(t *testing.T, c *cli.Client) cli.Task {
	t.Helper()
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{
		ID: "other", Name: "Other", Key: "OT",
	}); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	task, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "other", Title: "in another project", Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create other-project task: %v", err)
	}
	return task
}

func TestTaskListScopesFromGitRemote(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	scoped := createTestTask(t, c, "in the scoped project")
	other := createOtherProjectTask(t, c)

	setupGitRepo(t, "git@github.com:acme/proj.git")

	got := taskListIDs(t)
	if len(got) != 1 || got[0] != scoped.ID {
		t.Fatalf("task list = %v; want only %s (scoped off the git remote, not %s)",
			got, scoped.ID, other.ID)
	}
}

func TestTaskListUnmappedRepoIsUnscoped(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	scoped := createTestTask(t, c, "a task")
	other := createOtherProjectTask(t, c)

	setupGitRepo(t, "git@github.com:acme/not-mapped.git")

	got := taskListIDs(t)
	if len(got) != 2 {
		t.Fatalf("task list = %v; want both %s and %s (unmapped repo means unscoped)",
			got, scoped.ID, other.ID)
	}
}

func TestTaskListRepoFlagSelectsProject(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	scoped := createTestTask(t, c, "a task")
	createOtherProjectTask(t, c)

	setupRepoConfig(t, "") // a repo config with no current_project

	got := taskListIDs(t, "--repo", "acme/proj")
	if len(got) != 1 || got[0] != scoped.ID {
		t.Fatalf("task list --repo = %v; want only %s", got, scoped.ID)
	}
}

func TestProjectAndRepoFlagsConflict(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	setupRepoConfig(t, "")

	out, err := runLode(t, "task", "list", "--project", "proj", "--repo", "acme/proj")
	if err == nil {
		t.Fatalf("--project with --repo succeeded; want an error\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "pass only one") {
		t.Fatalf("err = %v; want it to say the two flags name the same thing", err)
	}
}

func TestEmptyProjectFlagOptsOut(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	createTestTask(t, c, "a task")
	createOtherProjectTask(t, c)

	setupGitRepo(t, "git@github.com:acme/proj.git")

	if got := taskListIDs(t, "--project="); len(got) != 2 {
		t.Fatalf("task list --project= = %v; want both tasks", got)
	}
}

func TestTaskAddResolvesProjectFromGitRemote(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")

	setupGitRepo(t, "git@github.com:acme/proj.git")

	task := addTask(t, "--title", "From the git remote")
	if task.Project != "proj" {
		t.Fatalf("project = %q; want proj", task.Project)
	}
}

func TestTaskAddWithoutAnyProjectFails(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	setupGitRepo(t, "") // a git repo with no origin

	out, err := runLode(t, "task", "add", "--title", "Nowhere")
	if err == nil {
		t.Fatalf("task add with no resolvable project succeeded\noutput: %s", out)
	}
}
