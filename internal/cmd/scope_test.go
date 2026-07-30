package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestBareTaskNumberResolves(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "By number")
	setupRepoConfig(t, "proj")

	number := task.ID[strings.LastIndex(task.ID, "-")+1:]
	title, _ := taskTitleBody(t, number)
	if title != "By number" {
		t.Fatalf("task show %s = %q; want the task %s", number, title, task.ID)
	}
}

func TestFullTaskIDStillWorks(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "By full id")
	setupRepoConfig(t, "proj")

	if title, _ := taskTitleBody(t, task.ID); title != "By full id" {
		t.Fatalf("task show %s = %q; want By full id", task.ID, title)
	}
}

func TestBareTaskNumberWithoutProjectFails(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	createTestTask(t, c, "Unreachable by number")
	setupGitRepo(t, "") // no config, no remote

	out, err := runLode(t, "task", "show", "1")
	if err == nil {
		t.Fatalf("bare number with no project succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "task number") {
		t.Fatalf("error = %v; want it to explain that 1 is a task number", err)
	}
}

func TestBareTaskNumberWithUnknownProjectSaysSo(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	createTestTask(t, c, "Unreachable by number")
	setupRepoConfig(t, "projj") // a typo: no such project on the server

	out, err := runLode(t, "task", "show", "1")
	if err == nil {
		t.Fatalf("bare number with an unknown project succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "projj") {
		t.Fatalf("error = %v; want it to name the project whose key could not be looked up", err)
	}
	if strings.Contains(err.Error(), "set current_project") {
		t.Fatalf("error = %v; current_project is set — telling the user to set it is wrong", err)
	}
}

func TestBareTaskNumberInClaimRespectsProjectFlag(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	createTestTask(t, c, "In the current project")
	other := createOtherProjectTask(t, c)
	setupRepoConfig(t, "proj")

	number := other.ID[strings.LastIndex(other.ID, "-")+1:]
	out, err := runLode(t, "task", "claim", number, "--project", "other", "--worktree", "host:/tmp/wt")
	if err != nil {
		t.Fatalf("lode task claim %s --project other: %v\noutput: %s", number, err, out)
	}
	if !strings.Contains(out, other.ID) {
		t.Fatalf("claim output = %q; want it to claim %s from the flagged project", out, other.ID)
	}
}

func TestBareTaskNumberResolvesInTimeline(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Timeline by number")
	setupRepoConfig(t, "proj")

	number := task.ID[strings.LastIndex(task.ID, "-")+1:]
	out, err := runLode(t, "timeline", number, "--json")
	if err != nil {
		t.Fatalf("lode timeline %s: %v\noutput: %s", number, err, out)
	}
	var resp struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if resp.Task.ID != task.ID {
		t.Fatalf("timeline %s task = %q; want %s", number, resp.Task.ID, task.ID)
	}
}

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

// boardProjectIDs runs `lode board --json` and returns the project ids shown.
func boardProjectIDs(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := runLode(t, append([]string{"board", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("lode board: %v\noutput: %s", err, out)
	}
	var resp struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode board %q: %v", out, err)
	}
	ids := make([]string, 0, len(resp.Projects))
	for _, p := range resp.Projects {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestBoardScopesToCurrentProject(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	createOtherProjectTask(t, c)
	setupRepoConfig(t, "proj")

	if got := boardProjectIDs(t); len(got) != 1 || got[0] != "proj" {
		t.Fatalf("board = %v; want only proj", got)
	}
}

func TestBoardProjectFlagAndPositional(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	createOtherProjectTask(t, c)
	setupRepoConfig(t, "proj")

	if got := boardProjectIDs(t, "--project", "other"); len(got) != 1 || got[0] != "other" {
		t.Fatalf("board --project other = %v; want only other", got)
	}
	if got := boardProjectIDs(t, "other"); len(got) != 1 || got[0] != "other" {
		t.Fatalf("board other = %v; want only other", got)
	}
	if got := boardProjectIDs(t, "--project="); len(got) != 2 {
		t.Fatalf("board --project= = %v; want both projects", got)
	}
}

func TestInboxListScopesToCurrentProject(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	createOtherProjectTask(t, c)
	mapProjectRepo(t, c, "other", "acme/other")
	seedIssue(t, st, "acme/proj", 1)
	seedIssue(t, st, "acme/other", 2)

	setupRepoConfig(t, "proj")

	out, err := runLode(t, "inbox", "list", "--json")
	if err != nil {
		t.Fatalf("lode inbox list: %v\noutput: %s", err, out)
	}
	var resp struct {
		Issues []struct {
			Repo string `json:"repo"`
		} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Issues) != 1 || resp.Issues[0].Repo != "acme/proj" {
		t.Fatalf("inbox list = %+v; want only acme/proj", resp.Issues)
	}
}

// seedIssue inserts a triage_state="new" inbox issue through the event log,
// the same path a GitHub webhook takes.
func seedIssue(t *testing.T, st *store.Store, repo string, number int64) {
	t.Helper()
	_, _, err := st.RecordEvent(context.Background(), "github",
		fmt.Sprintf("%s-%s-%d", t.Name(), repo, number), "issues.opened", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.UpsertIssue(tx, store.Issue{
				Repo: repo, Number: number, Title: "issue", State: "open",
				URL: "https://example.test/x",
			})
		})
	if err != nil {
		t.Fatalf("seed issue %s#%d: %v", repo, number, err)
	}
}

func TestProjectResolveReportsSource(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	setupGitRepo(t, "git@github.com:acme/proj.git")

	out, err := runLode(t, "project", "resolve", "--json")
	if err != nil {
		t.Fatalf("lode project resolve: %v\noutput: %s", err, out)
	}
	var got struct {
		Project string `json:"project"`
		Key     string `json:"key"`
		Source  string `json:"source"`
		Remote  string `json:"remote"`
		Cached  bool   `json:"cached"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if got.Project != "proj" || got.Key != "PROJ" {
		t.Fatalf("resolve = %+v; want project proj", got)
	}
	if got.Source != "git remote" || got.Remote != "git@github.com:acme/proj.git" {
		t.Fatalf("resolve = %+v; want the git-remote source", got)
	}
	if got.Cached {
		t.Fatalf("first resolve reported cached = true")
	}

	// Second run is cached; --refresh re-queries.
	out, err = runLode(t, "project", "resolve", "--json")
	if err != nil {
		t.Fatalf("second resolve: %v\noutput: %s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Cached {
		t.Fatalf("second resolve reported cached = false")
	}

	out, err = runLode(t, "project", "resolve", "--json", "--refresh")
	if err != nil {
		t.Fatalf("resolve --refresh: %v\noutput: %s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Cached {
		t.Fatalf("--refresh reported cached = true")
	}
}

func TestProjectResolveUnscoped(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	setupGitRepo(t, "")

	out, err := runLode(t, "project", "resolve")
	if err != nil {
		t.Fatalf("lode project resolve: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "no current project") {
		t.Fatalf("output = %q; want it to say there is no current project", out)
	}
}
