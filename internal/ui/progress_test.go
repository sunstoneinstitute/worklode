package ui

import (
	"context"
	"regexp"
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
			acts := progressPlanActions(plan, tc.viewer, false)
			a := actionOn(acts, "accept")
			if a == nil {
				t.Fatalf("draft plan actions = %+v; want an accept action", acts)
			}
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
	plan := progressPlanActions(model.ProgressPlan{State: "not_started", Owner: "alice"}, "alice", false)
	if act := actionOn(plan, "accept"); act != nil {
		t.Errorf("an accepted plan carries %+v; want no Accept button", act)
	}
	spec := progressSpecActions(model.ProgressSpec{Status: "accepted", Owner: "alice"}, "alice", false)
	if act := actionOn(spec, "accept"); act != nil {
		t.Errorf("an accepted spec carries %+v; want no Accept button", act)
	}
}

// actionOn is the one action on the given route, or nil. Every test below
// asks for the act it is about rather than counting the slot's contents: a
// spec row and a plan line each carry Rally and Review on every row (§3.5,
// §3.3) alongside whichever of Accept and Plan applies.
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

	owner := actionOn(progressSpecActions(spec, "alice", false), "accept")
	if owner == nil || owner.Reason != "" || owner.Body != `{"doc":3}` {
		t.Fatalf("the owner's action = %+v; want an enabled Accept", owner)
	}
	other := actionOn(progressSpecActions(spec, "bob", false), "accept")
	if other == nil || !strings.Contains(other.Reason, "alice") {
		t.Fatalf("another actor's action = %+v; want it disabled naming the owner", other)
	}
}

