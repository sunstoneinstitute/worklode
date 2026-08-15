// authz.go is worklode's authorization layer. There is no RBAC model in the
// backbone yet — spec 029 §6's Crew roles and project-scoped participation do
// not exist — so this is deliberately a *seam*, not a pretend model: it
// encodes exactly the two-level truth the server already had (every
// authenticated actor, plus instance admins) in one readable place, and gives
// a real policy somewhere to land without touching a single handler.
//
// It follows the split that keeps authorization honest in a Go service:
//
//   - **Subject** (who) is derived once per request by the authentication
//     middleware — from a bearer token on /api/v1, from the session cookie on
//     the web — and carried in the request context. Handlers never re-derive
//     it and never inspect a cookie or a token themselves.
//   - **Policy** (what may be done) is one declarative table, `grants`, read
//     top to bottom. It is data, so it is reviewable in a diff and testable
//     without a server.
//   - **The decision point** (Decide) is a pure function of subject and
//     permission. It is default-deny: a permission with no grant entry is
//     refused, so forgetting to extend the table fails closed.
//   - **The enforcement points** are two middlewares (requirePerm for the
//     JSON API, webGuard for the web UI). They are the only places that write
//     a 401/403, so the denial shape cannot drift per handler.
//
// The route table in server.go names a permission for every route, and the
// router refuses to boot with a route that names none (see router.go) — which
// is what stops the next endpoint from shipping unguarded.
//
// What this does NOT do, and should not be read as doing: there are no
// project-scoped roles, no ownership checks, and no delegation. Decide takes
// the resource it would need for those (see Request.Resource) so adding them
// later does not change any call site, but today it is ignored. A deployment
// with no login provider serves no web surface at all unless it sets
// LODE_WEB_OPEN, and that bypass is a single named decision (authOpen) that is
// counted and logged rather than an implicit passthrough.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"slices"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Role is a named grant a subject holds. Two exist, because two exist in
// Keycloak (spec 001 §9.2 syncs the admin role onto the actor at login) and
// inventing more here would describe a system nobody can populate.
type Role string

const (
	// RoleUser is every authenticated actor — human, agent, or service.
	RoleUser Role = "user"
	// RoleAdmin is an instance administrator: project, actor, and token
	// management, plus the operational verbs.
	RoleAdmin Role = "admin"
)

// Permission names one guarded capability. Permissions are coarser than
// routes and finer than roles: they are the unit a future policy would grant,
// so they split where a policy would plausibly want to (an agent that may
// claim work but not assign it; a watcher that may report runtime events and
// nothing else), not wherever a URL happens to split.
type Permission string

const (
	// permPublic marks a route that is deliberately reachable without a
	// worklode identity. It is never granted to a role — Decide short-circuits
	// it — and every use carries a written reason at the registration site.
	permPublic Permission = "public"

	permTaskRead   Permission = "task.read"
	permTaskWrite  Permission = "task.write"
	permTaskClaim  Permission = "task.claim"
	permTaskAssign Permission = "task.assign"

	permProjectRead  Permission = "project.read"
	permProjectAdmin Permission = "project.admin"

	permDeliverableRead  Permission = "deliverable.read"
	permDeliverableWrite Permission = "deliverable.write"

	permActorAdmin Permission = "actor.admin"

	permSkillRead  Permission = "skill.read"
	permSkillAdmin Permission = "skill.admin"

	permDocRead  Permission = "doc.read"
	permDocWrite Permission = "doc.write"

	permInboxRead   Permission = "inbox.read"
	permInboxTriage Permission = "inbox.triage"
	permInboxAdmin  Permission = "inbox.admin"

	permRuntimeWrite Permission = "runtime.write"

	permWebRead  Permission = "web.read"
	permWebWrite Permission = "web.write"

	// permEventRead covers the read surfaces over the ordered event log
	// (spec 025 §15/§18): the log itself and subscriber status. Any
	// authenticated actor may read them — they are operational visibility,
	// not a write.
	permEventRead Permission = "event.read"
	// permEventAdmin covers seeking a subscriber's offsets: an admin
	// correction of consumer state, not a domain fact (it deliberately
	// bypasses RecordEvent — see events.go).
	permEventAdmin Permission = "event.admin"
)

