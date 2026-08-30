package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/secrets"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// initSecretsWorktree adds a linked worktree named "<taskID>-fix" under the
// default base (.worktrees) of a fresh repo and chdirs into it — the shape
// `lode worktree next` produces, so worktree.Root + Layout.TaskID resolve the task id
// the way production does. Returns the worktree directory.
func initSecretsWorktree(t *testing.T, taskID string) string {
	t.Helper()
	root := initGitRepo(t)
	dir := filepath.Join(root, worktree.DefaultBase, taskID+"-fix")
	out, err := exec.Command("git", "-C", root, "worktree", "add", dir, "-b", taskID+"-fix").CombinedOutput()
	if err != nil {
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

// TestSecretsPackRejectsMalformedNames: pack is a client-side writer of secret
// names into the keystore and the manifest, so it gates on the same grammar
// the server does — a name carrying an "=" would otherwise reach the exec
// child's environment as a second assignment.
func TestSecretsPackRejectsMalformedNames(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("A_TOKEN", "value-a")

	for _, tc := range []struct {
		bad  string
		args []string
	}{
		{"A_TOKEN=INJECTED", []string{"--task", "WL-7", "--names", "A_TOKEN=INJECTED"}},
		{"lowercase", []string{"--task", "WL-7", "--names", "lowercase"}},
		{"not a name", []string{"--task", "WL-7", "--names", "A_TOKEN", "--declined", "not a name"}},
	} {
		cmd := newSecretsPackCmd()
		cmd.SetArgs(tc.args)
		cmd.SetOut(io.Discard)
		err := cmd.Execute()
		// The rejection must be about the grammar: "not resolved in the
		// environment" would also name the value, and would still have let a
		// well-exported malformed name through.
		if err == nil || !strings.Contains(err.Error(), "invalid secret name") ||
			!strings.Contains(err.Error(), tc.bad) {
			t.Errorf("pack %v: err = %v; want an invalid-secret-name rejection naming %q", tc.args, err, tc.bad)
		}
	}
}

// TestSecretsPackPurgesNarrowedNames: re-running the ceremony after the
// declaration narrowed must not leave live values behind. keyring has no
// enumeration API and PurgeTask trusts the manifest, so a name dropped from
// the manifest without a keystore delete survives /lode-done forever.
func TestSecretsPackPurgesNarrowedNames(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("A_TOKEN", "value-a")
	t.Setenv("B_KEY", "value-b")

	pack := func(names string) {
		t.Helper()
		cmd := newSecretsPackCmd()
		cmd.SetArgs([]string{"--task", "WL-7", "--names", names})
		cmd.SetOut(io.Discard)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("pack %s: %v", names, err)
		}
	}
	pack("A_TOKEN,B_KEY")
	pack("A_TOKEN") // the declaration narrowed: B_KEY is no longer needed

	if _, err := secrets.Fetch("WL-7", "B_KEY"); err == nil {
		t.Fatal("B_KEY survived a narrowed re-pack: an orphaned value no purge path can reach")
	}
	if got, err := secrets.Fetch("WL-7", "A_TOKEN"); err != nil || got != "value-a" {
		t.Fatalf("A_TOKEN = %q, %v; want it kept", got, err)
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

// envValues returns every value the raw environment slice assigns to name.
// Flattening into a map would hide a duplicate, which is exactly the bug this
// file guards: execve keeps both entries and getenv returns the first.
func envValues(env []string, name string) []string {
	var out []string
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			out = append(out, v)
		}
	}
	return out
}

func TestSecretsExecInjectsExactlyMaterializedNames(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	initSecretsWorktree(t, "WL-9")
	// The operator's shell already exports one of the task's names. Spec 017
	// §4 says the child gets the task's value, not this one.
	t.Setenv("A_TOKEN", "stale-ambient")

	for _, n := range []string{"A_TOKEN", "B_KEY"} {
		if err := secrets.Put("WL-9", n, "val-"+n); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-9", Materialized: []string{"A_TOKEN", "B_KEY"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	// Another task's materialized secret must not leak into this child.
	if err := secrets.Put("WL-10", "OTHER_TOKEN", "val-other"); err != nil {
		t.Fatalf("put other task: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-10", Materialized: []string{"OTHER_TOKEN"}}); err != nil {
		t.Fatalf("manifest other task: %v", err)
	}

	var gotArgv, gotEnv []string
	restore := execFn
	execFn = func(bin string, argv, env []string) error {
		gotArgv, gotEnv = argv, env
		return nil
	}
	defer func() { execFn = restore }()

	cmd := newSecretsExecCmd()
	cmd.SetArgs([]string{"--", "env"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(gotArgv) == 0 || gotArgv[0] != "env" {
		t.Fatalf("argv = %v", gotArgv)
	}
	for name, want := range map[string]string{"A_TOKEN": "val-A_TOKEN", "B_KEY": "val-B_KEY"} {
		got := envValues(gotEnv, name)
		if len(got) != 1 || got[0] != want {
			t.Errorf("env entries for %s = %v; want exactly [%q]", name, got, want)
		}
	}
	if got := envValues(gotEnv, "OTHER_TOKEN"); len(got) != 0 {
		t.Errorf("another task's secret leaked into the child: OTHER_TOKEN = %v", got)
	}
}

// TestSecretsExecScrubsAmbientCredentials is 017 §4's acceptance criterion as
// amended by ADR 050, run end to end: with the operator's shell exporting
// ANTHROPIC_API_KEY, `lode secrets exec -- env` in a claimed worktree hands the
// child the materialized names and the shell plumbing, and not that key.
func TestSecretsExecScrubsAmbientCredentials(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	initSecretsWorktree(t, "WL-9")
	t.Setenv("ANTHROPIC_API_KEY", "test-value")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "operator-aws-secret")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(home, "gcp.json"))

	if err := secrets.Put("WL-9", "A_TOKEN", "val-A_TOKEN"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-9", Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	var gotEnv []string
	restore := execFn
	execFn = func(bin string, argv, env []string) error {
		gotEnv = env
		return nil
	}
	defer func() { execFn = restore }()

	cmd := newSecretsExecCmd()
	cmd.SetArgs([]string{"--", "env"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exec: %v", err)
	}

	for _, name := range []string{"ANTHROPIC_API_KEY", "AWS_SECRET_ACCESS_KEY", "GOOGLE_APPLICATION_CREDENTIALS"} {
		if got := envValues(gotEnv, name); len(got) != 0 {
			t.Errorf("ambient credential %s reached the child: %v", name, got)
		}
	}
	for _, kv := range gotEnv {
		if strings.Contains(kv, "test-value") || strings.Contains(kv, "operator-aws-secret") {
			t.Errorf("an ambient credential value survived under another name: %q", kv)
		}
	}
	if got := envValues(gotEnv, "A_TOKEN"); len(got) != 1 || got[0] != "val-A_TOKEN" {
		t.Errorf("A_TOKEN = %v; want exactly [\"val-A_TOKEN\"]", got)
	}
	// The plumbing the child needs is still there: scrubbing must not turn
	// into an allow-list that breaks PATH or HOME.
	for _, name := range []string{"PATH", "HOME"} {
		if got := envValues(gotEnv, name); len(got) != 1 {
			t.Errorf("%s entries = %v; want the child to inherit exactly one", name, got)
		}
	}
}

// TestSecretsExecPassesFlagsToTheChild: an agent writes `lode secrets exec
// kubectl get pods -n foo`, not the `--` form. Cobra must not claim the
// wrapped command's flags as its own.
func TestSecretsExecPassesFlagsToTheChild(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	initSecretsWorktree(t, "WL-9")
	if err := secrets.Put("WL-9", "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-9", Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	var gotArgv []string
	restore := execFn
	execFn = func(bin string, argv, env []string) error {
		gotArgv = argv
		return nil
	}
	defer func() { execFn = restore }()

	cmd := newSecretsExecCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"env", "-u", "HOME", "printenv"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exec with a flag-bearing child command: %v", err)
	}
	if strings.Join(gotArgv, " ") != "env -u HOME printenv" {
		t.Fatalf("argv = %v; want the child's flags passed through", gotArgv)
	}
}

func TestSecretsExecFailsOnMissingKeystoreItem(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	initSecretsWorktree(t, "WL-9")
	if err := secrets.SaveManifest(secrets.Manifest{Task: "WL-9", Materialized: []string{"GONE_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	cmd := newSecretsExecCmd()
	cmd.SetArgs([]string{"--", "env"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "GONE_TOKEN") {
		t.Fatalf("exec with missing item: %v; want error naming GONE_TOKEN", err)
	}
}

func TestSecretsExecRequiresWorktree(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	// A real git repo root that is not a Worklode worktree: the failure must
	// come from the binding guard, not from the absent manifest a plain repo
	// would also produce.
	t.Chdir(initGitRepo(t))

	cmd := newSecretsExecCmd()
	cmd.SetArgs([]string{"--", "env"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("exec outside a Worklode worktree succeeded; want the guard to fail")
	}
	if !strings.Contains(err.Error(), "not bound to a Worklode task") {
		t.Fatalf("error = %q; want the worktree guard's message", err)
	}
}

func TestSecretsCatalogLegendOnlyWithEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"secrets":[]}`)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "wl_test")
	t.Setenv("HOME", t.TempDir())

	cmd := newSecretsCatalogCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if strings.Contains(out.String(), "baseline") {
		t.Fatalf("empty catalog printed the legend:\n%s", out.String())
	}
}
