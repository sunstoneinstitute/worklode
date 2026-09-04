// router.go registers the server's routes against one declarative table of
// permissions. The table is the point: a route's guard is stated next to its
// pattern, in one screenful anyone can review, instead of being inferable
// only from how deeply its handler is wrapped.
//
// Two boot-time checks make the table binding rather than advisory, which is
// what turns "we have middleware" into "nothing is unguarded":
//
//   - Registering a pattern the table does not name panics. A new endpoint
//     cannot ship without someone deciding what may reach it — including
//     deciding it is public, which is spelled out with a written reason.
//   - A table entry no route uses fails NewServer. Dead policy is how a table
//     drifts into fiction: an entry nobody enforces reads like a guard.
//
// Neither check can be satisfied by accident, and every existing test that
// boots a server exercises both.
package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// routeGuard is one row of the table: the permission a route requires, plus,
// for public routes only, why it carries no worklode identity. The reason is
// mandatory for permPublic (checked by newRouter) because "this endpoint
// needs no authentication" is a claim that should have to be defended in the
// place someone reads it.
type routeGuard struct {
	perm   Permission
	public string
	// taskScope is what a task-scoped token (001 §2.1, WL-306) may do with
	// this route: nothing (the zero value — default deny), call it when the
	// path's {id} is the bound task (taskScopeBound), or call it freely
	// (taskScopeAny, for the worker surface that carries no task id).
	taskScope taskScope
}

type taskScope int

const (
	taskScopeNone taskScope = iota
	taskScopeBound
	taskScopeAny
)

// guarded is the ordinary case: a route requiring perm.
func guarded(perm Permission) routeGuard { return routeGuard{perm: perm} }

// guardedBound is guarded plus the task-token grant: a task-scoped token may
// call this route when the path's {id} is its bound task.
func guardedBound(perm Permission) routeGuard {
	return routeGuard{perm: perm, taskScope: taskScopeBound}
}

// guardedAny is guarded plus the unbound task-token grant: the worker
// surface a task-scoped token needs that carries no task id in the path —
// reads, follow-up filing, document authoring.
func guardedAny(perm Permission) routeGuard {
	return routeGuard{perm: perm, taskScope: taskScopeAny}
}

// open marks a route as deliberately unauthenticated, with the reason it can
// be. Note this is about the *worklode* identity: the webhook routes below
// authenticate every request by HMAC signature, they just do not carry an
// actor.
func open(why string) routeGuard { return routeGuard{perm: permPublic, public: why} }

