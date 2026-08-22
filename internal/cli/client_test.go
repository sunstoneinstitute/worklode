package cli_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedSkill upserts one skill with a deterministic hash ("h-<name>") and a
// non-empty archive, mirroring internal/api's own seedSkill test helper.
func seedSkill(t *testing.T, st *store.Store, name, description string) {
	t.Helper()
	_, _, err := st.UpsertSkill(context.Background(), store.SkillUpsert{
		Qualifier:   "acme",
		Name:        name,
		Description: description,
		SourceRepo:  "acme/skills",
		SourcePath:  "skills/" + name,
		GitCommit:   "deadbeef",
		ContentHash: "h-" + name,
		SkillMD:     "# " + name + "\n\n" + description,
		Frontmatter: json.RawMessage(`{}`),
		Archive:     []byte("gzip-archive-" + name),
	})
	if err != nil {
		t.Fatalf("seed skill %s: %v", name, err)
	}
}

// newTestServer opens a store in a temp dir, creates admin actor "alice"
// with a token, and starts a real HTTP server (httptest.NewServer, a live
// listener — not httptest.NewRecorder — since cli.Client makes real net/http
// calls). Returns the store (for out-of-band setup like seeding an inbox
// issue), a Client pointed at the server and authenticated as alice, and the
// server's base URL (for tests that need a second client with a different
// token).
func newTestServer(t *testing.T) (*store.Store, *cli.Client, string) {
	t.Helper()
	st := store.OpenTestStore(t)

	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	h, _, err := api.NewServer(st, api.Config{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	c := cli.NewClient(cli.Config{ServerURL: ts.URL, Token: token})
	return st, c, ts.URL
}

// moveToReview transitions a task from in_progress to in_review directly via
// the store, simulating the PR-open transition the CLI itself has no
// command for.
func moveToReview(t *testing.T, st *store.Store, taskID string) {
	t.Helper()
	_, _, err := st.RecordEvent(context.Background(), "github", "to-review-"+taskID, "task.review", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.Transition(tx, st.Now(), taskID, "in_progress", "in_review", eventID)
		})
	if err != nil {
		t.Fatalf("move %s to in_review: %v", taskID, err)
	}
}

func TestClientErrorRendering(t *testing.T) {
	err := &cli.ClientError{Status: 404, Msg: "task WL-9 not found"}
	want := "server error (404): task WL-9 not found"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}

	_, c, _ := newTestServer(t)
	_, _, err2 := c.GetTask(context.Background(), "WL-99")
	if err2 == nil {
		t.Fatalf("GetTask unknown id: err = nil, want ClientError")
	}
	var ce *cli.ClientError
	if !errors.As(err2, &ce) {
		t.Fatalf("GetTask unknown id err = %v (%T), want *cli.ClientError", err2, err2)
	}
	if ce.Status != 404 {
		t.Fatalf("ClientError.Status = %d, want 404", ce.Status)
	}
	if !strings.HasPrefix(ce.Error(), "server error (404): ") {
		t.Fatalf("ClientError.Error() = %q", ce.Error())
	}
}

