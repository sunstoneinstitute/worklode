// progresswrite_test.go exercises beginJSONPost, the Progress page's write
// gate (WL-SPEC-66 §4.2), rule by rule. It is a white-box test because the
// gate is a helper, not a route: the routes that call it arrive with WL-734,
// and a probe route cannot be registered from the black-box harness —
// routeGuards refuses to boot on a pattern it does not name.
package api

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
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
