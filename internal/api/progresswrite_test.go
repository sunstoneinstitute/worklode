// progresswrite_test.go exercises beginJSONPost, the Progress page's write
// gate (WL-SPEC-66 §4.2), rule by rule. It is a white-box test because the
// gate is a helper, not a route: the routes that call it arrive with WL-734,
// and a probe route cannot be registered from the black-box harness —
// routeGuards refuses to boot on a pattern it does not name.
package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/watcher"
)

const probeActor = "github:1"

// probeBody is a stand-in for a real write route's body.
type probeBody struct {
	Doc string `json:"doc"`
}

func newProbeServer(t *testing.T) *server {
	t.Helper()
	st := newStoreT(t)
	if err := st.CreateProject(t.Context(), "p", "Proj", "PP"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	s := &server{st: st, log: slog.Default()}
	s.initMetrics(prometheus.NewRegistry())
	return s
}

// goodPost builds the request a conforming page script sends.
func goodPost(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/projects/p/progress/_probe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "lode-cockpit")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return req
}

// probe runs one request through the gate on the throwaway "accept" route.
func probe(t *testing.T, s *server, req *http.Request, id string) (*httptest.ResponseRecorder, Subject, bool) {
	t.Helper()
	req.SetPathValue("id", id)
	req = withSubject(req, Subject{ActorID: probeActor})
	rr := httptest.NewRecorder()
	var dst probeBody
	subj, _, ok := s.beginJSONPost(rr, req, "accept", &dst)
	return rr, subj, ok
}

func TestBeginJSONPostRefusals(t *testing.T) {
	t.Parallel()
	s := newProbeServer(t)

	for _, tc := range []struct {
		name string
		req  func() *http.Request
		want int
		msg  string
	}{
		{
			name: "GET is not a write",
			req: func() *http.Request {
				req := goodPost("{}")
				req.Method = http.MethodGet
				return req
			},
			want: http.StatusMethodNotAllowed,
		},
		{
			name: "cross-site fetch",
			req: func() *http.Request {
				req := goodPost("{}")
				req.Header.Set("Sec-Fetch-Site", "cross-site")
				return req
			},
			want: http.StatusForbidden,
			msg:  "cross-site request refused",
		},
		{
			name: "same-site fetch",
			req: func() *http.Request {
				req := goodPost("{}")
				req.Header.Set("Sec-Fetch-Site", "same-site")
				return req
			},
			want: http.StatusForbidden,
			msg:  "cross-site request refused",
		},
		{
			// The one place this gate is stricter than sameOriginForm: a
			// script-issued fetch is never a typed navigation.
			name: "Sec-Fetch-Site none",
			req: func() *http.Request {
				req := goodPost("{}")
				req.Header.Set("Sec-Fetch-Site", "none")
				return req
			},
			want: http.StatusForbidden,
			msg:  "cross-site request refused",
		},
		{
			name: "foreign Origin with no Sec-Fetch-Site",
			req: func() *http.Request {
				req := goodPost("{}")
				req.Header.Del("Sec-Fetch-Site")
				req.Header.Set("Origin", "https://evil.example")
				return req
			},
			want: http.StatusForbidden,
			msg:  "cross-site request refused",
		},
		{
			name: "no page header",
			req: func() *http.Request {
				req := goodPost("{}")
				req.Header.Del("X-Requested-With")
				return req
			},
			want: http.StatusForbidden,
			msg:  "missing page header",
		},
		{
			name: "wrong page header",
			req: func() *http.Request {
				req := goodPost("{}")
				req.Header.Set("X-Requested-With", "XMLHttpRequest")
				return req
			},
			want: http.StatusForbidden,
			msg:  "missing page header",
		},
		{
			name: "form-encoded body",
			req: func() *http.Request {
				req := goodPost("doc=WL-PLAN-1")
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return req
			},
			want: http.StatusUnsupportedMediaType,
		},
		{
			name: "body over the cap",
			req:  func() *http.Request { return goodPost(`{"doc":"` + strings.Repeat("x", maxJSONPost) + `"}`) },
			want: http.StatusUnprocessableEntity,
		},
		{
			name: "undecodable body",
			req:  func() *http.Request { return goodPost(`{"doc":`) },
			want: http.StatusUnprocessableEntity,
		},
		{
			name: "unknown field",
			req:  func() *http.Request { return goodPost(`{"nope":1}`) },
			want: http.StatusUnprocessableEntity,
		},
		{
			name: "a body naming its own actor",
			req:  func() *http.Request { return goodPost(`{"doc":"WL-PLAN-1","actor":"github:99"}`) },
			want: http.StatusUnprocessableEntity,
			msg:  "the actor is the session",
		},
		{
			// The key at all, whatever it carries: an empty or null actor
			// is still a body trying to say who is acting.
			name: "a body naming an empty actor",
			req:  func() *http.Request { return goodPost(`{"doc":"WL-PLAN-1","actor":""}`) },
			want: http.StatusUnprocessableEntity,
			msg:  "the actor is the session",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rr, _, ok := probe(t, s, tc.req(), "p")
			if ok {
				t.Fatalf("gate admitted the request; want a refusal")
			}
			if rr.Code != tc.want {
				t.Fatalf("status = %d body=%s; want %d", rr.Code, rr.Body.String(), tc.want)
			}
			if tc.msg != "" && !strings.Contains(rr.Body.String(), tc.msg) {
				t.Fatalf("body = %s; want it to name %q", rr.Body.String(), tc.msg)
			}
			// §4.2 rule 4: the reply is never HTML, so a navigation can
			// never render it as a page.
			if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q; want application/json", ct)
			}
			// §4.4: every reply, refusals included, carries the policy.
			if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
				t.Fatalf("Content-Security-Policy = %q; want frame-ancestors 'none'", csp)
			}
		})
	}
}