// routeGuards names the guard for every route the server serves, keyed by the
// exact ServeMux pattern (method included — GET and POST on one path are
// different capabilities and get different rows).
//
// The /metrics and /healthz routes are absent by construction: they are
// served on the separate admin listener, which is never routed through the
// public ingress and has no authentication middleware at all.
var routeGuards = map[string]routeGuard{
	// --- web UI (spec 032) ---------------------------------------------------
	"GET /{$}":                            guarded(permWebRead),
	"GET /ideas":                          guarded(permWebRead),
	"GET /intake":                         guarded(permWebRead),
	"GET /projects":                       guarded(permWebRead),
	"GET /projects/{id}":                  guarded(permWebRead),
	"GET /projects/{id}/crew":             guarded(permWebRead),
	"POST /projects/{id}/crew":            guarded(permWebWrite),
	"POST /projects/{id}/crew/remove":     guarded(permWebWrite),
	"GET /projects/{id}/work":             guarded(permWebRead),
	"GET /projects/{id}/milestones":       guarded(permWebRead),
	"GET /projects/{id}/deliverables":     guarded(permWebRead),
	"GET /projects/{id}/deliverables/new": guarded(permWebWrite),
	"POST /projects/{id}/deliverables":    guarded(permWebWrite),
	"GET /projects/{id}/tasks/new":        guarded(permWebWrite),
	"POST /projects/{id}/tasks":           guarded(permWebWrite),
	// The cockpit's tombstone review (044 §2) and its two Restore buttons.
	// Reading the page is an ordinary web read; restoring carries the
	// permission the JSON API's undelete carries — permTaskWrite for a task,
	// permDocWrite for a document (044 §5) — rather than permWebWrite, which
	// is why they are two routes and not one.
	"GET /projects/{id}/deleted":                guarded(permWebRead),
	"POST /projects/{id}/deleted/tasks/restore": guarded(permTaskWrite),
	"POST /projects/{id}/deleted/docs/restore":  guarded(permDocWrite),
	"POST /preview":                             guarded(permWebWrite),
	"POST /dictate":                             guarded(permWebWrite),
	"GET /projects/{id}/{section}":              guarded(permWebRead),
	"GET /work":                                 guarded(permWebRead),
	"GET /inbox":                                guarded(permWebRead),
	"GET /reviews":                              guarded(permWebRead),
	"GET /deliveries":                           guarded(permWebRead),
	"GET /knowledge":                            guarded(permWebRead),
	"GET /tasks/{id}":                           guarded(permWebRead),
	"GET /docs":                                 guarded(permWebRead),
	"GET /docs/{id}":                            guarded(permWebRead),
	// Shaped /docs/versions/{id}/{n} rather than /docs/{id}/versions/{n}: the
	// latter is ambiguous with "GET /docs/ref/{ref...}" below when {id}
	// matches the literal "ref" — net/http's ServeMux refuses to register two
	// patterns neither of which is more specific for every path they share,
	// and here it isn't. The JSON API's sibling route below keeps the
	// straightforward /api/v1/docs/{id}/versions/{n} order because there is
	// no /api/v1/docs/ref/{ref...} pattern to collide with.
	"GET /docs/versions/{id}/{n}": guarded(permWebRead),
	"GET /docs/ref/{ref...}":      guarded(permWebRead),
	"GET /drift":                  guarded(permWebRead),
	// The cockpit's one decision act (029 §7.3). permApprovalDecide rather
	// than permWebWrite: deciding an approval is a different capability from
	// filing a task through a form, and the route is additionally gated by
	// requireSession — see authz.go.
	"POST /approvals/{id}/decide": guarded(permApprovalDecide),

	// --- unauthenticated by design ------------------------------------------
	"GET /assets/": open("stylesheet and fonts; no project data, and a " +
		"stylesheet request has no session to attach a login redirect to"),
	"GET /auth/login":             open("starts the login flow"),
	"GET /auth/callback":          open("finishes the login flow"),
	"POST /hooks/github":          open("authenticated by HMAC signature, not by an actor"),
	"POST /hooks/flux":            open("authenticated by HMAC signature, not by an actor"),
	"POST /hooks/catalog":         open("authenticated by HMAC signature, not by an actor"),
	"GET /auth/oidc/config":       open("issuer discovery the CLI needs before it can log in"),
	"POST /auth/oidc/token":       open("exchanges a verified Keycloak ID token for a wl_ token"),
	"GET /.well-known/lode-login": open("login-endpoint discovery, by definition pre-login"),
	"GET /auth/cli/login":         open("starts the server-mediated CLI login flow"),
	"POST /auth/cli/token":        open("redeems a one-time CLI login code"),

	// --- tasks ---------------------------------------------------------------
	"POST /api/v1/tasks":              guardedAny(permTaskWrite),
	"GET /api/v1/tasks":               guardedAny(permTaskRead),
	"GET /api/v1/tasks/{id}":          guardedBound(permTaskRead),
	"GET /api/v1/tasks/{id}/brief":    guardedBound(permTaskRead),
	"GET /api/v1/blockers":            guarded(permTaskRead),
	"GET /api/v1/tasks/{id}/blockers": guardedBound(permTaskRead),
	"GET /api/v1/tasks/{id}/cost":     guardedBound(permTaskRead),
	"GET /api/v1/tasks/{id}/timeline": guardedBound(permTaskRead),
	// A task's blob references (spec 021 §3). Listing is a task read; both
	// halves of the reference graph are task writes.
	"GET /api/v1/tasks/{id}/blobs":           guardedBound(permTaskRead),
	"POST /api/v1/tasks/{id}/blobs":          guardedBound(permTaskWrite),
	"DELETE /api/v1/tasks/{id}/blobs/{hash}": guardedBound(permTaskWrite),
	"PATCH /api/v1/tasks/{id}":               guardedBound(permTaskWrite),
	"PUT /api/v1/tasks/{id}/skills":          guardedBound(permTaskWrite),
	"GET /api/v1/tasks/{id}/checklist":       guardedBound(permTaskRead),
	"POST /api/v1/tasks/{id}/checklist":      guardedBound(permTaskWrite),
	"POST /api/v1/tasks/{id}/edges":          guardedBound(permTaskWrite),
	"DELETE /api/v1/tasks/{id}/edges":        guardedBound(permTaskWrite),
	"POST /api/v1/tasks/{id}/decompose":      guardedBound(permTaskWrite),
	"POST /api/v1/tasks/{id}/state":          guardedBound(permTaskWrite),
	"POST /api/v1/tasks/{id}/abandon":        guardedBound(permTaskWrite),
	"POST /api/v1/tasks/{id}/reopen":         guarded(permTaskWrite),
	// Delete and undelete are task writes like the rest (044 §5). Deliberately
	// not admin-only: a per-role delete permission would be the first of an
	// RBAC model this repo does not have (001 §9.2). What stops a careless
	// delete on a prod instance is the justification 044 §3 demands, not a
	// narrower role.
	"DELETE /api/v1/tasks/{id}":                 guarded(permTaskWrite),
	"POST /api/v1/tasks/{id}/undelete":          guarded(permTaskWrite),
	"POST /api/v1/tasks/claim-next":             guarded(permTaskClaim),
	"POST /api/v1/tasks/{id}/claim":             guardedBound(permTaskClaim),
	"POST /api/v1/tasks/{id}/renew":             guardedBound(permTaskClaim),
	"POST /api/v1/tasks/{id}/release":           guardedBound(permTaskClaim),
	"POST /api/v1/tasks/{id}/start":             guardedBound(permTaskClaim),
	"POST /api/v1/tasks/{id}/stop":              guardedBound(permTaskClaim),
	"POST /api/v1/tasks/{id}/lease/worktree":    guardedBound(permTaskClaim),
	"POST /api/v1/tasks/{id}/agent-session":     guardedBound(permTaskClaim),
	"POST /api/v1/tasks/{id}/agent-session/end": guardedBound(permTaskClaim),
	// Both are actor writes, not task-scoped surfaces: guarded blocks a
	// task-scoped token so claim can't drain instructions leased to a
	// different task the same actor also holds (0016 multi-lease).
	"POST /api/v1/tasks/{id}/instructions": guarded(permTaskWrite),

	// Posing and rewording a decision row (025 §10.1). Not guardedBound: a
	// PATCH may re-parent the row to another task, which a task-scoped
	// token has no business reaching.
	"POST /api/v1/tasks/{id}/decisions":        guarded(permTaskWrite),
	"PATCH /api/v1/tasks/{id}/decisions/{key}": guarded(permTaskWrite),
	"POST /api/v1/instructions/claim":          guarded(permTaskClaim),
	"POST /api/v1/tasks/{id}/assign":           guarded(permTaskAssign),
	"POST /api/v1/tasks/{id}/unassign":         guarded(permTaskAssign),
	"GET /api/v1/board":                        guarded(permTaskRead),

	// --- documents (spec 025 §5, §6, §7) --------------------------------------
	// Reading and writing the corpus is its own capability (see permDocRead in
	// authz.go). The accept routes are permDocWrite like the rest: whether a
	// given actor may accept a given document is the owner gate of 025 §7,
	// a per-document fact the store checks, not a role. Owner transfer
	// (§7.3) is the same shape: guarded rather than guardedAny, matching
	// accept and revision/accept, since it is another deliberate act on the
	// document's identity rather than routine authoring.
	"POST /api/v1/docs":                  guardedAny(permDocWrite),
	"GET /api/v1/docs":                   guardedAny(permDocRead),
	"GET /api/v1/docs/resolve":           guardedAny(permDocRead),
	"GET /api/v1/docs/lint":              guardedAny(permDocRead),
	"GET /api/v1/docs/{id}":              guardedAny(permDocRead),
	"GET /api/v1/docs/{id}/versions":     guardedAny(permDocRead),
	"GET /api/v1/docs/{id}/versions/{n}": guardedAny(permDocRead),
	"PUT /api/v1/docs/{id}/body":         guardedAny(permDocWrite),
	"PUT /api/v1/docs/{id}/edges":        guarded(permDocImport),
	"POST /api/v1/docs/{id}/submit":      guardedAny(permDocWrite),
	"POST /api/v1/docs/{id}/accept":      guarded(permDocWrite),
	"POST /api/v1/docs/{id}/revise":      guardedAny(permDocWrite),
	"POST /api/v1/docs/{id}/owner":       guarded(permDocWrite),
	"POST /api/v1/docs/{id}/notes":       guardedAny(permDocWrite),
	"GET /api/v1/docs/{id}/notes":        guardedAny(permDocRead),
	// Setting the reviewer set (025 §7.3, WL-359) is the same shape as owner
	// transfer: guarded rather than guardedAny, since it too is a deliberate
	// act on the document's identity, gated in the store on the same
	// owner-or-admin authority TransferDocOwner checks.
	"POST /api/v1/docs/{id}/reviewers": guarded(permDocWrite),
	// Asking for review is authoring, not deciding: same permission as
	// submit, and reachable by the task-scoped token an agent writes a
	// document under.
	"POST /api/v1/docs/{id}/request-approval": guardedAny(permDocWrite),
	"PUT /api/v1/docs/{id}/revision":          guardedAny(permDocWrite),
	"DELETE /api/v1/docs/{id}/revision":       guardedAny(permDocWrite),
	"POST /api/v1/docs/{id}/revision/accept":  guarded(permDocWrite),
	// The document half of 044 §5; see the task entries above.
	"DELETE /api/v1/docs/{id}":        guarded(permDocWrite),
	"POST /api/v1/docs/{id}/undelete": guarded(permDocWrite),

	// --- approvals (spec 029 §7) --------------------------------------------
	// Read-only. Deciding is not on the JSON API at all: 029 §7.3 makes it a
	// web-session act, and its route is "POST /approvals/{id}/decide" above.
	"GET /api/v1/approvals": guarded(permApprovalRead),

	// --- skills --------------------------------------------------------------
	"GET /api/v1/skills":                       guardedAny(permSkillRead),
	"GET /api/v1/skills/{name}":                guardedAny(permSkillRead),
	"GET /api/v1/skills/{name}/archive/{hash}": guardedAny(permSkillRead),
	"POST /api/v1/skills/recommend":            guardedAny(permSkillRead),
	"POST /api/v1/skills/sync":                 guarded(permSkillAdmin),

	// --- search ----------------------------------------------------------------
	// guardedAny, not guarded: search is worker surface carrying no task id,
	// like the skills reads above.
	"GET /api/v1/search": guardedAny(permSearchRead),

	// --- runtime -------------------------------------------------------------
	"POST /api/v1/runtime-events": guarded(permRuntimeWrite),

	// --- blobs (spec 021) -----------------------------------------------------
	// The upload is an ordinary API route. The read is an asset route: it is
	// registered with r.asset, which takes either a bearer token or a web
	// session, since a task page's <img> and an agent's fetch both land here
	// (see eitherGuard in authz.go).
	"POST /api/v1/blobs":    guardedAny(permBlobWrite),
	"GET /blob/{hash}":      guardedAny(permBlobRead),
	"POST /api/v1/blobs/gc": guarded(permBlobAdmin),

	// --- secrets (spec 017) ---------------------------------------------------
	// Metadata only — names, purposes and op:// references, never values —
	// but vault and item names describe the org's secret topology, so the
	// route is authenticated like any other.
	"GET /api/v1/secrets/catalog": guardedAny(permSecretRead),
	// permTaskClaim, not permSecretRead: reporting which names were
	// materialized is a step of the claim ceremony, performed by the actor
	// taking the lease, and it writes to the task's audit trail. It belongs
	// with claim/renew/release/start/stop; permission to read the catalog
	// should not imply permission to write task history.
	"POST /api/v1/tasks/{id}/secrets-materialized": guardedBound(permTaskClaim),

	// --- delivery ------------------------------------------------------------
	// Reporting a merge advances tasks, so it needs the same permission the
	// state/abandon endpoints do. The webhook reporter carries no actor and is
	// authenticated by HMAC instead; this one is a person's CLI token.
	"POST /api/v1/merges": guardedAny(permTaskWrite),

	// --- projects, actors, tokens -------------------------------------------
	"GET /api/v1/projects":                              guardedAny(permProjectRead),
	"GET /api/v1/projects/resolve":                      guardedAny(permProjectRead),
	"GET /api/v1/projects/{id}":                         guardedAny(permProjectRead),
	"GET /api/v1/projects/{id}/cockpit":                 guarded(permProjectRead),
	"GET /api/v1/projects/{id}/deliverables":            guarded(permDeliverableRead),
	"POST /api/v1/projects/{id}/deliverables":           guarded(permDeliverableWrite),
	"PATCH /api/v1/deliverables/{id}":                   guarded(permDeliverableWrite),
	"POST /api/v1/projects/{id}/milestones":             guarded(permMilestoneWrite),
	"GET /api/v1/projects/{id}/participants":            guarded(permProjectRead),
	"POST /api/v1/projects/{id}/participants":           guarded(permCrewWrite),
	"DELETE /api/v1/projects/{id}/participants/{actor}": guarded(permCrewWrite),
	// Reporting overhead usage (spec 052 §2) is not a claim on any one task,
	// so every authenticated role may report it — see permProjectReport.
	"POST /api/v1/projects/{id}/session-usage": guarded(permProjectReport),
	"POST /api/v1/projects":                    guarded(permProjectAdmin),
	"PATCH /api/v1/projects/{id}":              guarded(permProjectAdmin),
	"POST /api/v1/projects/{id}/repos":         guarded(permProjectAdmin),
	"PATCH /api/v1/repos/{owner}/{name}":       guarded(permProjectAdmin),
	"DELETE /api/v1/repos/{owner}/{name}":      guarded(permProjectAdmin),
	"POST /api/v1/actors":                      guarded(permActorAdmin),
	"POST /api/v1/tasks/{id}/tokens":           guarded(permTaskToken),
	"POST /api/v1/actors/{id}/tokens":          guarded(permActorAdmin),
	"DELETE /api/v1/tokens":                    guarded(permActorAdmin),

	// --- inbox ---------------------------------------------------------------
	"GET /api/v1/inbox":          guarded(permInboxRead),
	"POST /api/v1/inbox/promote": guarded(permInboxTriage),
	"POST /api/v1/inbox/dismiss": guarded(permInboxTriage),
	"POST /api/v1/inbox/link":    guarded(permInboxTriage),
	"POST /api/v1/inbox/import":  guarded(permInboxAdmin),

	// --- events (spec 025 §15/§18) --------------------------------------------
	"GET /api/v1/events": guarded(permEventRead),
	// A route of its own rather than ?follow=1 on the line above: the table
	// keys on the exact pattern, which is how "the stream is admin, the
	// bounded read is not" stays data instead of becoming a branch inside a
	// handler.
	"GET /api/v1/events/stream":                  guarded(permEventStream),
	"GET /api/v1/event-subscribers":              guarded(permEventRead),
	"GET /api/v1/graph/projection/failures":      guarded(permProjectionRead),
	"POST /api/v1/event-subscribers/{name}/seek": guarded(permEventAdmin),

	// --- identity (spec 013) --------------------------------------------------
	"GET /api/v1/whoami": guardedAny(permWhoAmI),

	// --- reconciliation (spec 013) ---------------------------------------------
	"GET /api/v1/repos/doctor": guarded(permReconcile),
	"POST /api/v1/reconcile":   guarded(permReconcile),

	// --- drift & overview (spec 007) ------------------------------------------
	// One permission for all five reads: they are one screen's worth of the
	// same picture, and an actor who may see the frontier may see what is
	// drifting from it.
	"GET /api/v1/overview":      guarded(permOverviewRead),
	"GET /api/v1/drift":         guarded(permOverviewRead),
	"GET /api/v1/gaps":          guarded(permOverviewRead),
	"GET /api/v1/frontier":      guarded(permOverviewRead),
	"GET /api/v1/critical-path": guarded(permOverviewRead),
	// The one write on this surface, and admin-only; see permDeriveRun.
	"POST /api/v1/derive": guarded(permDeriveRun),
}

