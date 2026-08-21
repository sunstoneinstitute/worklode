package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/harness"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/skillhash"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// skillsTestServer is lifecycleTestServer plus a counter of archive-download
// requests (so install tests can assert a repeat install performs no fetch)
// and a temp LODE_SKILLS_DIR (so no test can forget to isolate the local
// store and scribble in a developer's home directory). Returns the store,
// client, archive-hit counter, and the skills-dir root.
func skillsTestServer(t *testing.T) (*store.Store, *cli.Client, *int32, string) {
	t.Helper()
	var archiveHits int32
	st, c := testServer(t, api.Config{}, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/archive/") {
				atomic.AddInt32(&archiveHits, 1)
			}
			h.ServeHTTP(w, r)
		})
	})

	root := t.TempDir()
	t.Setenv("LODE_SKILLS_DIR", root)

	return st, c, &archiveHits, root
}

// seedSkill upserts one skill with a deterministic hash and a fake (non-tar)
// archive — good enough for list/get/recommend tests, which never extract
// it.
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

// seedInstallableSkill upserts a skill backed by a real tar.gz archive whose
// content hash is correctly computed — install tests extract it for real via
// skillstore.Ensure, unlike seedSkill's fixtures.
func seedInstallableSkill(t *testing.T, st *store.Store, name string) (hash, content string) {
	t.Helper()
	content = "# " + name + "\n\nDo the thing.\n"
	archive := buildTarGz(t, map[string]string{"SKILL.md": content})
	hash = skillhash.Sum([]skillhash.File{{Path: "SKILL.md", Data: []byte(content)}})
	_, _, err := st.UpsertSkill(context.Background(), store.SkillUpsert{
		Qualifier:   "acme",
		Name:        name,
		Description: "desc for " + name,
		SourceRepo:  "acme/skills",
		SourcePath:  "skills/" + name,
		GitCommit:   "deadbeef",
		ContentHash: hash,
		SkillMD:     content,
		Frontmatter: json.RawMessage(`{}`),
		Archive:     archive,
	})
	if err != nil {
		t.Fatalf("seed installable skill %s: %v", name, err)
	}
	return hash, content
}

// seedInstallableSkillWithHash is seedInstallableSkill but the caller
// supplies (and can deliberately mis-state) the content hash, so the
// server-advertised hash and the archive's real content can disagree — the
// seam that surfaces a corrupt or truncated download to a human.
func seedInstallableSkillWithHash(t *testing.T, st *store.Store, name, hash string) (content string) {
	t.Helper()
	content = "# " + name + "\n\nDo the thing.\n"
	archive := buildTarGz(t, map[string]string{"SKILL.md": content})
	_, _, err := st.UpsertSkill(context.Background(), store.SkillUpsert{
		Qualifier:   "acme",
		Name:        name,
		Description: "desc for " + name,
		SourceRepo:  "acme/skills",
		SourcePath:  "skills/" + name,
		GitCommit:   "deadbeef",
		ContentHash: hash,
		SkillMD:     content,
		Frontmatter: json.RawMessage(`{}`),
		Archive:     archive,
	})
	if err != nil {
		t.Fatalf("seed installable skill %s: %v", name, err)
	}
	return content
}

