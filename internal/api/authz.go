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
//   - **The enforcement points** are three middlewares (requirePerm for the
//     JSON API, webGuard for the web UI, eitherGuard for the asset routes both
//     audiences fetch). They are the only places that write a 401/403, so the
//     denial shape cannot drift per handler.
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
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

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
	// permProjectReport covers reporting overhead usage (spec 052 §2): tokens
	// spent with no task to bill to, from a main-checkout orchestration
	// session or a worktree whose lease the reporting actor no longer held.
	// Not a claim on any one task, so every authenticated role may report it.
	permProjectReport Permission = "project.report"

	permDeliverableRead  Permission = "deliverable.read"
	permDeliverableWrite Permission = "deliverable.write"

	// permCrewWrite covers changing a project's Crew: who is on it and with
	// which role labels (spec 029 §6.1). Its own capability rather than a
	// flavour of project.admin, because Crew is the working group of a
	// project rather than its configuration — spec 029 §6.1 puts the change
	// in the hands of the Crew itself, not of an instance administrator.
	permCrewWrite Permission = "crew.write"

	// permDocRead and permDocWrite cover the backbone's design documents (spec
	// 025 §5): specs, ADRs and plans. Their own capability rather than a
	// flavour of task.read/task.write, because a document is a different
	// object with a different lifecycle — authoring and accepting the corpus
	// is not the same authority as filing and closing work, and a policy that
	// lets an agent work the tracker without letting it edit the specs that
	// govern it is one someone will plausibly want. Today's grant is the same
	// set, so nothing changes; the seam is what is being added.
	permDocRead  Permission = "doc.read"
	permDocWrite Permission = "doc.write"
	// permDocImport is the corpus importer's authority (025 §12). It asserts
	// facts the ordinary lifecycle establishes — a status set directly instead
	// of through the accept gate, an edge set replaced wholesale — so it is an
	// instance-administration act rather than ordinary authoring, and is
	// separated from doc.write for exactly that reason.
	permDocImport Permission = "doc.import"

	permActorAdmin Permission = "actor.admin"

	// permTaskToken covers minting a task-scoped token (001 §2.1, WL-306):
	// POST /api/v1/tasks/{id}/tokens. Not actor.admin, deliberately — the
	// minted credential is strictly narrower than the minter's own (bound to
	// one task, expiring with its lease, unable to mint further), so a user
	// dispatching their own sandbox is privilege reduction, not escalation.
	// A task-scoped token itself cannot reach the route: its guard names no
	// task scope, and the default is deny.
	permTaskToken Permission = "task.token"

	permSkillRead  Permission = "skill.read"
	permSkillAdmin Permission = "skill.admin"

	permInboxRead   Permission = "inbox.read"
	permInboxTriage Permission = "inbox.triage"
	permInboxAdmin  Permission = "inbox.admin"

	permRuntimeWrite Permission = "runtime.write"

	// permSecretRead covers reading the org secrets catalog: names, purposes
	// and op:// references, never values. It is its own permission rather
	// than a flavour of task.read because the two disclose different things —
	// the catalog is the shape of the org's vault, and a policy that lets a
	// reporting or read-only actor see the work without seeing the vault
	// topology is one someone will plausibly want. Today's grant is the same
	// set, so nothing changes; the seam is what is being added.
	permSecretRead Permission = "secret.read"

	permWebRead  Permission = "web.read"
	permWebWrite Permission = "web.write"

	// permBlobWrite and permBlobRead cover the content-addressed blobs a task
	// body embeds (spec 021). Their own capability rather than a flavour of
	// task.write/task.read because the objects outlive and are addressed
	// independently of any one task: a blob is referenced by hash, and nothing
	// about the upload names the work it belongs to.
	permBlobWrite Permission = "blob.write"
	permBlobRead  Permission = "blob.read"
	// permBlobAdmin covers the GC sweeps (spec 021 §11): they delete index
	// rows and object-store bytes on every actor's behalf, which is instance
	// administration, not ordinary blob authoring.
	permBlobAdmin Permission = "blob.admin"

	// permEventRead covers the read surfaces over the ordered event log
	// (spec 025 §15/§18): the log itself and subscriber status. Any
	// authenticated actor may read them — they are operational visibility,
	// not a write.
	permEventRead Permission = "event.read"
	// permEventAdmin covers seeking a subscriber's offsets: an admin
	// correction of consumer state, not a domain fact (it deliberately
	// bypasses RecordEvent — see events.go).
	permEventAdmin Permission = "event.admin"
	// permEventStream covers following the log live over server-sent events.
	// Admin-only although it exposes exactly the rows permEventRead already
	// does: the grant being narrowed is operational, not informational — a
	// follow holds a connection, a goroutine and a repeating horizon-bounded
	// query open for as long as the client watches, which is a resource an
	// instance hands out deliberately.
	permEventStream Permission = "event.stream"

	// permProjectionRead covers GET /api/v1/graph/projection/failures: which
	// projects the knowledge-graph projector has quarantined, since when, and
	// why. Any authenticated actor may read it, on permEventRead's reasoning —
	// it is operational visibility over a derived record, not a write, and the
	// alternative today is psql against the backbone.
	permProjectionRead Permission = "projection.read"

	// permWhoAmI covers GET /api/v1/whoami: any authenticated actor may ask
	// which identity their token resolves to. No admin gate — this is how
	// the CLI (and lode doctor) confirms a token is accepted and whose it is.
	permWhoAmI Permission = "whoami"

	// permReconcile covers spec 013's reconciliation surface: GET
	// /api/v1/repos/doctor (ingestion health across the org) and POST
	// /api/v1/reconcile (replays/repairs ingestion). Admin-only — both read and
	// act across every project's repos, not just the caller's own work.
	permReconcile Permission = "reconcile"

	// permOverviewRead covers spec 007's five read surfaces: the roll-up,
	// drift, gaps, the frontier mirror and the critical path. Its own
	// capability rather than a flavour of task.read because what it discloses
	// is a different object — the org-wide shape of the work and where the
	// code has drifted from the design, joined across the backbone and the
	// knowledge graph — rather than the tasks a working actor reads.
	permOverviewRead Permission = "overview.read"
	// permDeriveRun covers POST /api/v1/derive: running the server-side
	// derivers. Admin-only, on both counts that separate an operational act
	// from an ordinary one — it replaces org-wide named graphs wholesale, and
	// it spends GitHub App API calls across every repo the org has mapped.
	permDeriveRun Permission = "derive.run"

	// permApprovalDecide covers deciding an approval (spec 029 §7.3): POST
	// /approvals/{id}/decide, the web surface's only decision act. The route
	// is additionally gated by requireSession, an authentication-method check
	// that runs ahead of and is orthogonal to this role check, and by the
	// approval's own required_role, which is a per-row fact the store checks
	// rather than a role in this table.
	permApprovalDecide Permission = "approval.decide"

	// permApprovalRead covers reading the awaiting queue (029 §7.1): GET
	// /api/v1/approvals. Its own permission rather than a flavour of
	// task.read, because the queue spans entity kinds — a pull request, a
	// document — and reads across every project, which is what makes "who
	// may see the whole review backlog" a question worth being able to
	// answer separately from "who may read a task".
	permApprovalRead Permission = "approval.read"
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

	permProjectRead:   {RoleUser, RoleAdmin},
	permProjectAdmin:  {RoleAdmin},
	permProjectReport: {RoleUser, RoleAdmin},

	// Declaring what a project ships is its own capability, not a flavour of
	// task.write: spec 029 §3 makes a deliverable a different object, and a
	// policy that lets an agent file tasks without letting it redefine the
	// definition of done is one someone will plausibly want.
	permDeliverableRead:  {RoleUser, RoleAdmin},
	permDeliverableWrite: {RoleUser, RoleAdmin},

	// Every authenticated actor, which is wider than spec 029 §6.1 asks for:
	// the spec scopes the change to the project's own Crew ("any Crew member
	// may add or remove"), and this table has no project scope to express
	// that in — grants are instance-wide, keyed by role alone. This is a
	// declared, accepted gap, not an oversight: narrowing it needs
	// project-scoped authorization, which is a change to the model here
	// rather than a check inside a handler.
	permCrewWrite: {RoleUser, RoleAdmin},

	// Authoring the corpus is open to every authenticated actor; who may
	// *accept* a document is not a role question at all — 025 §7 gates it on
	// the document's assignee, checked in the store.
	permDocRead:  {RoleUser, RoleAdmin},
	permDocWrite: {RoleUser, RoleAdmin},
	// Admin-only: importing a corpus states statuses and edge sets directly,
	// which is instance administration, not authoring.
	permDocImport: {RoleAdmin},

	// Admin-only for a reason worth keeping written down: any bearer token
	// could otherwise mint further tokens, which is privilege escalation.
	permActorAdmin: {RoleAdmin},

	// See the permission's comment: minting a task-scoped token narrows
	// privilege, so it is every authenticated actor's.
	permTaskToken: {RoleUser, RoleAdmin},

	permSkillRead:  {RoleUser, RoleAdmin},
	permSkillAdmin: {RoleAdmin},

	permInboxRead:   {RoleUser, RoleAdmin},
	permInboxTriage: {RoleUser, RoleAdmin},
	permInboxAdmin:  {RoleAdmin},

	permRuntimeWrite: {RoleUser, RoleAdmin},

	// Every authenticated actor, because every actor about to claim a task
	// needs to know which secrets that task names and where they live.
	permSecretRead: {RoleUser, RoleAdmin},

	permWebRead:  {RoleUser, RoleAdmin},
	permWebWrite: {RoleUser, RoleAdmin},

	// Attaching a screenshot to the work you are describing is ordinary work,
	// so uploading is every authenticated actor's.
	permBlobWrite: {RoleUser, RoleAdmin},
	// Reading a blob is the asset half of reading a task: the body says
	// ![](/blob/<hash>), and anyone who may read the body may see the picture
	// in it. Narrowing this below task.read/web.read would render task pages
	// with broken images rather than protect anything.
	permBlobRead: {RoleUser, RoleAdmin},
	// Admin-only: a GC sweep deletes data on every actor's behalf, so running
	// one is an operational act, not ordinary blob authoring.
	permBlobAdmin: {RoleAdmin},

	// Reading the queue is every authenticated actor's: an agent that has
	// just asked for review needs to see whether it is still outstanding.
	// Deciding is not in this table's gift alone — see permApprovalDecide.
	permApprovalRead: {RoleUser, RoleAdmin},

	permEventRead:      {RoleUser, RoleAdmin},
	permProjectionRead: {RoleUser, RoleAdmin},
	permEventAdmin:     {RoleAdmin},
	permEventStream:    {RoleAdmin},

	permWhoAmI: {RoleUser, RoleAdmin},

	permReconcile: {RoleAdmin},

	// Every authenticated actor: the overview reads are how anyone working
	// the tracker sees what is ready and what has drifted. Running the
	// derivers is admin-only — see permDeriveRun.
	permOverviewRead: {RoleUser, RoleAdmin},
	permDeriveRun:    {RoleAdmin},

	// Every authenticated actor, because who may decide a *given* approval is
	// not a role question: 029 §7.1 gates it on the approval's own
	// required_role against the decider's groups, checked in the store. This
	// permission gates reaching the route at all.
	permApprovalDecide: {RoleUser, RoleAdmin},
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
	// TaskID is the task a task-scoped token is bound to (001 §2.1, WL-306),
	// "" for every other subject. A non-empty TaskID narrows what the
	// subject may reach: only routes whose guard names a task scope (see
	// routeGuard.taskScope), with the {id}-bound ones additionally requiring
	// the path's task to be this one.
	TaskID string
	// Groups is the actor row's stored groups claim (store.Actor.Groups),
	// carried through unfiltered. For a token subject it is only as fresh as
	// the actor row's last login sync; a session subject is no fresher by
	// itself — freshness comes from gating the act on Via, not from this
	// field, which is why requireSession exists. Nil for authOpen/authNone.
	Groups []string
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
	return Subject{ActorID: a.ID, Kind: a.Kind, Roles: roles, Via: via, Groups: a.Groups}
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