func TestBeginJSONPostMethodNotAllowedNamesPOST(t *testing.T) {
	t.Parallel()
	s := newProbeServer(t)
	req := goodPost("{}")
	req.Method = http.MethodGet
	rr, _, _ := probe(t, s, req, "p")
	if got := rr.Header().Get("Allow"); got != "POST" {
		t.Fatalf("Allow = %q; want POST", got)
	}
}

func TestBeginJSONPostUnknownProject(t *testing.T) {
	t.Parallel()
	s := newProbeServer(t)
	rr, _, ok := probe(t, s, goodPost(`{"doc":"WL-PLAN-1"}`), "nosuch")
	if ok {
		t.Fatalf("gate admitted an unknown project")
	}
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s; want 404", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q; want application/json", ct)
	}
}

func TestBeginJSONPostAdmitsThePageScript(t *testing.T) {
	t.Parallel()
	s := newProbeServer(t)
	req := goodPost(`{"doc":"WL-PLAN-1"}`)
	req.SetPathValue("id", "p")
	req = withSubject(req, Subject{ActorID: probeActor})
	rr := httptest.NewRecorder()
	var dst probeBody
	subj, project, ok := s.beginJSONPost(rr, req, "accept", &dst)
	if !ok {
		t.Fatalf("gate refused a conforming request: %d %s", rr.Code, rr.Body.String())
	}
	if subj.ActorID != probeActor {
		t.Fatalf("actor = %q; want %q (the session's, never the body's)", subj.ActorID, probeActor)
	}
	if project.ID != "p" {
		t.Fatalf("project = %q; want p", project.ID)
	}
	if dst.Doc != "WL-PLAN-1" {
		t.Fatalf("decoded doc = %q; want WL-PLAN-1", dst.Doc)
	}
}

func TestBeginJSONPostCountsRefusals(t *testing.T) {
	t.Parallel()
	s := newProbeServer(t)
	req := goodPost("{}")
	req.Header.Del("X-Requested-With")
	if _, _, ok := probe(t, s, req, "p"); ok {
		t.Fatalf("gate admitted a request with no page header")
	}
	if got := testutil.ToFloat64(s.progressWrites.WithLabelValues("accept", "refused")); got != 1 {
		t.Fatalf("progressWrites{accept,refused} = %v, want 1", got)
	}
	// The gate counts only what it answers itself; an admitted request is
	// the caller's to count.
	if _, _, ok := probe(t, s, goodPost(`{"doc":"x"}`), "p"); !ok {
		t.Fatalf("gate refused a conforming request")
	}
	if got := testutil.ToFloat64(s.progressWrites.WithLabelValues("accept", "refused")); got != 1 {
		t.Fatalf("progressWrites{accept,refused} = %v after an admitted request, want 1", got)
	}
}

