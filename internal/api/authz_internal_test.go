package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// An internal test: the policy is deliberately unexported (it is not an API
// other packages should reach into), and the point of most of these checks is
// to hold the *table* to its claims, which only this package can see.

func adminSubject() Subject {
	return subjectFromActor(&store.Actor{ID: "root", Kind: "human", Admin: true}, authToken)
}

func userSubject() Subject {
	return subjectFromActor(&store.Actor{ID: "dana", Kind: "human"}, authToken)
}

// TestDecideDefaultDeny is the property the whole layer rests on: a
// permission nobody granted is refused, including one that simply has not
// been added to the table yet. Fail-closed is the difference between a policy
// and a suggestion.
func TestDecideDefaultDeny(t *testing.T) {
	for _, sub := range []Subject{adminSubject(), userSubject(), {}} {
		d := Decide(Request{Subject: sub, Permission: Permission("task.teleport")})
		if d.Allowed {
			t.Errorf("subject %+v was allowed an ungranted permission", sub)
		}
		if d.Reason != "unknown_permission" {
			t.Errorf("reason = %q, want unknown_permission", d.Reason)
		}
	}
}

// TestDecideRoles checks the two-level model the server actually has: every
// authenticated actor works the tracker, admin-only permissions need the
// admin role, and an anonymous subject gets nothing.
func TestDecideRoles(t *testing.T) {
	for _, tt := range []struct {
		name    string
		sub     Subject
		perm    Permission
		allowed bool
		reason  string
	}{
		{"user may write tasks", userSubject(), permTaskWrite, true, "allowed"},
		{"user may not manage projects", userSubject(), permProjectAdmin, false, "no_role"},
		{"user may not mint tokens", userSubject(), permActorAdmin, false, "no_role"},
		{"user may author documents", userSubject(), permDocWrite, true, "allowed"},
		{"user may not import a corpus", userSubject(), permDocImport, false, "no_role"},
		{"admin may import a corpus", adminSubject(), permDocImport, true, "allowed"},
		{"admin may write tasks", adminSubject(), permTaskWrite, true, "allowed"},
		{"admin may manage projects", adminSubject(), permProjectAdmin, true, "allowed"},
		{"anonymous may not read", Subject{Via: authNone}, permTaskRead, false, "unauthenticated"},
		{"anonymous may not read the web", Subject{Via: authNone}, permWebRead, false, "unauthenticated"},
		{"open deployment may read the web", openSubject(), permWebRead, true, "allowed"},
		{"open deployment may write through the cockpit", openSubject(), permWebWrite, true, "allowed"},
		{"public needs nothing", Subject{Via: authNone}, permPublic, true, "public"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := Decide(Request{Subject: tt.sub, Permission: tt.perm})
			if d.Allowed != tt.allowed || d.Reason != tt.reason {
				t.Errorf("Decide = {allowed:%v reason:%q}, want {allowed:%v reason:%q}",
					d.Allowed, d.Reason, tt.allowed, tt.reason)
			}
		})
	}
}

// TestOpenSubjectIsNeverAdmin pins the one deliberate asymmetry in the open
// (no login provider) bypass: it keeps the UI reachable, but it must not hand
// out administration. A future admin-only web route has to fail closed on an
// instance where anyone can reach the port.
func TestOpenSubjectIsNeverAdmin(t *testing.T) {
	sub := openSubject()
	if sub.HasRole(RoleAdmin) {
		t.Fatal("the open-deployment subject holds the admin role")
	}
	if sub.Authenticated() {
		t.Error("the open-deployment subject reports itself authenticated")
	}
	if d := Decide(Request{Subject: sub, Permission: permProjectAdmin}); d.Allowed {
		t.Error("the open-deployment subject was allowed an admin permission")
	}
}

// TestAdminOnlyDenialWording checks the message an admin-gated refusal
// carries. It is derived from the policy rather than written per route, so
// this is what keeps every admin route saying the same thing.
func TestAdminOnlyDenialWording(t *testing.T) {
	for _, perm := range []Permission{
		permProjectAdmin, permActorAdmin, permSkillAdmin, permInboxAdmin, permDocImport,
	} {
		d := Decide(Request{Subject: userSubject(), Permission: perm})
		if got := denialMessage(d); got != "admin required" {
			t.Errorf("%s denial message = %q, want %q", perm, got, "admin required")
		}
	}
	d := Decide(Request{Subject: Subject{Via: authNone}, Permission: permTaskRead})
	if got := denialMessage(d); got != "forbidden" {
		t.Errorf("non-admin denial message = %q, want %q", got, "forbidden")
	}
}

// TestEveryGuardedPermissionIsGranted checks the two halves of the model
// agree: every permission a route requires exists in the policy, and every
// permission in the policy guards at least one route. Either kind of drift
// leaves a rule that looks enforced and is not.
func TestEveryGuardedPermissionIsGranted(t *testing.T) {
	used := map[Permission]bool{}
	for pattern, g := range routeGuards {
		if g.perm == permPublic {
			continue
		}
		if _, ok := grants[g.perm]; !ok {
			t.Errorf("route %q requires %q, which the grants table does not define "+
				"(Decide would deny every caller)", pattern, g.perm)
		}
		used[g.perm] = true
	}
	for perm := range grants {
		if !used[perm] {
			t.Errorf("permission %q is granted to roles but guards no route", perm)
		}
	}
}

