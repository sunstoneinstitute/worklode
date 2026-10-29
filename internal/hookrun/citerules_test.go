package hookrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// rulesServer answers GET /api/v1/rules?doc=WL-SPEC-77 with WL-REQ-165
// arranged at sec-4.
func rulesServer(t *testing.T) func() (*cli.Client, error) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/rules" || r.URL.Query().Get("doc") != "WL-SPEC-77" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"ref":"WL-REQ-165","arranged_in":[{"doc_ref":"WL-SPEC-77","anchor":"sec-4"}]}]`))
	}))
	t.Cleanup(ts.Close)
	return func() (*cli.Client, error) {
		return cli.NewClient(cli.Config{ServerURL: ts.URL, Token: "wl_" + strings.Repeat("0", 40)}), nil
	}
}

// commitMsgIn runs commit-msg over message in a plain repo (no task
// worktree: the citation check runs in every checkout) and returns the exit
// code and stderr.
func commitMsgIn(t *testing.T, message string, newClient func() (*cli.Client, error)) (int, string) {
	t.Helper()
	return commitMsgAt(t, initGitRepo(t), message, newClient)
}

// commitMsgAt runs commit-msg over message in the repo at root.
func commitMsgAt(t *testing.T, root, message string, newClient func() (*cli.Client, error)) (int, string) {
	t.Helper()
	rel := filepath.Join(".git", "COMMIT_EDITMSG")
	if err := os.WriteFile(filepath.Join(root, rel), []byte(message), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:     "commit-msg",
		Args:      []string{rel},
		Stdin:     bytes.NewReader(payloadJSON(t, Payload{Cwd: root})),
		Stdout:    &stdout,
		Stderr:    &stderr,
		NewClient: newClient,
	})
	return code, stderr.String()
}

func TestCommitMsgRefusesSectionCitationNamingRule(t *testing.T) {
	code, stderr := commitMsgIn(t, "Fix the thing\n\nAs WL-SPEC-77 §4 says.\n", rulesServer(t))
	if code == 0 {
		t.Fatal("a message citing a spec section must be refused")
	}
	if !strings.Contains(stderr, "WL-REQ-165") || !strings.Contains(stderr, "WL-REQ-1791") {
		t.Fatalf("refusal must name the rule ref: %q", stderr)
	}
}

func TestCommitMsgRefusalFallsBackOffline(t *testing.T) {
	code, stderr := commitMsgIn(t, "Fix the thing\n\nAs WL-SPEC-77 §4 says.\n", offline)
	if code == 0 || !strings.Contains(stderr, "lode show WL-SPEC-77#sec-4") {
		t.Fatalf("offline refusal must name lode show: code=%d stderr=%q", code, stderr)
	}
}

func TestCommitMsgPassesRuleRef(t *testing.T) {
	msg := "Fix the thing\n\nAs WL-REQ-165 says.\n# WL-SPEC-77 §4 in a comment line\n" +
		"# ------------------------ >8 ------------------------\n+// WL-SPEC-77 §4 in a verbose diff\n"
	if code, stderr := commitMsgIn(t, msg, rulesServer(t)); code != 0 {
		t.Fatalf("a message citing a rule ref must pass: code=%d stderr=%q", code, stderr)
	}
}

func TestSubagentStartInjectsCiteRules(t *testing.T) {
	stdout, _ := runHookOutput(t, "subagent-start", Payload{Cwd: t.TempDir(), HookEventName: "SubagentStart"})
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not additionalContext JSON: %v\n%s", err, stdout)
	}
	if out.HookSpecificOutput.HookEventName != "SubagentStart" || out.HookSpecificOutput.AdditionalContext != citeRulesContext {
		t.Fatalf("subagent-start output = %+v", out.HookSpecificOutput)
	}
}

func TestSessionStartInjectsCiteRulesOutsideWorktree(t *testing.T) {
	stdout, _ := runHookOutput(t, "session-start", Payload{Cwd: initGitRepo(t), HookEventName: "SessionStart"})
	if !strings.Contains(additionalContext(t, stdout), citeRulesContext) {
		t.Fatalf("session-start context must carry the citation rule: %q", stdout)
	}
}

