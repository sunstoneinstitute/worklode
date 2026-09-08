package ui

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestProgressAcceptActionByViewer: WL-SPEC-66 §3.2's rule about who may
// press Accept. The button is enabled only for the document's owner, because
// that is the only actor store.AcceptDoc admits (025 §7); everyone else gets
// it disabled with the reason, never hidden (§3).
func TestProgressAcceptActionByViewer(t *testing.T) {
	t.Parallel()
	plan := model.ProgressPlan{Doc: 7, Ref: "WL-PLAN-9", State: "draft", Owner: "alice"}

	for _, tc := range []struct {
		name, viewer, reason string
	}{
		{name: "the owner", viewer: "alice"},
		{name: "another actor", viewer: "bob", reason: "WL-PLAN-9 is owned by alice"},
		{name: "nobody signed in", viewer: "", reason: "sign in to accept"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			acts := progressPlanActions(plan, tc.viewer)
			if len(acts) != 1 {
				t.Fatalf("draft plan has %d actions, want 1", len(acts))
			}
			a := acts[0]
			if a.Route != "accept" || a.Label != "Accept" {
				t.Fatalf("action = %+v; want the accept route", a)
			}
			if a.Reason != tc.reason {
				t.Fatalf("reason = %q; want %q", a.Reason, tc.reason)
			}
			// An enabled button carries what the script sends; a disabled one
			// is never sent, so what it carries does not matter.
			if tc.reason == "" {
				if a.Body != `{"doc":7}` {
					t.Errorf("body = %q; want the document id", a.Body)
				}
				if a.Confirm != "Accept WL-PLAN-9" {
					t.Errorf("confirm = %q; want the act named in full", a.Confirm)
				}
			}
		})
	}
}

// TestProgressAcceptActionNeedsADraft: an accepted plan and an accepted spec
// have nothing to accept, so neither carries the button at all.
func TestProgressAcceptActionNeedsADraft(t *testing.T) {
	t.Parallel()
	if acts := progressPlanActions(model.ProgressPlan{State: "not_started", Owner: "alice"}, "alice"); acts != nil {
		t.Errorf("an accepted plan carries %+v; want no Accept button", acts)
	}
	if acts := progressSpecActions(model.ProgressSpec{Status: "accepted", Owner: "alice"}, "alice"); acts != nil {
		t.Errorf("an accepted spec carries %+v; want no Accept button", acts)
	}
}

// TestProgressAcceptActionOnADraftSpec: §3.2 puts the button on a draft
// spec's row as well as on a draft plan's line, on the same owner rule.
func TestProgressAcceptActionOnADraftSpec(t *testing.T) {
	t.Parallel()
	spec := model.ProgressSpec{Doc: 3, Ref: "WL-SPEC-66", Status: "draft", Owner: "alice"}

	owner := progressSpecActions(spec, "alice")
	if len(owner) != 1 || owner[0].Reason != "" || owner[0].Body != `{"doc":3}` {
		t.Fatalf("the owner's action = %+v; want one enabled Accept", owner)
	}
	other := progressSpecActions(spec, "bob")
	if len(other) != 1 || !strings.Contains(other[0].Reason, "alice") {
		t.Fatalf("another actor's action = %+v; want it disabled naming the owner", other)
	}
}

// TestProgressAcceptActionUnowned: a document with no owner cannot be
// accepted by anyone (checkDocOwner refuses an empty owner), so the button
// says that rather than promising a write the store would refuse.
func TestProgressAcceptActionUnowned(t *testing.T) {
	t.Parallel()
	acts := progressPlanActions(model.ProgressPlan{Doc: 1, Ref: "WL-PLAN-1", State: "draft"}, "alice")
	if len(acts) != 1 || acts[0].Reason != "WL-PLAN-1 has no owner to accept it" {
		t.Fatalf("action = %+v; want it disabled for want of an owner", acts)
	}
}