// TestPublicRoutesAreAnExplicitList is the review gate on unauthenticated
// surface area. Adding a route that anyone on the network may call means
// editing this list, in a diff, on purpose — it cannot happen by forgetting a
// wrapper.
func TestPublicRoutesAreAnExplicitList(t *testing.T) {
	want := []string{
		"GET /.well-known/lode-login",
		"GET /assets/",
		"GET /auth/callback",
		"GET /auth/cli/login",
		"GET /auth/login",
		"GET /auth/oidc/config",
		"POST /auth/cli/token",
		"POST /auth/oidc/token",
		"POST /hooks/catalog",
		"POST /hooks/flux",
		"POST /hooks/github",
	}

	var got []string
	for pattern, g := range routeGuards {
		if g.perm == permPublic {
			got = append(got, pattern)
			if strings.TrimSpace(g.public) == "" {
				t.Errorf("public route %q states no reason", pattern)
			}
		}
	}
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf("the set of unauthenticated routes changed.\n got: %v\nwant: %v\n\n"+
			"If this is intended, update the list here — deliberately, having "+
			"checked the new route is safe to expose.", got, want)
	}
}

// TestNoWriteRouteIsPublic is a blunter cross-check on the same surface: a
// route that mutates state must never be reachable without either an actor or
// a signature. The three signed webhooks and the two login-flow POSTs are the
// enumerated exceptions, each of which authenticates by other means.
func TestNoWriteRouteIsPublic(t *testing.T) {
	signedOrLogin := map[string]bool{
		"POST /hooks/github":    true, // HMAC
		"POST /hooks/flux":      true, // HMAC
		"POST /hooks/catalog":   true, // HMAC
		"POST /auth/oidc/token": true, // verifies a Keycloak ID token
		"POST /auth/cli/token":  true, // redeems a one-time code
	}
	for pattern, g := range routeGuards {
		if g.perm != permPublic || signedOrLogin[pattern] {
			continue
		}
		if method, _, ok := strings.Cut(pattern, " "); ok && method != http.MethodGet {
			t.Errorf("route %q mutates state and is public with no signature check", pattern)
		}
	}
}

// TestRouterRejectsUndeclaredRoute proves the boot-time guarantee rather than
// asserting it in a comment: registering a route the table does not name
// panics, so an unguarded endpoint cannot reach production.
func TestRouterRejectsUndeclaredRoute(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("registering an undeclared route did not panic")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "routeGuards") {
			t.Fatalf("panic = %v, want it to name routeGuards", r)
		}
	}()
	rt := newRouter(&server{}, http.NewServeMux())
	rt.api("GET /api/v1/undeclared", func(http.ResponseWriter, *http.Request) {})
}

// TestRouterRejectsPublicWithoutReason checks the other half: a route may be
// unauthenticated, but not silently so.
func TestRouterRejectsPublicWithoutReason(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("a public route with no stated reason did not panic")
		}
	}()
	// An injected table, so a case about a malformed table cannot leave the
	// real one modified for whatever runs next.
	rt := newRouterWithGuards(&server{}, http.NewServeMux(), map[string]routeGuard{
		"GET /silent": {perm: permPublic},
	})
	rt.public("GET /silent", http.NotFoundHandler())
}

// TestUnusedGuardsAreReported checks the dead-policy check: a table entry no
// route claims is reported, because a rule nothing enforces reads like a
// guard to the next person who greps for it.
func TestUnusedGuardsAreReported(t *testing.T) {
	rt := newRouter(&server{}, http.NewServeMux())
	err := rt.checkComplete()
	if err == nil {
		t.Fatal("a router that registered nothing reported a complete table")
	}
	if !strings.Contains(err.Error(), "GET /api/v1/tasks") {
		t.Errorf("error does not name the unregistered routes: %v", err)
	}
}

// TestRequireSession checks the authentication-method gate spec 029 §7.3
// puts in front of approval decisions: only a subject authenticated by a
// live session cookie reaches the wrapped handler. A bearer token and the
// open-deployment bypass are refused exactly like an unauthenticated caller
// — this is a check on Via, not on role, so an admin token does not pass
// either.
func TestRequireSession(t *testing.T) {
	for _, via := range []authMethod{authNone, authToken, authSession, authOpen} {
		t.Run(string(via), func(t *testing.T) {
			s := &server{log: slog.Default()}
			reached := false
			h := s.requireSession(func(http.ResponseWriter, *http.Request) {
				reached = true
			})

			r := httptest.NewRequest(http.MethodPost, "/approvals/1/decide", nil)
			r = withSubject(r, Subject{Via: via, Roles: []Role{RoleUser, RoleAdmin}})
			rr := httptest.NewRecorder()
			h(rr, r)

			if via == authSession {
				if !reached {
					t.Fatal("a session subject did not reach the handler")
				}
				return
			}
			if reached {
				t.Fatalf("via %q reached the wrapped handler", via)
			}
			if rr.Code != http.StatusForbidden {
				t.Errorf("via %q: status = %d, want %d", via, rr.Code, http.StatusForbidden)
			}
			const want = "approving requires a signed-in browser session"
			if !strings.Contains(rr.Body.String(), want) {
				t.Errorf("via %q: body = %q, want it to contain %q", via, rr.Body.String(), want)
			}
		})
	}
}

// TestSubjectFromActorGroups pins that Subject.Groups is a plain passthrough
// of store.Actor.Groups — no filtering, no re-derivation — so the staleness
// requireSession guards against is the actor row's, not something this
// mapping adds on top.
func TestSubjectFromActorGroups(t *testing.T) {
	groups := []string{"crew-backbone", "org-admins"}
	sub := subjectFromActor(&store.Actor{ID: "dana", Kind: "human", Groups: groups}, authSession)
	if !slices.Equal(sub.Groups, groups) {
		t.Errorf("Groups = %v, want %v", sub.Groups, groups)
	}
}