// actorIDFrom returns the id of the acting actor to attribute a write to, or
// "" when there is none: an unauthenticated request, or a web request on an
// instance with no login provider, where the subject is permitted but
// anonymous (openSubject/authOpen). The guard already resolved and validated
// the subject — including confirming the actor row still exists — so this is
// a context read, not a second authentication. It is the one way a handler
// names who is acting: the request carried both a *store.Actor and a Subject
// before, and every handler read the same id off one of them.
func actorIDFrom(r *http.Request) string {
	return subjectFrom(r).ActorID
}

// --- enforcement ------------------------------------------------------------

// requireTaskScope enforces 001 §2.1's narrowing for task-scoped subjects
// (WL-306). It composes ahead of the handler, after requirePerm: an
// ordinary subject passes untouched; a task-scoped one is refused unless
// this route's guard names a task scope, and on an {id}-bound route unless
// the path's task is the bound one. Default-deny by construction — a route
// nobody marked is a route no task-scoped token reaches.
func (s *server) requireTaskScope(g routeGuard, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub := subjectFrom(r)
		if sub.TaskID == "" {
			next(w, r)
			return
		}
		switch g.taskScope {
		case taskScopeAny:
			next(w, r)
			return
		case taskScopeBound:
			if r.PathValue("id") == sub.TaskID {
				next(w, r)
				return
			}
		}
		d := Decision{Reason: "task_scope"}
		s.observeAuthz(g.perm, d)
		s.logDenial(r, sub, g.perm, d)
		writeErr(w, http.StatusForbidden, "token is scoped to task "+sub.TaskID)
	}
}

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
// point. It resolves the session cookie to a
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