// grants is the policy: which roles hold which permission. It is the whole
// model — there is nothing else to read.
//
// Today it says what the server has always done: every authenticated actor
// may work the tracker, and instance administration is admin-only. The value
// is not in what it says but in where it says it — one table instead of
// scattered `if actor.Admin` checks, with every permission listed even when
// its grant is unsurprising, so a future policy edits data rather than code.
//
// A permission absent from this map is denied to everyone (see Decide).
var grants = map[Permission][]Role{
	permTaskRead:   {RoleUser, RoleAdmin},
	permTaskWrite:  {RoleUser, RoleAdmin},
	permTaskClaim:  {RoleUser, RoleAdmin},
	permTaskAssign: {RoleUser, RoleAdmin},

	permProjectRead:  {RoleUser, RoleAdmin},
	permProjectAdmin: {RoleAdmin},

	// Declaring what a project ships is its own capability, not a flavour of
	// task.write: spec 029 §3 makes a deliverable a different object, and a
	// policy that lets an agent file tasks without letting it redefine the
	// definition of done is one someone will plausibly want.
	permDeliverableRead:  {RoleUser, RoleAdmin},
	permDeliverableWrite: {RoleUser, RoleAdmin},

	// Admin-only for a reason worth keeping written down: any bearer token
	// could otherwise mint further tokens, which is privilege escalation.
	permActorAdmin: {RoleAdmin},

	permSkillRead:  {RoleUser, RoleAdmin},
	permSkillAdmin: {RoleAdmin},

	permDocRead:  {RoleUser, RoleAdmin},
	permDocWrite: {RoleUser, RoleAdmin},

	permInboxRead:   {RoleUser, RoleAdmin},
	permInboxTriage: {RoleUser, RoleAdmin},
	permInboxAdmin:  {RoleAdmin},

	permRuntimeWrite: {RoleUser, RoleAdmin},

	permWebRead:  {RoleUser, RoleAdmin},
	permWebWrite: {RoleUser, RoleAdmin},

	permEventRead:  {RoleUser, RoleAdmin},
	permEventAdmin: {RoleAdmin},
}

// authMethod records how a subject was identified, so a denial can say
// whether the caller should authenticate or give up, and so the open-web
// bypass is visible in logs and metrics rather than indistinguishable from a
// real login.
type authMethod string

const (
	authNone    authMethod = "none"    // no credential presented
	authToken   authMethod = "token"   // wl_ bearer token (/api/v1)
	authSession authMethod = "session" // signed session cookie (web UI)
	// authOpen is the deployment-wide bypass: no login provider is configured
	// and LODE_WEB_OPEN asked for it, so the web UI serves anyone who can
	// reach the port. It is a
	// Subject like any other, carrying only RoleUser — never RoleAdmin, so a
	// future admin-only web route fails closed on an open instance.
	authOpen authMethod = "open"
)

// Subject is the authenticated caller, derived once per request by the
// authentication middleware and carried in the request context. ActorID is ""
// for an unauthenticated or open-deployment subject; handlers that attribute
// a write use it directly and write NULL when it is empty.
type Subject struct {
	ActorID string
	Kind    string // human, agent, service ("" when there is no actor)
	Roles   []Role
	Via     authMethod
}

// Authenticated reports whether the subject presented a credential. An open
// deployment's subject is not authenticated even though it is permitted.
func (s Subject) Authenticated() bool {
	return s.Via == authToken || s.Via == authSession
}

// HasRole reports whether the subject holds role.
func (s Subject) HasRole(role Role) bool { return slices.Contains(s.Roles, role) }