func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		body := []byte(content)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// runLodeOutErr is runLode but keeps stdout and stderr in separate buffers,
// for tests asserting which stream a command writes to.
func runLodeOutErr(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetFlags(t, rootCmd)
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs(args)
	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestSkillsListTableAndJSON(t *testing.T) {
	st, _, _, _ := skillsTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")
	seedSkill(t, st, "debugging", "Systematic debugging loop")

	out, err := runLode(t, "skills", "list")
	if err != nil {
		t.Fatalf("skills list: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "tdd        Red-green-refactor discipline\n") ||
		!strings.Contains(out, "debugging  Systematic debugging loop\n") {
		t.Fatalf("skills list table output = %q", out)
	}

	out, err = runLode(t, "skills", "list", "--json")
	if err != nil {
		t.Fatalf("skills list --json: %v\noutput: %s", err, out)
	}
	var resp struct {
		Skills []model.Skill `json:"skills"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode skills list --json output %q: %v", out, err)
	}
	if len(resp.Skills) != 2 {
		t.Fatalf("skills list --json = %+v, want 2 skills", resp.Skills)
	}
}

func TestSkillsRecommendFlagValidation(t *testing.T) {
	skillsTestServer(t)

	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"none", nil, true},
		{"task and text", []string{"--task", "WL-1", "--text", "x"}, true},
		{"task and file", []string{"--task", "WL-1", "--file", "/nonexistent"}, true},
		{"text alone", []string{"--text", "write tests first"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"skills", "recommend"}, tc.args...)
			_, err := runLode(t, args...)
			if tc.wantErr && err == nil {
				t.Fatalf("skills recommend %v: want error, got nil", tc.args)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("skills recommend %v: %v", tc.args, err)
			}
		})
	}
}

func TestSkillsRecommendFileFlag(t *testing.T) {
	skillsTestServer(t)
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte("write tests first"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := runLode(t, "skills", "recommend", "--file", path); err != nil {
		t.Fatalf("skills recommend --file: %v", err)
	}
}

func TestSkillsRecommendEmptyFile(t *testing.T) {
	skillsTestServer(t)
	path := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(path, []byte("   \n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err := runLode(t, "skills", "recommend", "--file", path)
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("skills recommend --file (empty): want an 'is empty' error, got %v", err)
	}
}

func TestSkillsRecommendWarningsOnStderr(t *testing.T) {
	st, c, _, _ := skillsTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")
	if _, _, err := c.CreateProject(context.Background(), model.CreateProjectInput{ID: "proj", Name: "Project", Key: "PROJ"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, _, err := c.CreateTask(context.Background(), model.CreateTaskInput{
		Project: "proj", Title: "Fix it", Priority: "high", Kind: "bug",
		Skills: []string{"tdd", "ghost"},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	stdout, stderr, err := runLodeOutErr(t, "skills", "recommend", "--task", task.ID)
	if err != nil {
		t.Fatalf("skills recommend --task: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "pinned\tacme:tdd\t") {
		t.Fatalf("stdout = %q, want a pinned tdd line", stdout)
	}
	if strings.Contains(stdout, "ghost") {
		t.Fatalf("stdout = %q, warning about ghost leaked into stdout", stdout)
	}
	if !strings.Contains(stderr, "warning: pinned skill not found: ghost") {
		t.Fatalf("stderr = %q, want a warning about the missing ghost skill", stderr)
	}
	if strings.Contains(stderr, "pinned\tacme:tdd") {
		t.Fatalf("stderr = %q, pinned result leaked into stderr", stderr)
	}
}

func TestSkillsInstallResolvesHash(t *testing.T) {
	st, _, archiveHits, _ := skillsTestServer(t)
	_, content := seedInstallableSkill(t, st, "tdd")

	out, err := runLode(t, "skills", "install", "tdd")
	if err != nil {
		t.Fatalf("skills install tdd: %v\noutput: %s", err, out)
	}
	if atomic.LoadInt32(archiveHits) != 1 {
		t.Fatalf("archive hits = %d, want 1", atomic.LoadInt32(archiveHits))
	}
	path := strings.TrimSpace(out)
	got, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed SKILL.md at %s: %v", path, err)
	}
	if string(got) != content {
		t.Fatalf("installed SKILL.md = %q, want %q", got, content)
	}
}

func TestSkillsInstallIdempotent(t *testing.T) {
	st, _, archiveHits, _ := skillsTestServer(t)
	hash, _ := seedInstallableSkill(t, st, "tdd")

	if _, err := runLode(t, "skills", "install", "tdd@"+hash); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if atomic.LoadInt32(archiveHits) != 1 {
		t.Fatalf("archive hits after first install = %d, want 1", atomic.LoadInt32(archiveHits))
	}

	if _, err := runLode(t, "skills", "install", "tdd@"+hash); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if atomic.LoadInt32(archiveHits) != 1 {
		t.Fatalf("archive hits after second install = %d, want still 1 (no re-fetch)", atomic.LoadInt32(archiveHits))
	}
}

func TestSkillsInstallHashMismatch(t *testing.T) {
	st, _, _, root := skillsTestServer(t)
	// A well-formed but wrong hash: the archive's real content hashes to
	// something else, simulating a corrupt or truncated download.
	const badHash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	seedInstallableSkillWithHash(t, st, "tdd", badHash)

	// Asserted precisely, not just "some error": a wrong error here (e.g.
	// from a broken hash-resolution step upstream) would let this test pass
	// for the wrong reason while proving nothing about hash verification.
	_, err := runLode(t, "skills", "install", "tdd")
	if err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Fatalf("skills install: want a content-hash mismatch error, got %v", err)
	}

	if _, err := os.Lstat(filepath.Join(root, "tdd")); err == nil {
		t.Fatalf("skills install left a symlink at %s despite the hash mismatch", filepath.Join(root, "tdd"))
	}
	storeDir := filepath.Join(filepath.Dir(root), "store")
	entries, _ := os.ReadDir(storeDir)
	for _, e := range entries {
		if e.Name() == badHash {
			t.Fatalf("skills install left a store dir for the mismatched hash: %s", e.Name())
		}
	}
}

func TestSkillsInstallNameRequired(t *testing.T) {
	skillsTestServer(t)
	for _, arg := range []string{"", "@somehash"} {
		_, err := runLode(t, "skills", "install", arg)
		if err == nil || !strings.Contains(err.Error(), "skill name is required") {
			t.Fatalf("skills install %q: want a name-required error, got %v", arg, err)
		}
	}
}

func TestSkillsInstallDeletedWarnsOnStderr(t *testing.T) {
	st, _, _, _ := skillsTestServer(t)
	_, content := seedInstallableSkill(t, st, "tdd")
	if _, err := st.SoftDeleteSkillsExcept(context.Background(), "acme/skills", nil); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	stdout, stderr, err := runLodeOutErr(t, "skills", "install", "tdd")
	if err != nil {
		t.Fatalf("skills install tdd (deleted): %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "warning: tdd was removed from its source repo") {
		t.Fatalf("stderr = %q, want a removed-from-source-repo warning", stderr)
	}
	path := strings.TrimSpace(stdout)
	got, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed SKILL.md at %s: %v", path, err)
	}
	if string(got) != content {
		t.Fatalf("installed SKILL.md = %q, want %q", got, content)
	}
}

// TestSkillsInstallLink drives `skills install --link`, which needs a real
// store.Store behind skillsTestServer, so it needs Postgres — it skips on
// this machine and runs for real in CI (pgvector runner).
func TestSkillsInstallLink(t *testing.T) {
	st, _, _, _ := skillsTestServer(t)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	_, content := seedInstallableSkill(t, st, "tdd")

	out, err := runLode(t, "skills", "install", "tdd", "--link", claudeCode)
	if err != nil {
		t.Fatalf("skills install --link claude-code: %v\noutput: %s", err, out)
	}
	linkedSkill := filepath.Join(homeDir, ".claude", "skills", "tdd")
	got, err := os.ReadFile(filepath.Join(linkedSkill, "SKILL.md"))
	if err != nil {
		t.Fatalf("read published SKILL.md at %s: %v", linkedSkill, err)
	}
	if string(got) != content {
		t.Fatalf("published SKILL.md = %q, want %q", got, content)
	}

	// --link all touches every registered adapter's personal target, not
	// just claude-code's.
	homeDir2 := t.TempDir()
	t.Setenv("HOME", homeDir2)
	if out, err := runLode(t, "skills", "install", "tdd", "--link", "all"); err != nil {
		t.Fatalf("skills install --link all: %v\noutput: %s", err, out)
	}
	for _, id := range harness.IDs() {
		h, ok := harness.Get(id)
		if !ok {
			continue
		}
		targets, err := h.SkillTargets("", harness.ScopeLocal)
		if err != nil {
			t.Fatalf("%s SkillTargets: %v", id, err)
		}
		for _, target := range targets {
			path := target.Dir
			if target.PerSkill {
				path = filepath.Join(target.Dir, "tdd")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("%s: %s not published by --link all: %v", id, path, err)
			}
		}
	}

	// --link nonsense errors naming the registered ids and "all".
	_, err = runLode(t, "skills", "install", "tdd", "--link", "nonsense")
	if err == nil {
		t.Fatal("skills install --link nonsense: want an error")
	}
	for _, id := range harness.IDs() {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("error %q does not name adapter %s", err.Error(), id)
		}
	}
	if !strings.Contains(err.Error(), "all") {
		t.Fatalf("error %q does not name \"all\"", err.Error())
	}
}

// TestSkillsInstallLinkContinuesAfterOneTargetFails pins publishLinked's
// record-and-continue stance: amp is first in harness.IDs() and shares
// ~/.agents/skills with codex and copilot, so blocking that target (a
// regular file where the directory should go) fails amp's publish before
// claude-code's distinct ~/.claude/skills target is even reached. The
// command must still succeed and still publish to claude-code's target —
// one bad target must not stop --link all from reaching the rest. Needs
// Postgres via skillsTestServer; skips locally, runs in CI.
func TestSkillsInstallLinkContinuesAfterOneTargetFails(t *testing.T) {
	st, _, _, _ := skillsTestServer(t)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	seedInstallableSkill(t, st, "tdd")

	// A regular file at ~/.agents blocks MkdirAll(~/.agents/skills), so
	// amp's (and codex's and copilot's) PublishDirLink call fails.
	if err := os.WriteFile(filepath.Join(homeDir, ".agents"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("seed foreign .agents file: %v", err)
	}

	out, err := runLode(t, "skills", "install", "tdd", "--link", "all")
	if err != nil {
		t.Fatalf("skills install --link all (one target blocked): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "amp:") {
		t.Fatalf("output = %q, want a reported failure for amp's blocked target", out)
	}

	claudeSkill := filepath.Join(homeDir, ".claude", "skills", "tdd")
	if _, err := os.Lstat(claudeSkill); err != nil {
		t.Fatalf("%s not published despite amp's earlier failure: %v", claudeSkill, err)
	}
}