// TestProgressAcceptActionUnowned: a document with no owner cannot be
// accepted by anyone (checkDocOwner refuses an empty owner), so the button
// says that rather than promising a write the store would refuse.
func TestProgressAcceptActionUnowned(t *testing.T) {
	t.Parallel()
	acts := progressPlanActions(model.ProgressPlan{Doc: 1, Ref: "WL-PLAN-1", State: "draft"}, "alice", false)
	act := actionOn(acts, "accept")
	if act == nil || act.Reason != "WL-PLAN-1 has no owner to accept it" {
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

	act := actionOn(progressSpecActions(spec, "alice", false), "plan")
	if act == nil || act.Reason != "" {
		t.Fatalf("action = %+v; want an enabled Plan button", act)
	}
	if act.Body != `{"doc":3}` || act.Confirm != "Mint a planning task for WL-SPEC-66" {
		t.Errorf("action = %+v; want the doc body and the act named in full", act)
	}
	if out := actionOn(progressSpecActions(spec, "", false), "plan"); out == nil || out.Reason == "" {
		t.Errorf("signed out action = %+v; want the button disabled with a reason", out)
	}

	// Nothing unplanned: nothing to mint.
	covered := spec
	covered.Sections = []model.ProgressSection{{Anchor: "sec-1", State: "built"}}
	if out := actionOn(progressSpecActions(covered, "alice", false), "plan"); out != nil {
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

// renderProgressRow renders one spec row as a signed-in viewer sees it, with
// the review surface disabled — today's reality (§3.3) — and a GitHub App
// configured, which is what §3.6's merge button needs.
func renderProgressRow(t *testing.T, s model.ProgressSpec) string {
	t.Helper()
	return renderProgressRowWithApp(t, s, true)
}

func renderProgressRowWithApp(t *testing.T, s model.ProgressSpec, mergeEnabled bool) string {
	t.Helper()
	var b strings.Builder
	if err := progressRow(s, "alice", false, mergeEnabled).Render(context.Background(), &b); err != nil {
		t.Fatalf("render row: %v", err)
	}
	return b.String()
}

// specWithMerge is a spec whose one plan minted one task, carrying the open
// PR §3.6 acts on.
func specWithMerge(m *model.ProgressMerge) model.ProgressSpec {
	return model.ProgressSpec{
		Doc: 3, Ref: "WL-SPEC-66", Status: "accepted",
		Sections: []model.ProgressSection{{Anchor: "sec-1", State: "in_progress"}},
		Plans: []model.ProgressPlan{{
			Doc: 4, Ref: "WL-PLAN-139", State: "in_progress", Open: 1,
			Tasks: []model.ProgressTask{{ID: "WL-752", Title: "Queue for merge",
				State: "in_review", Class: "active", Position: "PR #7 open", Merge: m}},
		}},
	}
}

// TestProgressMergeButton: criterion 18. A task whose PR sits on a
// queue-protected branch offers "Queue for merge"; the same PR on a branch
// without the rule offers "Merge", disabled while the reason the backbone
// already knows applies.
func TestProgressMergeButton(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                   string
		merge                  model.ProgressMerge
		label, confirm, reason string
	}{
		{
			name:    "a queue-protected branch queues",
			merge:   model.ProgressMerge{Repo: "acme/app", Number: 7, Queue: true},
			label:   "Queue for merge",
			confirm: "Queue PR #7 for merge",
		},
		{
			name:    "a branch without the rule merges",
			merge:   model.ProgressMerge{Repo: "acme/app", Number: 7},
			label:   "Merge",
			confirm: "Merge PR #7",
		},
		{
			name:    "checks that have not passed disable the merge",
			merge:   model.ProgressMerge{Repo: "acme/app", Number: 7, Reason: "checks have not passed"},
			label:   "Merge",
			confirm: "Merge PR #7",
			reason:  "checks have not passed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := tc.merge
			html := renderProgressRow(t, specWithMerge(&m))
			if !strings.Contains(html, `data-route="merge"`) {
				t.Fatalf("row = %s; want the merge button", html)
			}
			if !strings.Contains(html, ">"+tc.label+"<") {
				t.Errorf("row = %s; want the label %q", html, tc.label)
			}
			if tc.reason == "" {
				if !strings.Contains(html, `data-confirm="`+tc.confirm+`"`) {
					t.Errorf("row = %s; want the confirmation %q", html, tc.confirm)
				}
				if !strings.Contains(html, `data-body="{&#34;task&#34;:&#34;WL-752&#34;`) {
					t.Errorf("row = %s; want the body to name the task and its PR", html)
				}
				if strings.Contains(html, `data-route="merge" data-reason`) {
					t.Errorf("row = %s; want the button enabled", html)
				}
				return
			}
			if !strings.Contains(html, `data-reason="`+tc.reason+`"`) {
				t.Errorf("row = %s; want it disabled with %q", html, tc.reason)
			}
		})
	}
}

// A task with no open PR has nothing to merge, so the row carries no button
// at all — there is no PR for a disabled one to name.
func TestProgressMergeButtonNeedsAPR(t *testing.T) {
	t.Parallel()
	if html := renderProgressRow(t, specWithMerge(nil)); strings.Contains(html, `data-route="merge"`) {
		t.Errorf("row = %s; want no merge button without an open PR", html)
	}
}

// Without a GitHub App the server cannot act on a PR at all, so the button
// renders disabled with that reason rather than posting a route that would
// answer 503.
func TestProgressMergeButtonWithoutApp(t *testing.T) {
	t.Parallel()
	m := model.ProgressMerge{Repo: "acme/app", Number: 7, Queue: true}
	html := renderProgressRowWithApp(t, specWithMerge(&m), false)
	if !strings.Contains(html, `data-reason="no GitHub App is configured"`) {
		t.Errorf("row = %s; want the button disabled with the App reason", html)
	}
}

// TestProgressRallyActionOnEveryRow: §3.5 puts a Rally button on every spec
// row, whatever group the spec is in — a spec with nothing outstanding is a
// no-op the route answers, not an act to hide.
func TestProgressRallyActionOnEveryRow(t *testing.T) {
	t.Parallel()
	spec := model.ProgressSpec{Doc: 3, Ref: "WL-SPEC-66", Status: "accepted",
		Sections: []model.ProgressSection{{Anchor: "sec-1", State: "built"}}}

	act := actionOn(progressSpecActions(spec, "alice", false), "rally/add")
	if act == nil || act.Reason != "" {
		t.Fatalf("action = %+v; want an enabled Rally button", act)
	}
	if act.Body != `{"doc":3}` || act.Confirm != "Add WL-SPEC-66 to the rally" {
		t.Errorf("action = %+v; want the doc body and the act named in full", act)
	}
	if out := actionOn(progressSpecActions(spec, "", false), "rally/add"); out == nil || out.Reason == "" {
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

// TestTaskPulseAnimationKeepsPageStill holds §5.3 and §5.4 together: the
// activity animations exist (in both a full-motion and a
// prefers-reduced-motion form, so a reader who has turned off motion still
// gets an instant colour change rather than no feedback), and neither one
// touches a property that would move layout around it.
func TestTaskPulseAnimationKeepsPageStill(t *testing.T) {
	flat := strings.Join(strings.Fields(builtCSS(t)), "")

	if !strings.Contains(flat, "@media(prefers-reduced-motion:no-preference){.task.pulse{") {
		t.Error("app.css: no prefers-reduced-motion:no-preference block for .task.pulse (§5.3)")
	}
	if !strings.Contains(flat, "@media(prefers-reduced-motion:reduce){.task.pulse{") {
		t.Error("app.css: no prefers-reduced-motion:reduce fallback for .task.pulse (§5.3)")
	}
	if !strings.Contains(flat, ".prog-row.touched{") {
		t.Error("app.css: no .prog-row.touched rule for the row's activity highlight (§5.3)")
	}

	rules := regexp.MustCompile(`\.task\.pulse\{([^}]*)\}`).FindAllStringSubmatch(flat, -1)
	if len(rules) == 0 {
		t.Fatal("app.css: no .task.pulse rule")
	}
	for _, r := range rules {
		for _, layout := range []string{"width:", "height:", "margin:", "padding:"} {
			if strings.Contains(r[1], layout) {
				t.Errorf(".task.pulse rule %q sets %q; an activity animation must never change layout size (§5.4)", r[1], layout)
			}
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

// TestProgressSliceClass pins the widths the section bar can no longer put in
// a style attribute (WL-769): a slice's share of the bar, rounded to the whole
// percent app.tailwind.css has a rule for, and never rounded away to nothing.
func TestProgressSliceClass(t *testing.T) {
	bar := []model.ProgressSlice{
		{State: "built", Count: 3},
		{State: "in_progress", Count: 1},
		{State: "unplanned", Count: 396},
	}
	want := []string{"seg cell-built seg-1", "seg cell-in_progress seg-1", "seg cell-unplanned seg-99"}
	for i := range bar {
		if got := progressSliceClass(bar, i); got != want[i] {
			t.Errorf("slice %d = %q, want %q", i, got, want[i])
		}
	}
	one := []model.ProgressSlice{{State: "built", Count: 7}}
	if got := progressSliceClass(one, 0); got != "seg cell-built seg-100" {
		t.Errorf("lone slice = %q, want the whole bar", got)
	}
}
