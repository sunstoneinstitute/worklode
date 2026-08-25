package hookrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestSessionStartOutputPerHarness(t *testing.T) {
	_, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Harness brief")

	run := func(harness, sessionID string) string {
		t.Helper()
		var outBuf, errBuf bytes.Buffer
		code := Run(context.Background(), Options{
			Event:   "session-start",
			Harness: harness,
			Stdin: bytes.NewReader(payloadJSON(t, Payload{
				Cwd: wtDir, SessionID: sessionID, HookEventName: "SessionStart"})),
			Stdout: &outBuf,
			Stderr: &errBuf,
		})
		if code != 0 {
			t.Fatalf("session-start --harness %s exit = %d (stderr: %s)", harness, code, errBuf.String())
		}
		return outBuf.String()
	}

	amp := run("amp", "s-amp")
	if strings.Contains(amp, "hookSpecificOutput") {
		t.Fatalf("amp stdout is the Claude envelope, want plain text: %q", amp)
	}
	if !strings.Contains(amp, taskID) || !strings.Contains(amp, "Harness brief") {
		t.Fatalf("amp stdout missing the brief: %q", amp)
	}

	if out := run("copilot", "s-copilot"); out != "" {
		t.Fatalf("copilot stdout = %q, want empty (no verified consumer)", out)
	}

	// Codex parses stdout against session-start.command.output, which sets
	// additionalProperties:false at both levels and drops the context
	// entirely when JSON-looking stdout fails to parse. So assert the exact
	// key set, not just that the brief is somewhere in there: an extra key
	// is not a cosmetic diff, it silently costs Codex the whole brief.
	var codex map[string]any
	codexOut := run("codex", "s-codex")
	if err := json.Unmarshal([]byte(codexOut), &codex); err != nil {
		t.Fatalf("codex stdout is not valid JSON: %v\nstdout: %s", err, codexOut)
	}
	if len(codex) != 1 {
		t.Fatalf("codex envelope top-level keys = %v, want only hookSpecificOutput", keysOf(codex))
	}
	hso, ok := codex["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("codex envelope has no hookSpecificOutput object: %s", codexOut)
	}
	if len(hso) != 2 {
		t.Fatalf("codex hookSpecificOutput keys = %v, want exactly hookEventName and additionalContext", keysOf(hso))
	}
	if hso["hookEventName"] != "SessionStart" {
		t.Fatalf("codex hookEventName = %v, want SessionStart", hso["hookEventName"])
	}
	ctx, _ := hso["additionalContext"].(string)
	if !strings.Contains(ctx, taskID) || !strings.Contains(ctx, "Harness brief") {
		t.Fatalf("codex additionalContext missing the brief: %q", ctx)
	}
}

// keysOf names a map's keys for a failure message.
func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func TestSessionStartEmitsAdditionalContext(t *testing.T) {
	_, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Brief context")

	stdout, _ := runSessionStart(t, wtDir, "s-emit")

	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not valid additionalContext JSON: %v\nstdout: %s", err, stdout)
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hookEventName = %q, want SessionStart", out.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, taskID) {
		t.Fatalf("additionalContext missing task id %q: %q", taskID, out.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "Brief context") {
		t.Fatalf("additionalContext missing task title: %q", out.HookSpecificOutput.AdditionalContext)
	}

	// The session marker must have been written and read as fresh (our pid).
	if !sessionMarkerFresh(wtDir) {
		t.Fatalf("session marker not written/fresh after session-start")
	}
}

// TestSessionStartNormalizesHarnessPayload drives session-start with
// Harness: "codex" and a camelCase payload (no cwd/session_id keys at all)
// against a worktree whose lease has expired, and asserts it re-acquires the
// lease and touches the session exactly as the Claude Code shape does — spec
// 024 acceptance 3: same behaviour, same backbone rows, different payload on
// stdin.
func TestSessionStartNormalizesHarnessPayload(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Codex resume")
	expireLease(t, st, taskID)

	raw := []byte(fmt.Sprintf(`{"cwd":%q,"sessionId":"s9"}`, wtDir))
	var outBuf, errBuf bytes.Buffer
	code := Run(context.Background(), Options{
		Event:   "session-start",
		Harness: "codex",
		Stdin:   bytes.NewReader(raw),
		Stdout:  &outBuf,
		Stderr:  &errBuf,
	})
	if code != 0 {
		t.Fatalf("session-start exit code = %d, want 0 (stderr: %s)", code, errBuf.String())
	}
	if !rec.hitAny("/claim") {
		t.Fatalf("claim endpoint was not hit for auto-resume; paths: %v", rec.list())
	}

	after, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task after auto-resume: %v", err)
	}
	if after.State != "in_progress" || after.Lease == nil {
		t.Fatalf("task after auto-resume = state %q lease %+v, want in_progress/non-nil", after.State, after.Lease)
	}

	lease, err := st.ActiveLease(context.Background(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	sess, err := st.AgentSession(context.Background(), lease.ID, "codex", "s9")
	if err != nil {
		t.Fatalf("agent session: %v", err)
	}
	if sess.EndedAt != nil {
		t.Fatal("session should still be open")
	}
}

// TestSessionUnknownAgentRecordsOther covers the only path LODE_AGENT is set
// by hand on. An id the backbone has no entry for — a typo, or a harness
// worklode does not know yet — used to be posted verbatim, which
// store.TouchAgentSession rejects (it mirrors the agent_sessions.agent CHECK
// constraint). Because a hook downgrades every backbone failure to a warning
// so the triggering event still succeeds, that rejection lost the session
// with no signal the flag was the problem. It is folded onto "other" instead:
// the row lands, and the warning names the id that was not recognised.
func TestSessionUnknownAgentRecordsOther(t *testing.T) {
	t.Setenv("LODE_AGENT", "codx")
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Typo harness task")

	_, stderr := runHookOutput(t, "session-start", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if !strings.Contains(stderr, `codx`) || !strings.Contains(stderr, `"other"`) {
		t.Fatalf("stderr did not warn about the unknown agent: %q", stderr)
	}

	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	if _, err := st.AgentSession(t.Context(), lease.ID, "other", "sess-1"); err != nil {
		t.Fatalf("session was not recorded under %q: %v", model.AgentOther, err)
	}

	// The close half of the lifecycle normalizes identically, or the session
	// would stay open forever.
	runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1"})
	sess, err := st.AgentSession(t.Context(), lease.ID, "other", "sess-1")
	if err != nil {
		t.Fatalf("agent session after end: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatal("session-end did not close the session recorded as \"other\"")
	}
}
