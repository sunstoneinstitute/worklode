package ui

import (
	"context"
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
	spec := progressSpecActions(model.ProgressSpec{Status: "accepted", Owner: "alice"}, "alice")
	if act := actionOn(spec, "accept"); act != nil {
		t.Errorf("an accepted spec carries %+v; want no Accept button", act)
	}
}

// actionOn is the one action on the given route, or nil. Every test below
// asks for the act it is about rather than counting the slot's contents: a
// spec row carries Rally on every row (§3.5) and gains Review as 059 lands.
func actionOn(acts []ProgressAction, route string) *ProgressAction {
	for i, a := range acts {
		if a.Route == route {
			return &acts[i]
		}
	}
	return nil
}

// TestProgressAcceptActionOnADraftSpec: §3.2 puts the button on a draft
// spec's row as well as on a draft plan's line, on the same owner rule.
func TestProgressAcceptActionOnADraftSpec(t *testing.T) {
	t.Parallel()
	spec := model.ProgressSpec{Doc: 3, Ref: "WL-SPEC-66", Status: "draft", Owner: "alice"}

	owner := actionOn(progressSpecActions(spec, "alice"), "accept")
	if owner == nil || owner.Reason != "" || owner.Body != `{"doc":3}` {
		t.Fatalf("the owner's action = %+v; want an enabled Accept", owner)
	}
	other := actionOn(progressSpecActions(spec, "bob"), "accept")
	if other == nil || !strings.Contains(other.Reason, "alice") {
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

// TestProgressPlanAction: §3.4's Plan button is offered when the spec has a
// section no plan covers and no planning task is open. Any signed-in viewer
// may press it; the task it mints is a prompt to plan, not the plan.
func TestProgressPlanAction(t *testing.T) {
	t.Parallel()
	spec := model.ProgressSpec{Doc: 3, Ref: "WL-SPEC-66", Status: "accepted",
		Sections: []model.ProgressSection{{Anchor: "sec-1", State: "unplanned"}}}

	act := actionOn(progressSpecActions(spec, "alice"), "plan")
	if act == nil || act.Reason != "" {
		t.Fatalf("action = %+v; want an enabled Plan button", act)
	}
	if act.Body != `{"doc":3}` || act.Confirm != "Mint a planning task for WL-SPEC-66" {
		t.Errorf("action = %+v; want the doc body and the act named in full", act)
	}
	if out := actionOn(progressSpecActions(spec, ""), "plan"); out == nil || out.Reason == "" {
		t.Errorf("signed out action = %+v; want the button disabled with a reason", out)
	}

	// Nothing unplanned: nothing to mint.
	covered := spec
	covered.Sections = []model.ProgressSection{{Anchor: "sec-1", State: "built"}}
	if out := actionOn(progressSpecActions(covered, "alice"), "plan"); out != nil {
		t.Errorf("a fully covered spec carries %+v; want no Plan button", out)
	}
}

// TestProgressRowPlanningTaskLink: a spec whose planning task is already open
// shows it as a link instead of the button — the route would only hand back
// that same task (§3.4).
func TestProgressRowPlanningTaskLink(t *testing.T) {
	t.Parallel()
	spec := model.ProgressSpec{Doc: 3, Ref: "WL-SPEC-66", Title: "Progress", Status: "accepted",
		Sections: []model.ProgressSection{{Anchor: "sec-1", Heading: "Scope", State: "unplanned"}}}

	open := spec
	open.PlanningTask = "WL-42"
	html := renderProgressRow(t, open)
	if !strings.Contains(html, `href="/tasks/WL-42"`) || !strings.Contains(html, "Planning: WL-42") {
		t.Errorf("row = %s; want a link to the open planning task", html)
	}
	if strings.Contains(html, `data-route="plan"`) {
		t.Errorf("row = %s; want no Plan button beside the link", html)
	}

	if html := renderProgressRow(t, spec); !strings.Contains(html, `data-route="plan"`) {
		t.Errorf("row = %s; want the Plan button when no planning task is open", html)
	} else if strings.Contains(html, "Planning:") {
		t.Errorf("row = %s; want no planning link when none is open", html)
	}
}

// renderProgressRow renders one spec row as a signed-in viewer sees it.
func renderProgressRow(t *testing.T, s model.ProgressSpec) string {
	t.Helper()
	var b strings.Builder
	if err := progressRow(s, "alice").Render(context.Background(), &b); err != nil {
		t.Fatalf("render row: %v", err)
	}
	return b.String()
}

// TestProgressRallyActionOnEveryRow: §3.5 puts a Rally button on every spec
// row, whatever group the spec is in — a spec with nothing outstanding is a
// no-op the route answers, not an act to hide.
func TestProgressRallyActionOnEveryRow(t *testing.T) {
	t.Parallel()
	spec := model.ProgressSpec{Doc: 3, Ref: "WL-SPEC-66", Status: "accepted",
		Sections: []model.ProgressSection{{Anchor: "sec-1", State: "built"}}}

	act := actionOn(progressSpecActions(spec, "alice"), "rally/add")
	if act == nil || act.Reason != "" {
		t.Fatalf("action = %+v; want an enabled Rally button", act)
	}
	if act.Body != `{"doc":3}` || act.Confirm != "Add WL-SPEC-66 to the rally" {
		t.Errorf("action = %+v; want the doc body and the act named in full", act)
	}
	if out := actionOn(progressSpecActions(spec, ""), "rally/add"); out == nil || out.Reason == "" {
		t.Errorf("signed out action = %+v; want it disabled with a reason", out)
	}
	if html := renderProgressRow(t, spec); !strings.Contains(html, `data-route="rally/add"`) {
		t.Errorf("row = %s; want the Rally button in the action slot", html)
	}
}

// TestProgressFooterHeightIsFixed: §2.1's reserved slot. The footer keeps the
// same height class with and without a draft rally, so confirming or
// discarding one moves nothing on the page above it (§5.4).
func TestProgressFooterHeightIsFixed(t *testing.T) {
	t.Parallel()
	empty := renderProgressFooter(t, nil)
	if !strings.Contains(empty, `class="prog-footer h-14"`) {
		t.Errorf("empty footer = %s; want the reserved slot at its fixed height", empty)
	}
	if strings.Contains(empty, "data-route") {
		t.Errorf("empty footer = %s; want no act with no draft rally", empty)
	}

	full := renderProgressFooter(t, &model.RallyBand{ID: "WL-900", Members: 4, Specs: 2})
	if !strings.Contains(full, `class="prog-footer h-14"`) {
		t.Errorf("footer = %s; want the same fixed height as the empty slot", full)
	}
	for _, want := range []string{
		"Rally: 4 tasks from 2 specs",
		`data-route="rally/confirm"`,
		`data-route="rally/discard"`,
		"Confirm Rally",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("footer = %s; want it to carry %q", full, want)
		}
	}
}

// TestProgressFooterNeedsASession: both footer acts are writes, so a viewer
// with no session gets them disabled with the reason rather than missing (§3).
func TestProgressFooterNeedsASession(t *testing.T) {
	t.Parallel()
	html := renderProgressFooter(t, &model.RallyBand{ID: "WL-900", Members: 1, Specs: 1})
	if strings.Contains(html, "disabled") {
		t.Errorf("signed-in footer = %s; want both acts live", html)
	}
	for _, a := range progressFooterActions("") {
		if a.Reason == "" {
			t.Errorf("signed out action %+v; want a reason", a)
		}
	}
}

// renderProgressFooter renders §3.5's footer as a signed-in viewer sees it.
func renderProgressFooter(t *testing.T, d *model.RallyBand) string {
	t.Helper()
	var b strings.Builder
	if err := progressFooter(d, "alice").Render(context.Background(), &b); err != nil {
		t.Fatalf("render footer: %v", err)
	}
	return b.String()
}