// --- the accept route (WL-SPEC-66 §3.2) --------------------------------------

// probeAcceptPlan is a draft plan with one declaration, so accepting it mints
// exactly one task (025 §9.2) and the reply's count is checkable.
const probeAcceptPlan = `---
status: draft
---

# Probe plan

## Tasks

### Task 1 — Only task

` + "```yaml" + `
kind: feature
priority: medium
` + "```" + `

Do the thing.
`

// seedProbeDoc writes one document straight through the store, the way the
// JSON API's create handler does. The black-box harness cannot reach this
// route with a named actor — a web route takes its actor from a session
// cookie, and these tests configure no login provider — so the whole accept
// route is exercised white-box, like the gate above it.
func seedProbeDoc(t *testing.T, s *server, in store.DocInput) *model.Doc {
	t.Helper()
	var doc *model.Doc
	err := s.recordEvent(t.Context(), "cli", "doc.created", map[string]any{"slug": in.Slug},
		func(tx *sql.Tx, eventID int64) error {
			d, err := store.CreateDoc(tx, s.st.Now(), in, eventID)
			if err != nil {
				return err
			}
			doc = d
			return nil
		})
	if err != nil {
		t.Fatalf("seed doc %s: %v", in.Slug, err)
	}
	return doc
}

// newAcceptServer is newProbeServer plus two actors: the owner of the seeded
// plan, and somebody else.
func newAcceptServer(t *testing.T) (*server, *model.Doc) {
	t.Helper()
	s := newProbeServer(t)
	for _, id := range []string{probeActor, otherActor} {
		if err := s.st.CreateActor(t.Context(), id, "human", id, false); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	plan := seedProbeDoc(t, s, store.DocInput{
		Project: "p", Kind: "plan", Slug: "probe-plan",
		Body: probeAcceptPlan, Owner: probeActor, CreatedBy: probeActor,
	})
	return s, plan
}

const otherActor = "github:2"

// acceptPost runs one POST /projects/p/progress/accept as actor.
func acceptPost(t *testing.T, s *server, actor string, doc int64) *httptest.ResponseRecorder {
	t.Helper()
	req := goodPost(`{"doc":` + strconv.FormatInt(doc, 10) + `}`)
	req.SetPathValue("id", "p")
	req = withSubject(req, Subject{ActorID: actor})
	rr := httptest.NewRecorder()
	s.progressAccept(rr, req)
	return rr
}

// TestProgressAcceptMintsTasks: the owner accepts their draft plan and gets
// back what was accepted and how many tasks it minted (§3.2, 025 §9.2).
func TestProgressAcceptMintsTasks(t *testing.T) {
	t.Parallel()
	s, plan := newAcceptServer(t)

	rr := acceptPost(t, s, probeActor, plan.ID)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	var got model.ProgressAcceptResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	want := model.ProgressAcceptResponse{Doc: plan.ID, Status: "accepted", Minted: 1}
	if got != want {
		t.Fatalf("reply = %+v; want %+v", got, want)
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("accept", "ok")); n != 1 {
		t.Fatalf("progressWrites{accept,ok} = %v, want 1", n)
	}
}

// TestProgressAcceptRefusesNonOwner: the owner gate is the store's (025 §7),
// so it holds here whatever the page rendered for this viewer.
func TestProgressAcceptRefusesNonOwner(t *testing.T) {
	t.Parallel()
	s, plan := newAcceptServer(t)

	rr := acceptPost(t, s, otherActor, plan.ID)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s; want 403", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), probeActor) {
		t.Errorf("body = %s; want it to name the owner", rr.Body.String())
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("accept", "refused")); n != 1 {
		t.Fatalf("progressWrites{accept,refused} = %v, want 1", n)
	}
	// Nothing was accepted, so the button is still live for the owner.
	doc, err := s.st.GetDoc(t.Context(), plan.ID)
	if err != nil {
		t.Fatalf("reload doc: %v", err)
	}
	if doc.Status != "draft" {
		t.Fatalf("doc status = %q after a refused accept; want draft", doc.Status)
	}
}

