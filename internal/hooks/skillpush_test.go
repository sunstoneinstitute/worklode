package hooks_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// newEnvWithSkillPush builds an env like newEnv, wiring onSkillPush into the
// handler instead of leaving it nil.
func newEnvWithSkillPush(t *testing.T, onSkillPush func(repo, branch string) bool) *env {
	t.Helper()
	st := store.OpenTestStore(t)
	ctx := context.Background()
	if err := st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(ctx, "demo", "sunstoneinstitute/demo"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	return &env{
		st: st,
		h:  hooks.NewGitHubHandler(st, testSecret, slog.Default(), onSkillPush),
	}
}

// skillPushBody builds a minimal push payload for a given repo and ref.
func skillPushBody(repo, ref string) []byte {
	return []byte(`{
		"ref": "` + ref + `",
		"repository": {"full_name": "` + repo + `", "default_branch": "main"},
		"commits": [],
		"head_commit": null
	}`)
}

func TestSkillPushMatchMarksEventAndSkipsIgnored(t *testing.T) {
	var calls []string
	e := newEnvWithSkillPush(t, func(repo, branch string) bool {
		calls = append(calls, repo+"@"+branch)
		return repo == "acme/skills-repo" && branch == "main"
	})

	rr := deliverBody(t, e.h, "push", "d-1", skillPushBody("acme/skills-repo", "refs/heads/main"))
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if typ := e.eventType(t, "d-1"); typ != "push.skills" {
		t.Fatalf("event type = %q, want push.skills", typ)
	}
	if len(calls) != 1 || calls[0] != "acme/skills-repo@main" {
		t.Fatalf("onSkillPush calls = %v", calls)
	}
}

func TestSkillPushNoMatchStaysIgnored(t *testing.T) {
	var calls []string
	e := newEnvWithSkillPush(t, func(repo, branch string) bool {
		calls = append(calls, repo+"@"+branch)
		return repo == "acme/skills-repo" && branch == "main"
	})

	// Same repo, non-matching branch: unmapped, and not a skill push either,
	// so existing "ignored" behavior applies unchanged.
	rr := deliverBody(t, e.h, "push", "d-1", skillPushBody("acme/skills-repo", "refs/heads/dev"))
	if rr.Code != http.StatusOK || status(t, rr) != "ignored" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if typ := e.eventType(t, "d-1"); typ != "push.ignored" {
		t.Fatalf("event type = %q, want push.ignored", typ)
	}
	if len(calls) != 1 {
		t.Fatalf("onSkillPush calls = %v, want exactly 1", calls)
	}
}

// TestSkillPushWinsOverMappedRepoApply proves the skill-push path takes
// priority over the normal apply path, not just over "ignored": a repo that
// is both project-mapped and a matching skill source still yields
// push.skills, and the push's own apply (main_commits bookkeeping) never
// runs.
func TestSkillPushWinsOverMappedRepoApply(t *testing.T) {
	e := newEnvWithSkillPush(t, func(repo, branch string) bool {
		return repo == demoRepo && branch == "main"
	})

	rr := deliver(t, e.h, "push", "d-1", "push_main_ff.json")
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if typ := e.eventType(t, "d-1"); typ != "push.skills" {
		t.Fatalf("event type = %q, want push.skills", typ)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM main_commits`); n != 0 {
		t.Fatalf("main_commits rows = %d, want 0 (apply must not run)", n)
	}
}

// TestSkillPushTagRefDoesNotMatch proves the refs/heads/ gate: a tag push
// never calls onSkillPush, regardless of what it would return.
func TestSkillPushTagRefDoesNotMatch(t *testing.T) {
	called := false
	e := newEnvWithSkillPush(t, func(repo, branch string) bool {
		called = true
		return true
	})

	rr := deliverBody(t, e.h, "push", "d-1", skillPushBody("acme/skills-repo", "refs/tags/v1"))
	if rr.Code != http.StatusOK || status(t, rr) != "ignored" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatalf("onSkillPush was called for a tag ref")
	}
}

// TestSkillPushNonPushEventNeverCallsCallback proves onSkillPush is only
// consulted for push events.
func TestSkillPushNonPushEventNeverCallsCallback(t *testing.T) {
	called := false
	e := newEnvWithSkillPush(t, func(repo, branch string) bool {
		called = true
		return true
	})

	deliverOK(t, e, "issues", "d-1", "issues_opened.json")
	if called {
		t.Fatalf("onSkillPush was called for a non-push event")
	}
}

// TestSkillPushDuplicateDeliveryStaysDuplicate documents the accepted
// trade-off: onSkillPush fires before RecordEvent's idempotency check, so a
// redelivered push re-triggers the callback. The response must still report
// "duplicate" on a redelivery.
func TestSkillPushDuplicateDeliveryStaysDuplicate(t *testing.T) {
	calls := 0
	e := newEnvWithSkillPush(t, func(repo, branch string) bool {
		calls++
		return true
	})

	body := skillPushBody("acme/skills-repo", "refs/heads/main")
	rr := deliverBody(t, e.h, "push", "d-1", body)
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("first delivery: code=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = deliverBody(t, e.h, "push", "d-1", body)
	if rr.Code != http.StatusOK || status(t, rr) != "duplicate" {
		t.Fatalf("second delivery: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if calls != 2 {
		t.Fatalf("onSkillPush calls = %d, want 2 (fires before RecordEvent's dedup)", calls)
	}
	if n := e.eventCount(t); n != 1 {
		t.Fatalf("event rows = %d, want 1", n)
	}
}