var offline = func() (*cli.Client, error) { return nil, errors.New("no server") }

// gatedRepo is a repo whose [gate] table guards guarded/**, with origin/main
// at its first commit.
func gatedRepo(t *testing.T) *gitRepo {
	t.Helper()
	r := &gitRepo{t: t, root: t.TempDir()}
	r.git("init")
	if err := os.MkdirAll(filepath.Join(r.root, ".worklode"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.commit(".worklode/config.toml", "[gate]\npaths = [\"guarded/**\"]\n", "add gate")
	r.git("update-ref", "refs/remotes/origin/main", "HEAD")
	if err := os.MkdirAll(filepath.Join(r.root, "guarded"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, ok := worktree.Root(r.root)
	if !ok {
		t.Fatalf("worktree.Root(%s): ok = false", r.root)
	}
	r.root = root
	return r
}

// stage writes name and adds it to the index without committing.
func (r *gitRepo) stage(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.root, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", name)
}

func TestCommitMsgGateRefusesGuardedPathWithoutTrailer(t *testing.T) {
	r := gatedRepo(t)
	r.stage("guarded/a.go", "package a\n")
	code, stderr := commitMsgAt(t, r.root, "Change a\n", offline)
	if code == 0 || !strings.Contains(stderr, "guarded/a.go") || !strings.Contains(stderr, "Spec:") {
		t.Fatalf("want a refusal naming the path and the trailer: code=%d stderr=%q", code, stderr)
	}
}

func TestCommitMsgGatePassesWithTrailer(t *testing.T) {
	r := gatedRepo(t)
	r.stage("guarded/a.go", "package a\n")
	if code, stderr := commitMsgAt(t, r.root, "Change a\n\nSpec: WL-REQ-7\n", offline); code != 0 {
		t.Fatalf("a trailer must pass: code=%d stderr=%q", code, stderr)
	}
}

func TestCommitMsgGateRefusesMalformedTrailer(t *testing.T) {
	r := gatedRepo(t)
	r.stage("guarded/a.go", "package a\n")
	code, stderr := commitMsgAt(t, r.root, "Change a\n\nSpec: none image tag bump\n", offline)
	if code == 0 || !strings.Contains(stderr, "none takes one reason") {
		t.Fatalf("want a malformed-trailer refusal: code=%d stderr=%q", code, stderr)
	}
}

func TestCommitMsgGateAcceptsTrailerEarlierOnBranch(t *testing.T) {
	r := gatedRepo(t)
	r.commit("guarded/a.go", "package a\n", "Add a\n\nSpec: WL-REQ-7")
	r.stage("guarded/b.go", "package a\n")
	if code, stderr := commitMsgAt(t, r.root, "Add b\n", offline); code != 0 {
		t.Fatalf("a trailer earlier on the branch must pass: code=%d stderr=%q", code, stderr)
	}
}

func TestCommitMsgGateRefusesSectionCitationInStagedLines(t *testing.T) {
	r := gatedRepo(t)
	r.stage("notes.md", "As WL-SPEC-77 §4 says.\n")
	code, stderr := commitMsgAt(t, r.root, "Add notes\n", offline)
	if code == 0 || !strings.Contains(stderr, "notes.md") || !strings.Contains(stderr, "WL-REQ-1791") {
		t.Fatalf("want a citation refusal naming the file: code=%d stderr=%q", code, stderr)
	}
}

func TestCommitMsgGateSkipsMerge(t *testing.T) {
	r := gatedRepo(t)
	r.stage("guarded/a.go", "package a\n")
	if err := os.WriteFile(filepath.Join(r.root, ".git", "MERGE_HEAD"), []byte(r.git("rev-parse", "HEAD")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stderr := commitMsgAt(t, r.root, "Merge\n", offline); code != 0 {
		t.Fatalf("a merge commit must not be gated: code=%d stderr=%q", code, stderr)
	}
}