// subjectFromActor builds the subject for an authenticated actor. Every actor
// holds RoleUser; the admin flag — synced from Keycloak's admin role at login
// (spec 001 §9.2) — adds RoleAdmin. This is the one place actor state becomes
// policy input.
func subjectFromActor(a *store.Actor, via authMethod) Subject {
	if a == nil {
		return Subject{Via: authNone}
	}
	roles := []Role{RoleUser}
	if a.Admin {
		roles = append(roles, RoleAdmin)
	}
	return Subject{ActorID: a.ID, Kind: a.Kind, Roles: roles, Via: via}
}

// openSubject is the subject an unauthenticated-by-configuration web request
// gets: permitted as an ordinary user, attributable to nobody. It is reached
// only under webOpen — no login provider configured *and* LODE_WEB_OPEN set —
// so it is never how a request is served on an instance that merely forgot to
// configure one.
func openSubject() Subject {
	return Subject{Roles: []Role{RoleUser}, Via: authOpen}
}

// Decision is the outcome of one authorization check. Reason is a bounded,
// non-identifying token ("allowed", "no_role", "unauthenticated",
// "unknown_permission") suitable for both the audit log and a metric label.
type Decision struct {
	Allowed bool
	Reason  string
	// AdminOnly is true when the permission is granted to admins alone, which
	// is the only case whose denial message names a specific role.
	AdminOnly bool
}

// Request is what a decision is made about. Resource is unused today and is
// here so that the project-scoped decision spec 029 §6 will need ("may this
// actor approve in *that* project") arrives as a new field on an existing
// struct rather than as a new signature at every call site.
type Request struct {
	Subject    Subject
	Permission Permission
	Resource   string
}

// Decide is the policy decision point: a pure function of the request, with
// no I/O and no request plumbing, so the policy is testable as a table.
// It is default-deny — an unknown permission is refused, not waved through.
func Decide(req Request) Decision {
	if req.Permission == permPublic {
		return Decision{Allowed: true, Reason: "public"}
	}
	allowed, known := grants[req.Permission]
	if !known {
		return Decision{Reason: "unknown_permission"}
	}
	adminOnly := len(allowed) == 1 && allowed[0] == RoleAdmin
	for _, role := range allowed {
		if req.Subject.HasRole(role) {
			return Decision{Allowed: true, Reason: "allowed", AdminOnly: adminOnly}
		}
	}
	if len(req.Subject.Roles) == 0 {
		return Decision{Reason: "unauthenticated", AdminOnly: adminOnly}
	}
	return Decision{Reason: "no_role", AdminOnly: adminOnly}
}

// denialMessage is what a refused caller is told. Admin-only permissions keep
// the wording the API has always used; everything else says what was needed
// without leaking the policy's shape.
func denialMessage(d Decision) string {
	if d.AdminOnly {
		return "admin required"
	}
	return "forbidden"
}

// --- context plumbing -------------------------------------------------------

// subjectKey is the context key for the request's Subject.
type subjectKey struct{}

// withSubject returns r carrying sub. Called only by the authentication
// middlewares.
func withSubject(r *http.Request, sub Subject) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), subjectKey{}, sub))
}

// subjectFrom returns the request's subject, or the zero (unauthenticated)
// Subject outside the authentication middleware.
func subjectFrom(r *http.Request) Subject {
	sub, _ := r.Context().Value(subjectKey{}).(Subject)
	return sub
}

// --- enforcement ------------------------------------------------------------

// requirePerm is the /api/v1 policy enforcement point. It runs inside auth,
// which authenticated the caller and put both the actor and the subject in
// the context, so an unauthenticated request never reaches it — every denial
// here is a 403, never a 401.
func (s *server) requirePerm(perm Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub := subjectFrom(r)
		d := Decide(Request{Subject: sub, Permission: perm})
		s.observeAuthz(perm, d)
		if !d.Allowed {
			s.logDenial(r, sub, perm, d)
			writeErr(w, http.StatusForbidden, denialMessage(d))
			return
		}
		next(w, r)
	}
}