// TestProgressAcceptTwiceIsAConflict: §4.2 rule 7. A plan is re-acceptable
// while accepted through the CLI (025 §9.2), but this button is offered on a
// draft document, so a second click on a stale page is refused rather than
// answered as a success that minted nothing.
func TestProgressAcceptTwiceIsAConflict(t *testing.T) {
	t.Parallel()
	s, plan := newAcceptServer(t)

	if rr := acceptPost(t, s, probeActor, plan.ID); rr.Code != http.StatusOK {
		t.Fatalf("first accept = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	rr := acceptPost(t, s, probeActor, plan.ID)
	if rr.Code != http.StatusConflict {
		t.Fatalf("second accept = %d body=%s; want 409", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not draft") {
		t.Errorf("body = %s; want the store's reason", rr.Body.String())
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("accept", "conflict")); n != 1 {
		t.Fatalf("progressWrites{accept,conflict} = %v, want 1", n)
	}
}

// TestProgressAcceptRefusesAnotherProjectsDoc: the route is project-scoped,
// so a document that belongs to another project is not found on this one.
func TestProgressAcceptRefusesAnotherProjectsDoc(t *testing.T) {
	t.Parallel()
	s, _ := newAcceptServer(t)
	if err := s.st.CreateProject(t.Context(), "q", "Other", "QQ"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	elsewhere := seedProbeDoc(t, s, store.DocInput{
		Project: "q", Kind: "plan", Slug: "elsewhere-plan",
		Body: probeAcceptPlan, Owner: probeActor, CreatedBy: probeActor,
	})

	rr := acceptPost(t, s, probeActor, elsewhere.ID)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s; want 404", rr.Code, rr.Body.String())
	}
}

// probePlanSpec is a spec with one anchored section. Nothing covers it, so
// the section is unplanned and §3.4's Plan button is offered on the row.
const probePlanSpec = `---
status: accepted
---

# Probe spec

## 1. Scope {#sec-1}

Scope body.
`

// probeCoveringPlan covers that one section, which leaves the spec with no
// unplanned section at all.
const probeCoveringPlan = `---
status: draft
covers:
  - spec: probe-spec.md#sec-1
    coverage: full
---

# Covering plan

Covers the whole spec.
`

// newPlanServer is newProbeServer plus the actors and a spec whose only
// section no plan covers.
func newPlanServer(t *testing.T) (*server, *model.Doc) {
	t.Helper()
	s := newProbeServer(t)
	for _, id := range []string{probeActor, otherActor} {
		if err := s.st.CreateActor(t.Context(), id, "human", id, false); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	spec := seedProbeDoc(t, s, store.DocInput{
		Project: "p", Kind: "spec", Slug: "probe-spec", Number: 66,
		Body: probePlanSpec, Owner: probeActor, CreatedBy: probeActor,
	})
	return s, spec
}

// planPost runs one POST /projects/p/progress/plan as actor.
func planPost(t *testing.T, s *server, actor string, doc int64) *httptest.ResponseRecorder {
	t.Helper()
	req := goodPost(`{"doc":` + strconv.FormatInt(doc, 10) + `}`)
	req.SetPathValue("id", "p")
	req = withSubject(req, Subject{ActorID: actor})
	rr := httptest.NewRecorder()
	s.progressPlan(rr, req)
	return rr
}

func decodePlanReply(t *testing.T, rr *httptest.ResponseRecorder) model.ProgressPlanResponse {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	var got model.ProgressPlanResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	return got
}

// TestProgressPlanMintsOnce: the first request mints the planning task, the
// second returns the same one with existing true and mints nothing (§3.4,
// §4.2 rule 7).
func TestProgressPlanMintsOnce(t *testing.T) {
	t.Parallel()
	s, spec := newPlanServer(t)

	first := decodePlanReply(t, planPost(t, s, probeActor, spec.ID))
	if first.Task == "" || first.Existing {
		t.Fatalf("first reply = %+v; want a minted task and existing false", first)
	}
	second := decodePlanReply(t, planPost(t, s, probeActor, spec.ID))
	if second.Task != first.Task || !second.Existing {
		t.Fatalf("second reply = %+v; want %s with existing true", second, first.Task)
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("plan", "ok")); n != 2 {
		t.Fatalf("progressWrites{plan,ok} = %v, want 2", n)
	}

	task, err := s.st.GetTask(t.Context(), first.Task)
	if err != nil {
		t.Fatalf("get minted task: %v", err)
	}
	if task.Kind != "design" {
		t.Errorf("task kind = %q; want design", task.Kind)
	}
	if task.CreatedBy != probeActor {
		t.Errorf("task created_by = %q; want the session actor %s", task.CreatedBy, probeActor)
	}
	// §3.4: a task minted here and one minted on acceptance are the same
	// thing, so the title is the doc-lifecycle subscriber's own.
	subscriber := watcher.Evaluate(watcher.Input{
		EventType: eventbus.TypeDocumentAccepted,
		DocKind:   "spec", DocTitle: spec.Title,
	})
	if len(subscriber) != 1 || task.Title != subscriber[0].Title {
		t.Fatalf("minted title = %q; want the subscriber's %+v", task.Title, subscriber)
	}
	if task.Title != watcher.PlanningTitle(spec.Title) {
		t.Errorf("minted title = %q; want %q", task.Title, watcher.PlanningTitle(spec.Title))
	}
}

// TestProgressPlanRefusesFullyCoveredSpec: the button is offered on an
// unplanned section, so a spec that has none is a 409 rather than a mint
// nobody asked for.
func TestProgressPlanRefusesFullyCoveredSpec(t *testing.T) {
	t.Parallel()
	s, spec := newPlanServer(t)
	seedProbeDoc(t, s, store.DocInput{
		Project: "p", Kind: "plan", Slug: "covering-plan",
		Body: probeCoveringPlan, Owner: probeActor, CreatedBy: probeActor,
	})

	rr := planPost(t, s, probeActor, spec.ID)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s; want 409", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unplanned") {
		t.Errorf("body = %s; want it to name the reason", rr.Body.String())
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("plan", "conflict")); n != 1 {
		t.Fatalf("progressWrites{plan,conflict} = %v, want 1", n)
	}
}

// TestProgressPlanRefusesNonSpec: the planning task is about a spec, so a
// plan document is not found on this route.
func TestProgressPlanRefusesNonSpec(t *testing.T) {
	t.Parallel()
	s, _ := newPlanServer(t)
	plan := seedProbeDoc(t, s, store.DocInput{
		Project: "p", Kind: "plan", Slug: "not-a-spec",
		Body: probeAcceptPlan, Owner: probeActor, CreatedBy: probeActor,
	})

	if rr := planPost(t, s, probeActor, plan.ID); rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s; want 404", rr.Code, rr.Body.String())
	}
}

// --- the rally act (WL-SPEC-66 §3.5) -----------------------------------------

// probeRallySpec is §8 criterion 17's spec: one section an accepted plan
// covers, one a draft plan covers, and one nothing covers at all.
const probeRallySpec = `---
status: accepted
---

# Rally spec

## 1. Executed {#sec-1}

Covered by the accepted plan.

## 2. Proposed {#sec-2}

Covered by the draft plan.

## 3. Unplanned {#sec-3}

Covered by nothing.
`

// probeRallyPlanA covers the first section and declares the two tasks that
// become the rally's execute members once it is accepted.
const probeRallyPlanA = `---
status: draft
covers:
  - spec: rally-spec.md#sec-1
    coverage: full
---

# Rally plan A

## Tasks

### Task 1 — First

` + "```yaml" + `
kind: feature
priority: medium
` + "```" + `

Do the first thing.

### Task 2 — Second

` + "```yaml" + `
kind: feature
priority: medium
` + "```" + `

Do the second thing.
`

// probeRallyPlanB stays draft, so the rally owes a prompt to accept it.
const probeRallyPlanB = `---
status: draft
covers:
  - spec: rally-spec.md#sec-2
    coverage: full
---

# Rally plan B

Nothing to mint; this plan is the question.
`

// newRallyServer seeds criterion 17's fixture: the spec, an accepted plan
// with two open tasks, and a draft plan. probeActor owns every document and
// is on the project's crew, so the accept prompt has somewhere to land.
func newRallyServer(t *testing.T) (*server, *model.Doc, *model.Doc) {
	t.Helper()
	s := newProbeServer(t)
	for _, id := range []string{probeActor, otherActor} {
		if err := s.st.CreateActor(t.Context(), id, "human", id, false); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	if err := s.recordEvent(t.Context(), "cli", "project.participant_added", map[string]string{"actor": probeActor},
		func(tx *sql.Tx, eventID int64) error {
			return store.AddParticipant(tx, s.st.Now(), "p", probeActor, "engineer", false, false, probeActor, eventID)
		}); err != nil {
		t.Fatalf("add crew member: %v", err)
	}
	spec := seedProbeDoc(t, s, store.DocInput{
		Project: "p", Kind: "spec", Slug: "rally-spec", Number: 66,
		Body: probeRallySpec, Owner: probeActor, CreatedBy: probeActor,
	})
	planA := seedProbeDoc(t, s, store.DocInput{
		Project: "p", Kind: "plan", Slug: "rally-plan-a",
		Body: probeRallyPlanA, Owner: probeActor, CreatedBy: probeActor,
	})
	planB := seedProbeDoc(t, s, store.DocInput{
		Project: "p", Kind: "plan", Slug: "rally-plan-b",
		Body: probeRallyPlanB, Owner: probeActor, CreatedBy: probeActor,
	})
	if rr := acceptPost(t, s, probeActor, planA.ID); rr.Code != http.StatusOK {
		t.Fatalf("accept plan A = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	return s, spec, planB
}

// rallyPost runs one POST /projects/p/progress/rally/<act> as actor.
func rallyPost(t *testing.T, s *server, h http.HandlerFunc, actor, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := goodPost(body)
	req.SetPathValue("id", "p")
	req = withSubject(req, Subject{ActorID: actor})
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func rallyAddPost(t *testing.T, s *server, doc int64) model.ProgressRallyAddResponse {
	t.Helper()
	rr := rallyPost(t, s, s.progressRallyAdd, probeActor, `{"doc":`+strconv.FormatInt(doc, 10)+`}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("rally add = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	var got model.ProgressRallyAddResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	return got
}

// TestProgressRallyAddAssemblesFourMembers is §8 criterion 17 and 10: a spec
// with one draft plan, one unplanned section and two open tasks yields four
// members — the two tasks, one design task about the spec, one decision task
// about the plan — and adding the same spec again adds nothing.
func TestProgressRallyAddAssemblesFourMembers(t *testing.T) {
	t.Parallel()
	s, spec, planB := newRallyServer(t)

	first := rallyAddPost(t, s, spec.ID)
	if first.Added != 4 || first.Members != 4 {
		t.Fatalf("first add = %+v; want four members added", first)
	}
	if first.Specs != 1 {
		t.Errorf("specs = %d; want the one spec they all come from", first.Specs)
	}

	kinds := map[string]int{}
	for _, id := range rallyMemberIDs(t, s, first.Rally) {
		task, err := s.st.GetTask(t.Context(), id)
		if err != nil {
			t.Fatalf("get member %s: %v", id, err)
		}
		kinds[task.Kind]++
	}
	if kinds["feature"] != 2 || kinds["design"] != 1 || kinds["decision"] != 1 {
		t.Fatalf("member kinds = %v; want two feature, one design, one decision", kinds)
	}

	// §3.5's accept prompt is about the draft plan and lands in its owner's
	// queue, so the person who can accept it is the person holding it.
	prompt, err := s.st.OpenTaskForDoc(t.Context(), planB.ID, "decision")
	if err != nil || prompt == "" {
		t.Fatalf("open decision about plan B = %q, %v; want one", prompt, err)
	}
	task, err := s.st.GetTask(t.Context(), prompt)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if task.Assignee != probeActor {
		t.Errorf("prompt assignee = %q; want the plan's owner %s", task.Assignee, probeActor)
	}

	second := rallyAddPost(t, s, spec.ID)
	if second.Added != 0 || second.Members != 4 || second.Rally != first.Rally {
		t.Fatalf("second add = %+v; want nothing added to %s", second, first.Rally)
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("rally/add", "ok")); n != 2 {
		t.Fatalf("progressWrites{rally/add,ok} = %v, want 2", n)
	}
}

// TestProgressRallyAddAssignsNonCrewOwnerNothing: the accept prompt is
// assigned to the plan's owner only when the owner can hold a task. An owner
// who is not on the crew (029 §6.1) gets an unassigned prompt rather than a
// refused rally.
func TestProgressRallyAddAssignsNonCrewOwnerNothing(t *testing.T) {
	t.Parallel()
	s, spec, planB := newRallyServer(t)
	// otherActor owns plan B and is on no crew.
	if err := s.recordEvent(t.Context(), "cli", "doc.updated", map[string]string{"owner": otherActor},
		func(tx *sql.Tx, eventID int64) error {
			_, err := store.TransferDocOwner(tx, s.st.Now(), planB.ID, otherActor, probeActor, eventID)
			return err
		}); err != nil {
		t.Fatalf("reassign plan B: %v", err)
	}

	if got := rallyAddPost(t, s, spec.ID); got.Added != 4 {
		t.Fatalf("add = %+v; want the rally assembled anyway", got)
	}
	prompt, err := s.st.OpenTaskForDoc(t.Context(), planB.ID, "decision")
	if err != nil || prompt == "" {
		t.Fatalf("open decision about plan B = %q, %v; want one", prompt, err)
	}
	task, err := s.st.GetTask(t.Context(), prompt)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if task.Assignee != "" {
		t.Errorf("prompt assignee = %q; want none for an owner off the crew", task.Assignee)
	}
}

// TestProgressRallyAddRefusesAForeignDoc: the act is assembled from a spec of
// this project, so a plan, and a document of another project, are not found.
func TestProgressRallyAddRefusesAForeignDoc(t *testing.T) {
	t.Parallel()
	s, _, planB := newRallyServer(t)

	rr := rallyPost(t, s, s.progressRallyAdd, probeActor,
		`{"doc":`+strconv.FormatInt(planB.ID, 10)+`}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("add of a plan = %d body=%s; want 404", rr.Code, rr.Body.String())
	}
}

// TestProgressRallyConfirmActivates: Confirm publishes the draft, which is
// what 005 §2a calls activation — ActiveRally returns it afterwards, and the
// project has no draft left.
func TestProgressRallyConfirmActivates(t *testing.T) {
	t.Parallel()
	s, spec, _ := newRallyServer(t)
	added := rallyAddPost(t, s, spec.ID)

	rr := rallyPost(t, s, s.progressRallyConfirm, probeActor, "{}")
	if rr.Code != http.StatusOK {
		t.Fatalf("confirm = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	var got model.ProgressRallyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	if want := (model.ProgressRallyResponse{Rally: added.Rally, State: "ready"}); got != want {
		t.Fatalf("reply = %+v; want %+v", got, want)
	}
	active, err := s.st.ActiveRally(t.Context(), "p")
	if err != nil || active.ID != added.Rally {
		t.Fatalf("active rally = %+v, %v; want %s", active, err, added.Rally)
	}
	if _, err := s.st.DraftRally(t.Context(), "p"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("draft rally after confirm = %v; want ErrNotFound", err)
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("rally/confirm", "ok")); n != 1 {
		t.Fatalf("progressWrites{rally/confirm,ok} = %v, want 1", n)
	}
}

// TestProgressRallyConfirmRefusesASecondActive is §8 criterion 9: the
// backbone allows one active rally per project, so a second Confirm is a 409
// naming the rally that holds the slot, and it activates nothing.
func TestProgressRallyConfirmRefusesASecondActive(t *testing.T) {
	t.Parallel()
	s, spec, _ := newRallyServer(t)

	var running string
	if err := s.recordEvent(t.Context(), "web", "task.created", map[string]string{"kind": "rally"},
		func(tx *sql.Tx, eventID int64) error {
			r, err := store.CreateTask(tx, s.st.Now(), store.TaskInput{
				ProjectID: "p", Title: "Running rally", Kind: "rally",
				Priority: "medium", CreatedBy: probeActor,
			}, eventID)
			if err != nil {
				return err
			}
			running = r.ID
			return store.AttributeEventToTask(tx, eventID, r.ID)
		}); err != nil {
		t.Fatalf("seed active rally: %v", err)
	}
	draft := rallyAddPost(t, s, spec.ID)

	rr := rallyPost(t, s, s.progressRallyConfirm, probeActor, "{}")
	if rr.Code != http.StatusConflict {
		t.Fatalf("confirm = %d body=%s; want 409", rr.Code, rr.Body.String())
	}
	var got model.ProgressRallyConflict
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	if got.Active != running || !strings.Contains(got.Error, running) {
		t.Fatalf("conflict = %+v; want it to name the active rally %s", got, running)
	}
	// Nothing was activated: the draft is still a draft.
	still, err := s.st.DraftRally(t.Context(), "p")
	if err != nil || still.ID != draft.Rally {
		t.Fatalf("draft rally = %+v, %v; want %s untouched", still, err, draft.Rally)
	}
	if n := testutil.ToFloat64(s.progressWrites.WithLabelValues("rally/confirm", "conflict")); n != 1 {
		t.Fatalf("progressWrites{rally/confirm,conflict} = %v, want 1", n)
	}
}

// TestProgressRallyDiscard: Discard abandons the draft, which drops it from
// every reader — the footer included.
func TestProgressRallyDiscard(t *testing.T) {
	t.Parallel()
	s, spec, _ := newRallyServer(t)
	added := rallyAddPost(t, s, spec.ID)

	rr := rallyPost(t, s, s.progressRallyDiscard, probeActor, "{}")
	if rr.Code != http.StatusOK {
		t.Fatalf("discard = %d body=%s; want 200", rr.Code, rr.Body.String())
	}
	if _, err := s.st.DraftRally(t.Context(), "p"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("draft rally after discard = %v; want ErrNotFound", err)
	}
	if band, err := s.st.DraftRallyBand(t.Context(), "p"); err != nil || band != nil {
		t.Fatalf("draft band after discard = %+v, %v; want none", band, err)
	}
	// The tasks it held are untouched: discarding a rally drops the rally,
	// not the work.
	if _, err := s.st.GetTask(t.Context(), added.Rally); err != nil {
		t.Fatalf("get discarded rally: %v", err)
	}
}

// TestProgressRallyMoveNeedsADraft: the footer is not drawn without a draft
// rally, so a Confirm or Discard arriving here came from a stale page.
func TestProgressRallyMoveNeedsADraft(t *testing.T) {
	t.Parallel()
	s, _, _ := newRallyServer(t)

	for _, tc := range []struct {
		name  string
		h     http.HandlerFunc
		route string
	}{
		{name: "confirm", h: s.progressRallyConfirm, route: "rally/confirm"},
		{name: "discard", h: s.progressRallyDiscard, route: "rally/discard"},
	} {
		rr := rallyPost(t, s, tc.h, probeActor, "{}")
		if rr.Code != http.StatusConflict {
			t.Fatalf("%s = %d body=%s; want 409", tc.name, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "no draft rally") {
			t.Errorf("%s body = %s; want it to say why", tc.name, rr.Body.String())
		}
		if n := testutil.ToFloat64(s.progressWrites.WithLabelValues(tc.route, "conflict")); n != 1 {
			t.Fatalf("progressWrites{%s,conflict} = %v, want 1", tc.route, n)
		}
	}
}

// rallyMemberIDs lists the tasks a rally's 'blocks' edges name.
func rallyMemberIDs(t *testing.T, s *server, rallyID string) []string {
	t.Helper()
	_, in, err := s.st.ListEdges(t.Context(), rallyID)
	if err != nil {
		t.Fatalf("list edges of %s: %v", rallyID, err)
	}
	var out []string
	for _, e := range in {
		if e.Type == "blocks" {
			out = append(out, e.FromTask)
		}
	}
	return out
}