// requireSession refuses any subject not authenticated by a live OIDC
// session cookie. Spec 029 §7.3: approving is a web-session act because the
// session's group claims are at most as old as the login that stored them; a
// bearer token's are as old as the token. authOpen is refused too — an open
// instance has no identity to attribute a decision to.
func (s *server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub := subjectFrom(r)
		if sub.Via != authSession {
			d := Decision{Reason: "session_required"}
			s.observeAuthz(permApprovalDecide, d)
			s.logDenial(r, sub, permApprovalDecide, d)
			webErr(w, http.StatusForbidden,
				"approving requires a signed-in browser session")
			return
		}
		next(w, r)
	}
}

// eitherGuard is the asset routes' combined authentication and policy
// enforcement point: it accepts a bearer token *or* a web session, because a
// blob is the one thing both audiences fetch directly — a browser <img> on a
// task page carries the session cookie, the CLI and agents carry a token.
//
// Three properties are deliberate:
//
//   - It inherits the UI's posture instead of setting its own. With no login
//     provider configured, webSubject yields the open subject only when the
//     operator set LODE_WEB_OPEN, and nobody at all otherwise — so a blob is
//     refused on a closed instance and served on one that opted in, exactly
//     as the pages are (spec 021 §4). A blob route is not the place to
//     unilaterally tighten or loosen an installation's auth model.
//   - It answers with writeErr (JSON), never webErr (HTML), and never a login
//     redirect. This is a subresource: a browser fetching <img src="/blob/…">
//     cannot usefully follow a redirect to a login page — it would render the
//     HTML as a broken image — so the honest answer is a status code. That
//     code is 401 and not webGuard's 503, even on a provider-less instance:
//     unlike a page, a blob there *is* served to a caller carrying a bearer
//     token, so the anonymous refusal is a missing credential rather than a
//     deployment fact (spec 021 §15 AC3).
//   - A bearer token that does not resolve is refused outright rather than
//     retried as a web request. Falling through to webSubject would serve the
//     blob to a rejected credential whenever LODE_WEB_OPEN is set.
//   - The session cookie is SameSite=Lax (see setAuthCookie), which is
//     load-bearing here: Lax withholds the cookie from cross-site subresource
//     loads, so an attacker page embedding <img src="https://worklode/blob/…">
//     gets a 401 rather than a probe for what a logged-in victim can see.
func (s *server) eitherGuard(perm Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sub Subject
		if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && token != "" {
			actor, boundTask, err := s.st.Authenticate(r.Context(), token)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					// A token that does not resolve is refused here and
					// never falls through to webSubject: on an instance with
					// LODE_WEB_OPEN set, falling through would serve the blob
					// to a caller whose credential was rejected. Counted and
					// logged like any other denial — the blob route is the
					// one both audiences hit directly, so a token-guessing
					// campaign against it must be visible in
					// worklode_authz_decisions_total. The subject stays
					// authNone — a token that did not resolve authenticates
					// nobody, and logging it as an authenticated denial would
					// let an anonymous caller raise log level at will.
					d := Decision{Reason: "invalid_token"}
					s.observeAuthz(perm, d)
					s.logDenial(r, Subject{Via: authNone}, perm, d)
					writeErr(w, http.StatusUnauthorized, "unauthorized")
					return
				}
				s.mapStoreErr(w, err)
				return
			}
			sub = subjectFromActor(actor, authToken)
			sub.TaskID = boundTask
		} else {
			sub = s.webSubject(r.Context(), r)
		}
		d := Decide(Request{Subject: sub, Permission: perm})
		s.observeAuthz(perm, d)
		if d.Allowed {
			next(w, withSubject(r, sub))
			return
		}
		s.logDenial(r, sub, perm, d)
		if !sub.Authenticated() {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		writeErr(w, http.StatusForbidden, denialMessage(d))
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