// webGuard is the web UI's combined authentication and policy enforcement
// point, replacing the former webAuth. It resolves the session cookie to a
// subject (loading the actor, so the web surface knows who is acting and not
// merely that the cookie verifies), decides, and then either serves the page,
// refuses on an instance with no login provider, sends an unauthenticated
// visitor to login, or renders a 403.
//
// With no login provider configured it uses openSubject only when the
// operator opted in (see webOpen): that bypass is a decision that is counted
// and logged rather than a silent passthrough. Without the opt-in there is no
// identity to derive and no login flow to offer, so the page is refused.
func (s *server) webGuard(perm Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub := s.webSubject(r.Context(), r)
		d := Decide(Request{Subject: sub, Permission: perm})
		s.observeAuthz(perm, d)
		if d.Allowed {
			next(w, withSubject(r, sub))
			return
		}
		s.logDenial(r, sub, perm, d)
		if s.oidc == nil {
			// Refused because the instance is misconfigured, not because this
			// caller lacks something. 503 and not 403: the page is unavailable
			// on this deployment, and no credential the caller could present
			// would change that.
			webErr(w, http.StatusServiceUnavailable,
				"the web UI needs a login provider: configure LODE_OIDC_ISSUER "+
					"and LODE_OIDC_CLIENT_ID, or set LODE_WEB_OPEN=1 to serve it "+
					"unauthenticated on a trusted network")
			return
		}
		if !sub.Authenticated() {
			http.Redirect(w, r, s.loginTarget(r.URL.Path), http.StatusFound)
			return
		}
		webErr(w, http.StatusForbidden, denialMessage(d))
	}
}

// webOpen reports whether this instance deliberately serves the web UI to
// anonymous callers: no login provider is configured *and* the operator asked
// for it. Both halves matter — the first makes it the only way to serve a
// page at all, the second makes it a decision rather than an oversight.
func (s *server) webOpen() bool {
	return s.oidc == nil && s.cfg.WebOpen
}

// webSubject derives the subject behind a web request: the open-deployment
// subject when no login provider is configured and the operator opted in,
// nobody at all when no login provider is configured and they did not, and
// otherwise the actor named by a valid session cookie. A cookie that verifies
// but names an actor that no
// longer exists yields an unauthenticated subject — the session outlived its
// actor, and treating it as anonymous is both safe and honest.
func (s *server) webSubject(ctx context.Context, r *http.Request) Subject {
	if s.oidc == nil {
		if s.webOpen() {
			return openSubject()
		}
		// No provider and no opt-in: there is no identity to derive and no
		// login flow to send the caller into. webGuard turns this into a
		// refusal; returning an unauthenticated subject here would send them
		// to /auth/login, which 404s without OIDC.
		return Subject{Via: authNone}
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return Subject{Via: authNone}
	}
	actorID, ok := verifySession(s.cfg.SessionSecret, c.Value, s.st.Now())
	if !ok {
		return Subject{Via: authNone}
	}
	a, err := s.st.GetActor(ctx, actorID)
	if err != nil || a == nil {
		return Subject{Via: authNone}
	}
	return subjectFromActor(a, authSession)
}

// logDenial records a refused request. A refusal of someone who *is* signed
// in is the security-relevant kind — somebody reached for something they do
// not hold — and logs at Warn. A refusal of an anonymous visitor is the
// ordinary "you are not logged in yet" that precedes every login redirect, so
// it logs at Debug: at Warn it would bury the first kind under crawler
// traffic within a day.
func (s *server) logDenial(r *http.Request, sub Subject, perm Permission, d Decision) {
	level := slog.LevelDebug
	if sub.Authenticated() {
		level = slog.LevelWarn
	}
	s.log.Log(r.Context(), level, "authorization denied",
		"actor", sub.ActorID,
		"via", string(sub.Via),
		"permission", string(perm),
		"reason", d.Reason,
		"method", r.Method,
		"path", r.URL.Path,
	)
}
