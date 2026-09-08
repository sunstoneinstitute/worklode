// progresswrite_test.go exercises beginJSONPost, the Progress page's write
// gate (WL-SPEC-66 §4.2), rule by rule. It is a white-box test because the
// gate is a helper, not a route: the routes that call it arrive with WL-734,
// and a probe route cannot be registered from the black-box harness —
// routeGuards refuses to boot on a pattern it does not name.
package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