// router wires handlers onto a ServeMux through routeGuards, recording which
// entries were used so NewServer can reject a table that has drifted from the
// routes it claims to describe.
type router struct {
	srv *server
	mux *http.ServeMux
	// guards is routeGuards for the real server; a test injects its own so a
	// case about a malformed table never mutates the package-level one.
	guards map[string]routeGuard
	used   map[string]bool
}

func newRouter(s *server, mux *http.ServeMux) *router {
	return newRouterWithGuards(s, mux, routeGuards)
}

func newRouterWithGuards(s *server, mux *http.ServeMux, guards map[string]routeGuard) *router {
	return &router{srv: s, mux: mux, guards: guards, used: make(map[string]bool, len(guards))}
}

// guardFor returns the declared guard for pattern, panicking when the table
// does not name it. A panic and not an error: this is a programming mistake
// discovered at startup with a fixed set of routes, exactly like the pattern
// conflicts ServeMux itself panics on, and every test that builds a server
// runs it.
func (r *router) guardFor(pattern string) routeGuard {
	g, ok := r.guards[pattern]
	if !ok {
		panic(fmt.Sprintf("route %q has no entry in routeGuards: every route "+
			"declares the permission it requires, or open(\"why\") if it needs none", pattern))
	}
	if g.perm == permPublic && g.public == "" {
		panic(fmt.Sprintf("route %q is public with no stated reason", pattern))
	}
	r.used[pattern] = true
	return g
}

