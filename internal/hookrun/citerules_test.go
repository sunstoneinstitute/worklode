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
	root := initGitRepo(t)
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
	offline := func() (*cli.Client, error) { return nil, errors.New("no server") }
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