func TestLoadConfigFileAndEnvOverride(t *testing.T) {
	configTestHome(t)
	content := "# a comment\nserver = \"https://file.example.com\"\ntoken = \"wl_filetoken\"\n\n"
	if err := cli.WriteRawConfigForTest(content); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig (file only): %v", err)
	}
	if cfg.ServerURL != "https://file.example.com" || cfg.Token != "wl_filetoken" {
		t.Fatalf("LoadConfig (file only) = %+v", cfg)
	}

	t.Setenv("LODE_SERVER", "https://env.example.com")
	t.Setenv("LODE_TOKEN", "wl_envtoken")
	cfg, err = cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig (env override): %v", err)
	}
	if cfg.ServerURL != "https://env.example.com" || cfg.Token != "wl_envtoken" {
		t.Fatalf("LoadConfig (env override) = %+v, want env values to win", cfg)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	configTestHome(t)
	t.Setenv("LODE_SERVER", "https://env-only.example.com")

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig with no file: %v", err)
	}
	if cfg.ServerURL != "https://env-only.example.com" {
		t.Fatalf("LoadConfig with no file = %+v", cfg)
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	configTestHome(t)

	want := cli.Config{ServerURL: "https://wl.example.com", Token: "wl_" + strings.Repeat("ab", 20)}
	if err := cli.SaveConfig(want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// The file holds only the server; the token no longer touches disk.
	raw, err := cli.ReadRawConfigForTest()
	if err != nil {
		t.Fatalf("read raw config: %v", err)
	}
	if strings.Contains(raw, "token") {
		t.Fatalf("config.toml still has a token line:\n%s", raw)
	}
	// The token round-trips through the (mock) keychain.
	if got, _ := cli.NewKeychainTokenStore().Get(want.ServerURL); got != want.Token {
		t.Fatalf("keychain token = %q; want %q", got, want.Token)
	}

	// LoadConfig reconstructs the full config from file + keychain.
	got, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.ServerURL != want.ServerURL || got.Token != want.Token {
		t.Fatalf("round-trip = %+v, want %+v", got, want)
	}
}

func TestLoadConfigResolvesTokenFromKeychain(t *testing.T) {
	configTestHome(t)

	// config.toml has only server.
	if err := cli.SaveServerOnly("https://wl.example.com"); err != nil {
		t.Fatalf("save server: %v", err)
	}
	if err := cli.NewKeychainTokenStore().Set("https://wl.example.com", "wl_kc"); err != nil {
		t.Fatalf("seed keychain: %v", err)
	}
	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Token != "wl_kc" {
		t.Fatalf("token = %q; want wl_kc", cfg.Token)
	}
}

func TestEnvTokenBeatsKeychain(t *testing.T) {
	configTestHome(t)
	t.Setenv("LODE_SERVER", "https://wl.example.com")
	t.Setenv("LODE_TOKEN", "wl_env")
	_ = cli.NewKeychainTokenStore().Set("https://wl.example.com", "wl_kc")

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Token != "wl_env" {
		t.Fatalf("token = %q; want wl_env (env overrides keychain)", cfg.Token)
	}
}

func TestSaveConfigWritesKeychainAndStripsLegacyToken(t *testing.T) {
	configTestHome(t)

	// Simulate a legacy cleartext config.toml with a token line.
	if err := cli.WriteRawConfigForTest("server = \"https://wl.example.com\"\ntoken = \"wl_old\"\n"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := cli.SaveConfig(cli.Config{ServerURL: "https://wl.example.com", Token: "wl_new"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Keychain now holds the new token.
	if got, _ := cli.NewKeychainTokenStore().Get("https://wl.example.com"); got != "wl_new" {
		t.Fatalf("keychain token = %q; want wl_new", got)
	}
	// File no longer contains a token line.
	raw, _ := cli.ReadRawConfigForTest()
	if strings.Contains(raw, "token") {
		t.Fatalf("config.toml still has a token line:\n%s", raw)
	}
}

// failingTokenStore is a TokenStore whose Set always errors, for the
// keychain-write-failure path.
type failingTokenStore struct{ err error }

func (f failingTokenStore) Get(string) (string, error) { return "", f.err }
func (f failingTokenStore) Set(string, string) error   { return f.err }
func (f failingTokenStore) Delete(string) error        { return f.err }

func TestSaveConfigKeychainWriteFailureStillSavesServer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_TOKEN", "")
	t.Setenv("LODE_SERVER", "")

	sentinel := errors.New("keychain unavailable")
	restore := cli.SwapTokenStoreForTest(failingTokenStore{err: sentinel})
	t.Cleanup(restore)

	err := cli.SaveConfig(cli.Config{ServerURL: "https://wl.example.com", Token: "wl_new"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("SaveConfig err = %v; want the keychain error", err)
	}
	// The server is not a secret, and the LODE_TOKEN guidance this failure
	// carries is useless without it: the next command would die on "server URL
	// not set" instead of using the exported token.
	raw, err := cli.ReadRawConfigForTest()
	if err != nil {
		t.Fatalf("read config after keychain failure: %v", err)
	}
	if !strings.Contains(raw, `server = "https://wl.example.com"`) {
		t.Fatalf("config.toml did not record the server:\n%s", raw)
	}
	if strings.Contains(raw, "wl_new") {
		t.Fatalf("config.toml leaked the token:\n%s", raw)
	}
}

func TestSaveConfigKeychainWriteFailureKeepsLegacyToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_TOKEN", "")
	t.Setenv("LODE_SERVER", "")

	sentinel := errors.New("keychain unavailable")
	restore := cli.SwapTokenStoreForTest(failingTokenStore{err: sentinel})
	t.Cleanup(restore)

	if err := cli.WriteRawConfigForTest("server = \"https://wl.example.com\"\ntoken = \"wl_old\"\n"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := cli.SaveConfig(cli.Config{ServerURL: "https://wl.example.com", Token: "wl_new"}); !errors.Is(err, sentinel) {
		t.Fatalf("SaveConfig err = %v; want the keychain error", err)
	}
	// Nothing migrated the legacy token into the keychain, so stripping it here
	// would destroy the only copy the user has.
	raw, err := cli.ReadRawConfigForTest()
	if err != nil {
		t.Fatalf("read config after keychain failure: %v", err)
	}
	if !strings.Contains(raw, `token = "wl_old"`) {
		t.Fatalf("config.toml dropped the legacy token:\n%s", raw)
	}
}

func TestLoadConfigServerOverrideDropsLegacyFileToken(t *testing.T) {
	configTestHome(t)

	// Legacy cleartext file: server + token both point at the file server.
	if err := cli.WriteRawConfigForTest("server = \"https://file.example.com\"\ntoken = \"wl_filetoken\"\n"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	// LODE_SERVER overrides to a different server with no keychain entry.
	t.Setenv("LODE_SERVER", "https://other.example.com")

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "https://other.example.com" {
		t.Fatalf("server = %q; want the override", cfg.ServerURL)
	}
	if cfg.Token != "" {
		t.Fatalf("token = %q; the file's legacy token must not leak onto the overridden server", cfg.Token)
	}
}

func TestLoadConfigMalformed(t *testing.T) {
	for name, content := range map[string]string{
		"missing equals": "not a key value pair\n",
		"unknown key":    "bogus = \"value\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			configTestHome(t)
			if err := cli.WriteRawConfigForTest(content); err != nil {
				t.Fatalf("write config file: %v", err)
			}
			if _, err := cli.LoadConfig(); err == nil {
				t.Fatalf("LoadConfig with malformed file: err = nil, want error")
			}
		})
	}
}

// writeRepoConfig writes content to <dir>/<confDir>/config.toml, creating the
// directory.
func writeRepoConfig(t *testing.T, dir, confDir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, confDir), 0o755); err != nil {
		t.Fatalf("mkdir %s/%s: %v", dir, confDir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, confDir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s/%s/config.toml: %v", dir, confDir, err)
	}
}

// configTestHome points $HOME at a fresh temp dir with a mock keychain and no
// LODE_* environment, so a config test sees only what it writes. A test that
// wants an override sets it after calling this.
func configTestHome(t *testing.T) string {
	t.Helper()
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_SERVER", "")
	t.Setenv("LODE_TOKEN", "")
	return dir
}

// repoTestHome sets up a fake $HOME with a user config and returns the home
// directory plus a nested repo working directory (<home>/git/proj/sub) to load
// from.
func repoTestHome(t *testing.T, userConfig string) (home, workDir string) {
	t.Helper()
	home = configTestHome(t)
	if userConfig != "" {
		if err := cli.WriteRawConfigForTest(userConfig); err != nil {
			t.Fatalf("write user config: %v", err)
		}
	}
	workDir = filepath.Join(home, "git", "proj", "sub")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	return home, workDir
}

func TestLoadConfigCurrentProjectFromUserConfig(t *testing.T) {
	_, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\ncurrent_project = \"user-proj\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "user-proj" {
		t.Fatalf("current project = %q; want user-proj", cfg.CurrentProject)
	}
}

func TestRepoConfigOverridesCurrentProject(t *testing.T) {
	for _, confDir := range []string{".worklode", ".lode"} {
		t.Run(confDir, func(t *testing.T) {
			home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\ncurrent_project = \"user-proj\"\n")
			// The repo config sits two levels above the working directory, so
			// finding it exercises the upward walk.
			writeRepoConfig(t, filepath.Join(home, "git", "proj"), confDir, "current_project = \"repo-proj\"\n")

			cfg, err := cli.LoadConfigFromForTest(workDir)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.CurrentProject != "repo-proj" {
				t.Fatalf("current project = %q; want repo-proj", cfg.CurrentProject)
			}
			if cfg.ServerURL != "https://wl.example.com" {
				t.Fatalf("server = %q; the repo config must not clear unset keys", cfg.ServerURL)
			}
		})
	}
}