// api registers a /api/v1 route: bearer-token authentication, then the policy
// check for the permission the table declares.
func (r *router) api(pattern string, h http.HandlerFunc) {
	g := r.guardFor(pattern)
	r.mux.Handle(pattern, r.srv.auth(r.srv.requirePerm(g.perm, r.srv.requireTaskScope(g, h))))
}

// web registers a web UI route behind webGuard, which resolves the session
// (or the open-deployment subject) and applies the same policy.
func (r *router) web(pattern string, h http.HandlerFunc) {
	g := r.guardFor(pattern)
	r.mux.HandleFunc(pattern, r.srv.webGuard(g.perm, h))
}

// asset registers an asset route behind eitherGuard. An asset route is one
// that serves bytes a *page* references rather than a document a caller
// navigates to — today, spec 021's /blob/{hash}. It is neither api nor web
// because both audiences fetch it directly: an agent with a bearer token, and
// a browser loading an <img> on a task page with its session cookie. Routing
// it through api would 401 every image in the cockpit; routing it through web
// would 401 every agent. eitherGuard accepts both and applies the same
// policy, and answers a refusal in JSON rather than by redirecting a
// subresource fetch into a login page.
func (r *router) asset(pattern string, h http.HandlerFunc) {
	g := r.guardFor(pattern)
	r.mux.HandleFunc(pattern, r.srv.eitherGuard(g.perm, r.srv.requireTaskScope(g, h)))
}

// public registers a route that carries no worklode identity. It still goes
// through the table, so "unauthenticated" is a row someone wrote rather than
// a wrapper someone forgot.
func (r *router) public(pattern string, h http.Handler) {
	g := r.guardFor(pattern)
	if g.perm != permPublic {
		panic(fmt.Sprintf("route %q is registered as public but the table requires %q", pattern, g.perm))
	}
	r.mux.Handle(pattern, h)
}

// publicFunc is public for an http.HandlerFunc.
func (r *router) publicFunc(pattern string, h http.HandlerFunc) { r.public(pattern, h) }

// unusedGuards lists table entries no route claimed, sorted for a stable
// error message. A non-empty result means the table describes a server that
// does not exist.
func (r *router) unusedGuards() []string {
	var unused []string
	for pattern := range r.guards {
		if !r.used[pattern] {
			unused = append(unused, pattern)
		}
	}
	slices.Sort(unused)
	return unused
}

// checkComplete returns an error when the table and the routes disagree.
func (r *router) checkComplete() error {
	if unused := r.unusedGuards(); len(unused) > 0 {
		return fmt.Errorf("routeGuards declares %d route(s) nothing registered: %s",
			len(unused), strings.Join(unused, ", "))
	}
	return nil
}
