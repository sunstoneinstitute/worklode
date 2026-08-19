package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/secrets"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// initSecretsWorktree creates a real git repo, adds a linked worktree for it
// under the default worktree base (.worktrees) named "<taskID>-fix", and
// chdirs into that worktree. This is the same shape `lode next` produces and
// the same pattern other lifecycle tests use to build one directly (see
// TestLeaseHeldElsewhere-style setups in lifecycle_test.go): a real linked
// worktree whose directory name carries the task id, one level below the
// base, so worktree.Root + Layout.TaskID resolve it exactly like production
// does. The plan's original helper (a bare mkdir under <tmp>/wt/<id>-fix)
// does not satisfy Layout.TaskID's "exactly one level below the configured
// base" guard, since the base defaults to ".worktrees", not "wt".
func initSecretsWorktree(t *testing.T, taskID string) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...)
		c.Dir = root
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-q", "-m", "initial commit")

	dir := filepath.Join(root, worktree.DefaultBase, taskID+"-fix")
	branch := taskID + "-fix"
	if out, err := exec.Command("git", "-C", root, "worktree", "add", dir, "-b", branch).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	t.Chdir(dir)
	return dir
}

func TestSecretsPackWritesKeystoreNotDisk(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("A_TOKEN", "value-a")
	t.Setenv("B_KEY", "value-b")

	cmd := newSecretsPackCmd()
	cmd.SetArgs([]string{"--task", "WL-7", "--names", "A_TOKEN,B_KEY", "--declined", "C_SECRET"})
	cmd.SetOut(new(bytes.Buffer))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pack: %v", err)
	}

	for name, want := range map[string]string{"A_TOKEN": "value-a", "B_KEY": "value-b"} {
		if got, err := secrets.Fetch("WL-7", name); err != nil || got != want {
			t.Fatalf("keystore %s = %q, %v; want %q", name, got, err, want)
		}
	}
	m, ok := secrets.LoadManifest("WL-7")
	if !ok || len(m.Materialized) != 2 || len(m.Declined) != 1 {
		t.Fatalf("manifest = %+v, %v", m, ok)
	}
	// The redaction check: no file under $HOME contains a value.
	filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "value-a") || strings.Contains(string(data), "value-b") {
			t.Errorf("secret value written to %s", path)
		}
		return nil
	})
}

func TestSecretsPackFailsOnUnresolvedName(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	os.Unsetenv("NOT_RESOLVED")

	cmd := newSecretsPackCmd()
	cmd.SetArgs([]string{"--task", "WL-7", "--names", "NOT_RESOLVED"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "NOT_RESOLVED") {
		t.Fatalf("pack with unresolved name: %v; want error naming it", err)
	}
}

func TestSecretsPurgeCommand(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	initSecretsWorktree(t, "WL-9")

	if err := secrets.Put("WL-9", "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-9", Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	cmd := newSecretsPurgeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := secrets.Fetch("WL-9", "A_TOKEN"); err == nil {
		t.Fatal("secret survived purge")
	}
	if !strings.Contains(out.String(), "A_TOKEN") {
		t.Fatalf("purge output = %q; want the purged name", out.String())
	}
}