func TestCurrentProjectFromRepoConfig(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")
	writeRepoConfig(t, filepath.Join(home, "git", "proj"), ".worklode", "current_project = \"repo-proj\"\n")

	if got := cli.CurrentProjectFrom(workDir); got != "repo-proj" {
		t.Fatalf("CurrentProjectFrom = %q, want %q", got, "repo-proj")
	}
}

func TestCurrentProjectFromUserConfigFallback(t *testing.T) {
	_, workDir := repoTestHome(t, "current_project = \"user-proj\"\n") // no repo-local config written

	if got := cli.CurrentProjectFrom(workDir); got != "user-proj" {
		t.Fatalf("CurrentProjectFrom = %q, want %q", got, "user-proj")
	}
}

func TestLoadConfigProjectKeyFromUserConfig(t *testing.T) {
	_, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\nproject_key = \"WL\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ProjectKey != "WL" {
		t.Fatalf("project key = %q; want WL", cfg.ProjectKey)
	}
}

func TestLoadConfigProjectKeyAbsent(t *testing.T) {
	_, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ProjectKey != "" {
		t.Fatalf("project key = %q; want empty when unset", cfg.ProjectKey)
	}
}

func TestRepoConfigOverridesProjectKey(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\nproject_key = \"USER\"\n")
	writeRepoConfig(t, filepath.Join(home, "git", "proj"), ".worklode", "project_key = \"WL\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ProjectKey != "WL" {
		t.Fatalf("project key = %q; want WL (repo overrides user)", cfg.ProjectKey)
	}
}

