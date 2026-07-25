package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// projectRepos runs `lode project list --json` and returns the repo mappings
// of the project with the given id.
func projectRepos(t *testing.T, id string) []cli.RepoMapping {
	t.Helper()
	out, err := runLode(t, "project", "list", "--json")
	if err != nil {
		t.Fatalf("lode project list: %v\noutput: %s", err, out)
	}
	var resp cli.ProjectListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	for _, p := range resp.Projects {
		if p.ID == id {
			return p.Repos
		}
	}
	t.Fatalf("project %s not in list output %q", id, out)
	return nil
}

// TestProjectRepoDoneState covers `lode project add-repo --done-state` and
// `lode project set-repo --done-state` end to end against a real server.
func TestProjectRepoDoneState(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	// runLode reuses rootCmd, so a flag value set by one call leaks into a
	// later call that omits it: exercise the omitted-flag case first.
	if out, err := runLode(t, "project", "add-repo", "proj", "acme/widgets"); err != nil {
		t.Fatalf("add-repo: %v\noutput: %s", err, out)
	}
	if out, err := runLode(t, "project", "add-repo", "proj", "acme/docs", "--done-state", "released"); err != nil {
		t.Fatalf("add-repo --done-state: %v\noutput: %s", err, out)
	}

	want := map[string]string{"acme/widgets": "merged", "acme/docs": "released"}
	for _, m := range projectRepos(t, "proj") {
		if want[m.Repo] != m.DoneState {
			t.Fatalf("repo %s done_state = %q, want %q", m.Repo, m.DoneState, want[m.Repo])
		}
		delete(want, m.Repo)
	}
	if len(want) != 0 {
		t.Fatalf("repos missing from project list: %v", want)
	}

	out, err := runLode(t, "project", "set-repo", "acme/widgets", "--done-state", "deployed_prod")
	if err != nil {
		t.Fatalf("set-repo: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "deployed_prod") {
		t.Fatalf("set-repo output = %q, want it to mention deployed_prod", out)
	}
	for _, m := range projectRepos(t, "proj") {
		if m.Repo == "acme/widgets" && m.DoneState != "deployed_prod" {
			t.Fatalf("acme/widgets done_state = %q, want deployed_prod", m.DoneState)
		}
	}

	// The server rejects an unknown state, and the CLI surfaces the failure.
	if out, err := runLode(t, "project", "set-repo", "acme/widgets", "--done-state", "bogus"); err == nil {
		t.Fatalf("set-repo bogus: want error, got nil\noutput: %s", out)
	}
	for _, m := range projectRepos(t, "proj") {
		if m.Repo == "acme/widgets" && m.DoneState != "deployed_prod" {
			t.Fatalf("acme/widgets done_state after rejected set = %q, want deployed_prod", m.DoneState)
		}
	}
}