func TestRepoConfigNearestWins(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")
	writeRepoConfig(t, filepath.Join(home, "git"), ".worklode", "current_project = \"outer\"\n")
	writeRepoConfig(t, filepath.Join(home, "git", "proj"), ".worklode", "current_project = \"inner\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "inner" {
		t.Fatalf("current project = %q; want inner (nearest config wins)", cfg.CurrentProject)
	}
}

func TestRepoConfigWorklodeBeatsLodeAtSameLevel(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "current_project = \"from-worklode\"\n")
	writeRepoConfig(t, repo, ".lode", "current_project = \"from-lode\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "from-worklode" {
		t.Fatalf("current project = %q; want from-worklode", cfg.CurrentProject)
	}
}

func TestRepoConfigWalkStopsAtHome(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")
	// A .worklode in $HOME itself is not a repo config and must be ignored.
	writeRepoConfig(t, home, ".worklode", "current_project = \"home-proj\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "" {
		t.Fatalf("current project = %q; the walk must stop before $HOME", cfg.CurrentProject)
	}
}

func TestRepoConfigRejectsToken(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")
	writeRepoConfig(t, filepath.Join(home, "git", "proj"), ".worklode", "token = \"wl_leaked\"\n")

	_, err := cli.LoadConfigFromForTest(workDir)
	if err == nil {
		t.Fatal("load with a token in the repo config: err = nil, want error")
	}
	if !strings.Contains(err.Error(), "must not set a token") {
		t.Fatalf("err = %v; want it to explain that repo configs may not set a token", err)
	}
}

func TestRepoConfigServerOverrideDropsLegacyFileToken(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://file.example.com\"\ntoken = \"wl_filetoken\"\n")
	writeRepoConfig(t, filepath.Join(home, "git", "proj"), ".worklode", "server = \"https://repo.example.com\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "https://repo.example.com" {
		t.Fatalf("server = %q; want the repo config's server", cfg.ServerURL)
	}
	if cfg.Token != "" {
		t.Fatalf("token = %q; the user config's legacy token must not leak onto the repo's server", cfg.Token)
	}
}

func TestSaveServerOnlyPreservesCurrentProject(t *testing.T) {
	configTestHome(t)

	if err := cli.WriteRawConfigForTest("server = \"https://old.example.com\"\ncurrent_project = \"keepme\"\n"); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := cli.SaveServerOnly("https://new.example.com"); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, err := cli.LoadConfigFromForTest("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "https://new.example.com" || cfg.CurrentProject != "keepme" {
		t.Fatalf("config after SaveServerOnly = %+v; want the new server and current_project kept", cfg)
	}
}

func TestSaveServerOnlyPreservesProjectKey(t *testing.T) {
	configTestHome(t)

	if err := cli.WriteRawConfigForTest("server = \"https://old.example.com\"\nproject_key = \"WL\"\n"); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := cli.SaveServerOnly("https://new.example.com"); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, err := cli.LoadConfigFromForTest("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "https://new.example.com" || cfg.ProjectKey != "WL" {
		t.Fatalf("config after SaveServerOnly = %+v; want the new server and project_key kept", cfg)
	}
}

func TestCurrentProjectPathRecordsSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "worklode")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user config: %v", err)
	}
	userPath := filepath.Join(userDir, "config.toml")
	if err := os.WriteFile(userPath, []byte("current_project = \"from-user\"\n"), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	cfg, err := cli.LoadConfigFromForTest(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "from-user" || cfg.CurrentProjectPath != userPath {
		t.Fatalf("user config: project=%q path=%q; want from-user, %s",
			cfg.CurrentProject, cfg.CurrentProjectPath, userPath)
	}

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir repo config: %v", err)
	}
	repoPath := filepath.Join(repo, ".worklode", "config.toml")
	if err := os.WriteFile(repoPath, []byte("current_project = \"from-repo\"\n"), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}

	cfg, err = cli.LoadConfigFromForTest(repo)
	if err != nil {
		t.Fatalf("load from repo: %v", err)
	}
	if cfg.CurrentProject != "from-repo" || cfg.CurrentProjectPath != repoPath {
		t.Fatalf("repo config: project=%q path=%q; want from-repo, %s",
			cfg.CurrentProject, cfg.CurrentProjectPath, repoPath)
	}
}

// WorktreeDirFrom, not LoadConfig/loadConfigFrom, is the sole reader of
// worktree_dir (spec 008 §6 scopes it to the repo-local config only) — see
// Config.WorktreeDir's doc. These two tests exercise it directly.

func TestWorktreeDirFromRepoConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "worktree_dir = \"wtrees\"\n")
	if got := cli.WorktreeDirFrom(repo); got != "wtrees" {
		t.Errorf("WorktreeDirFrom = %q, want wtrees", got)
	}
}

func TestWorktreeDirEnvOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LODE_WORKTREE_DIR", "from-env")
	// LODE_TOKEN set but otherwise irrelevant here: WorktreeDirFrom is
	// deliberately independent of loadConfigFrom (no keychain, no token, no
	// LODE_TOKEN early return) — this pins that the env override applies
	// regardless of unrelated client-config env state, not just in isolation.
	t.Setenv("LODE_TOKEN", "wl_"+strings.Repeat("a", 40))
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "worktree_dir = \"wtrees\"\n")
	if got := cli.WorktreeDirFrom(repo); got != "from-env" {
		t.Errorf("WorktreeDirFrom = %q, want from-env", got)
	}
}

// TestLoadConfigFromNeverPopulatesWorktreeDir pins the invariant that keeps
// a user-level worktree_dir from diverging from what internal/hookrun's guard
// sees: loadConfigFrom (LoadConfig's implementation) must leave
// Config.WorktreeDir empty even when BOTH a user-level and a repo-level
// config set worktree_dir — WorktreeDirFrom, not this merged Config, is the
// sole reader (spec 008 §6; see Config.WorktreeDir's doc). Today this is
// correct only by inspection (cfg.WorktreeDir = "" in loadConfigFrom, and
// merge() never touching it); this test would fail if either of those broke.
func TestLoadConfigFromNeverPopulatesWorktreeDir(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\nworktree_dir = \"user-wtrees\"\n")
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "worktree_dir = \"repo-wtrees\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.WorktreeDir != "" {
		t.Fatalf("Config.WorktreeDir = %q, want \"\" (worktree_dir must never merge into Config; use WorktreeDirFrom)", cfg.WorktreeDir)
	}
}

// The file corpus is retired: documents live in the backbone (spec 025), and
// nothing reads spec_corpus / plan_corpus any more. They are still accepted
// and ignored, so a checkout that has not dropped them from its config.toml
// yet does not fail every command on parseConfig's unknown-key error.
func TestLoadConfigIgnoresRetiredCorpusKeys(t *testing.T) {
	home, workDir := repoTestHome(t,
		"server = \"https://wl.example.com\"\nspec_corpus = \"user-specs\"\n")
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode",
		"current_project = \"proj\"\nspec_corpus = \"docs/specs\"\nplan_corpus = \"docs/plans\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "https://wl.example.com" {
		t.Errorf("ServerURL = %q, want the user config's value", cfg.ServerURL)
	}
	if cfg.CurrentProject != "proj" {
		t.Errorf("CurrentProject = %q, want proj", cfg.CurrentProject)
	}
}
