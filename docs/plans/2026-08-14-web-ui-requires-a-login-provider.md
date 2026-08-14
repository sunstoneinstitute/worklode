---
status: draft
covers:
  - docs/specs/001-identity-and-authentication.md#sec-6
  - docs/specs/021-images-in-task-bodies.md#sec-4
---
# Web UI requires a login provider implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop an instance with no login provider from serving the cockpit —
and accepting task and deliverable creations — to anyone who can reach the
port, while keeping `docker compose up` and the test suites working through an
explicit, logged opt-in rather than an implicit default.

**Architecture:** The bypass lives in exactly one function,
`(*server).webSubject` (`internal/api/authz.go:329`), which returns the
anonymous `openSubject()` whenever `s.oidc == nil`. That condition becomes
`s.oidc == nil && s.cfg.WebOpen`, with a new `Config.WebOpen bool`
(`LODE_WEB_OPEN`) defaulting to false. With neither a provider nor the opt-in,
`webGuard` renders a 503 naming the missing configuration instead of
redirecting to a login route that 404s without OIDC. Nothing else in the
request path changes: the permission table, the route table, `Decide`, and the
API's bearer-token path are all untouched. The cost of the change is almost
entirely in the ~40 existing web tests and the e2e suite, which drive pages
anonymously and must now say so.

**Who is exposed today:** the `hzdev` and `hzprod` overlays already patch in
`LODE_OIDC_ISSUER` and `LODE_OIDC_CLIENT_ID`
(`deploy/overlays/hzdev/kustomization.yaml:30,33`,
`deploy/overlays/hzprod/kustomization.yaml:27,30`), so the deployed clusters
are session-gated and are not the hazard. The hazard is every instance stood
up without those — the compose stack, CI, and any new overlay someone copies
without the patch. That is what makes a safe *default* the fix rather than a
documentation note.

**Tech Stack:** Go 1.26, `net/http` mux, `internal/api`'s router/authz layer,
Prometheus client, `templ`-rendered pages. `internal/api` tests need Postgres
with pgvector (`docker-compose.yml`); they skip silently without it.

**Read first:**
- `docs/specs/001-identity-and-authentication.md` §6 — the sentence this plan
  changes: "When OIDC is unconfigured the UI stays open as today."
- `docs/specs/021-images-in-task-bodies.md` §4 (Auth) and §14 (Q021.4) — the
  mirror of the same bypass, and the open question this closes
- `internal/api/authz.go:297-346` — `webGuard` and `webSubject`
- `internal/api/router.go:52-81` — `routeGuards`; every `guarded(permWeb*)`
  row is affected, every `open(...)` row is not
- `internal/api/authz_test.go:227` — `TestOpenDeploymentStaysOpen`, the test
  that asserts today's behaviour and becomes the opt-in test
- `internal/api/server_test.go:28` — `newTestServer`, the helper ~40 web tests
  build their server through

## Global Constraints

- **One gate, one place.** The open-mode decision stays inside `webSubject` /
  `webGuard`. Do not add a `WebOpen` check to a handler, to `routeGuards`, or
  to `Decide` — `Decide` is a pure function of subject and permission and must
  stay that way.
- **The API is out of scope.** `/api/v1` has always required a bearer token
  and is unaffected. So are the `open(...)` routes: `/assets/`, the auth
  routes, and the HMAC-signed webhooks must keep serving on a closed instance,
  or the login flow cannot complete.
- **`LODE_WEB_OPEN` must be visible at boot.** An instance running open is a
  deliberate operator choice; `NewServer` logs it at Warn so it appears in
  every startup log rather than only in a config file nobody rereads.
- **Never grant `RoleAdmin` to the open subject.** `openSubject()` returns
  `RoleUser` only, and that is load-bearing (`authz.go:156-161`). This plan
  does not widen it.
- **Metrics** (spec 022): the refusal is a new outcome on an existing
  instrument, not a new one. Keep the label set bounded.
- **Commit format:** describe the defect and the fix, not the plan file.
  Never add `Co-authored-by:` trailers.

---

## Decision made by this plan, and the alternative it rejects

Spec 021 §14 leaves two options open: "either require a provider before
serving any web surface, or gate the whole UI behind a default-deny". They
collapse to the same code — `webGuard` already default-denies, and with no
provider every subject is anonymous, so "gate default-deny" *is* "refuse to
serve". The real question is what happens to local development, because
`docker-compose.yml` configures no OIDC (`docker-compose.yml:38-52`) and the
cockpit on `localhost:8080` is the documented dev loop.

This plan takes **refuse by default, with an explicit opt-in**
(`LODE_WEB_OPEN`) rather than refuse unconditionally, because:

- an unconditional refusal makes the local stack unusable and would be worked
  around by running a throwaway Keycloak, which nobody will do;
- the exposure the follow-up describes is *accidental* openness. An operator
  who sets `LODE_WEB_OPEN=1` on a public deployment has been told; an operator
  who simply has not configured OIDC yet has not.

Whoever accepts this plan can instead choose **unconditional refusal**: drop
`Config.WebOpen`, have `webGuard` refuse whenever `s.oidc == nil`, and set
`LODE_OIDC_*` in `docker-compose.yml` against a Keycloak service. That is a
larger change to the compose stack and to `e2e/`, and Task 1 and Task 2 would
both need rewriting. Decide before starting.

---

## Task 1: `webGuard` refuses to serve without a provider or an opt-in

`webSubject` returns `openSubject()` on `s.oidc == nil` alone, so an instance
with no OIDC configuration serves `/`, `/work`, every `/projects/{id}` page
and every `/tasks/{id}` page to anyone who can reach it — and, because the
creation forms sit behind the same guard, accepts a task or a deliverable from
them too, recorded with a NULL `created_by`. Add the opt-in and make its
absence a refusal.

**Files:**
- Modify: `internal/api/server.go` (add `Config.WebOpen`, boot warning)
- Modify: `internal/api/authz.go` (`webSubject`, `webGuard`, `openSubject`
  doc comment)
- Modify: `internal/api/metrics.go:206` (`observeAuthz` — no signature change;
  see Step 5)
- Modify: `internal/api/server_test.go:28` (`newTestServer` opts in)
- Test: `internal/api/authz_test.go`

**Interfaces:**
- Consumes: `Decide`, `openSubject`, `webErr`, `s.observeAuthz` (all existing)
- Produces:
  - `api.Config.WebOpen bool` — when true *and* no login provider is
    configured, web routes serve the anonymous subject. Ignored when OIDC is
    configured. Task 2 sets it from `LODE_WEB_OPEN`; Task 3 documents it.
  - `(*server).webOpen() bool` — package-private, `s.oidc == nil &&
    s.cfg.WebOpen`. The single predicate; nothing else reads `cfg.WebOpen`.

- [ ] **Step 1: Write the failing tests**

In `internal/api/authz_test.go`, replace `TestOpenDeploymentStaysOpen`
(`:227`) with the opt-in version and add the refusal cases. The existing test
asserts precisely the behaviour this task removes, so it changes rather than
survives — its `created_by` assertion is still worth keeping under the opt-in.

```go
// TestOpenDeploymentRequiresOptIn: with no login provider and no explicit
// LODE_WEB_OPEN, the cockpit refuses to serve rather than serving the
// anonymous subject. A 503 and not a 302: there is no login route to redirect
// to when OIDC is unconfigured, so a redirect would be a lie.
func TestOpenDeploymentRequiresOptIn(t *testing.T) {
	st, h, _ := newTestServerWithConfig(t, api.Config{})
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/projects/proj", "", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("cockpit with no provider and no opt-in = %d, want 503", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "LODE_WEB_OPEN") {
		t.Errorf("refusal page does not name the setting that would allow it:\n%s", rr.Body.String())
	}
}

// TestOpenDeploymentRefusesWrites: the creation forms sit behind the same
// guard, so a closed instance must not accept an unattributable task either.
func TestOpenDeploymentRefusesWrites(t *testing.T) {
	st, h, _ := newTestServerWithConfig(t, api.Config{})
	createProject(t, st, "proj")

	form := url.Values{"title": {"Anonymous"}, "priority": {"low"}, "kind": {"chore"}}
	rr := withSession(t, h, "POST", "/projects/proj/tasks", "", form.Encode())
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("form submit with no provider and no opt-in = %d, want 503", rr.Code)
	}
	if _, err := st.GetTask(context.Background(), "WL-1"); err == nil {
		t.Fatal("a task was created by an anonymous caller on a closed instance")
	}
}

// TestOpenDeploymentOptedIn: LODE_WEB_OPEN restores the old behaviour
// deliberately — the cockpit serves and accepts writes, attributed to nobody
// rather than to a fabricated actor.
func TestOpenDeploymentOptedIn(t *testing.T) {
	st, h, _ := newTestServerWithConfig(t, api.Config{WebOpen: true})
	createProject(t, st, "proj")

	if rr := doReq(t, h, "GET", "/projects/proj", "", nil); rr.Code != http.StatusOK {
		t.Fatalf("cockpit on an opted-in open deployment = %d, want 200", rr.Code)
	}
	form := url.Values{"title": {"Anonymous"}, "priority": {"low"}, "kind": {"chore"}}
	rr := withSession(t, h, "POST", "/projects/proj/tasks", "", form.Encode())
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("form submit = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	task, err := st.GetTask(context.Background(), "WL-1")
	if err != nil {
		t.Fatalf("get created task: %v", err)
	}
	if task.CreatedBy != "" {
		t.Errorf("created_by = %q, want empty — an open deployment has no actor to attribute to", task.CreatedBy)
	}
}

// TestPublicRoutesServeOnAClosedInstance: the login flow and the stylesheet
// must survive the refusal, or a closed instance cannot be logged into and
// renders unstyled error pages.
func TestPublicRoutesServeOnAClosedInstance(t *testing.T) {
	_, h, _ := newTestServerWithConfig(t, api.Config{})
	if rr := doReq(t, h, "GET", "/assets/app.css", "", nil); rr.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.css on a closed instance = %d, want 200", rr.Code)
	}
}

// TestWebOpenIgnoredWhenProviderConfigured: the opt-in is about the *absence*
// of a provider. With OIDC configured it must not weaken session enforcement.
func TestWebOpenIgnoredWhenProviderConfigured(t *testing.T) {
	_, h := newOIDCServer(t, api.Config{WebOpen: true})
	rr := doReq(t, h, "GET", "/projects/proj", "", nil)
	if rr.Code != http.StatusFound {
		t.Fatalf("anonymous read with OIDC configured and WebOpen set = %d, want 302 to login", rr.Code)
	}
}
```

`newTestServerWithConfig` already exists at `internal/api/skills_test.go:39` —
use it, do not add a second helper. For the last test, reuse whatever
`TestWebGuardRedirectsAnonymous` (`authz_test.go:181`) already does to build a
server with OIDC configured — that is `newOIDCServer`, shared with
`oidcauth_test.go` and `oidcweb_test.go`. If it takes no `Config` argument,
extend it to take one rather than duplicating it, and update its other call
sites to pass `api.Config{}`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/api -run 'TestOpenDeployment|TestPublicRoutesServe|TestWebOpenIgnored' -v`
Expected: FAIL — `unknown field WebOpen in struct literal`, then (once the
field exists) 200s where 503s are wanted.

If the run reports `SKIP`, Postgres is not reachable. Start it
(`docker compose up -d postgres`) — a skipped run proves nothing here.

- [ ] **Step 3: Add the config field**

In `internal/api/server.go`, immediately after the `SessionSecret` line in the
OIDC block (`:55`):

```go
	// WebOpen (LODE_WEB_OPEN) permits the web UI to serve anonymous callers on
	// an instance with *no* login provider configured — the local stack and
	// CI. It is ignored when OIDC is configured: the opt-in is about the
	// absence of a provider, never about weakening one that exists. Without
	// it, an instance with no provider refuses to serve any web page rather
	// than serving the whole cockpit to anyone who can reach the port.
	WebOpen bool // LODE_WEB_OPEN
```

- [ ] **Step 4: Gate the bypass**

In `internal/api/authz.go`, add the predicate next to `webSubject`:

```go
// webOpen reports whether this instance deliberately serves the web UI to
// anonymous callers: no login provider is configured *and* the operator asked
// for it. Both halves matter — the first makes it the only way to serve a
// page at all, the second makes it a decision rather than an oversight.
func (s *server) webOpen() bool {
	return s.oidc == nil && s.cfg.WebOpen
}
```

and change `webSubject`'s first branch (`:329-332`) from

```go
	if s.oidc == nil {
		return openSubject()
	}
```

to

```go
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
```

Then, in `webGuard` (`:306-322`), make the refusal explicit before the
redirect. Replace the denial tail:

```go
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
```

Finally update the file header comment (`authz.go:32-34`), which currently
promises the opposite:

```go
// project-scoped roles, no ownership checks, and no delegation. Decide takes
// the resource it would need for those (see Request.Resource) so adding them
// later does not change any call site, but today it is ignored. A deployment
// with no login provider serves no web surface at all unless it sets
// LODE_WEB_OPEN, and that bypass is a single named decision (authOpen) that is
// counted and logged rather than an implicit passthrough.
```

and `openSubject`'s comment (`:199-200`) to say it is reached only under
`webOpen()`.

- [ ] **Step 5: Confirm the refusal is counted**

No metrics change is needed: `webGuard` already calls `s.observeAuthz(perm, d)`
before the denial tail, so a refused page increments
`worklode_authz_decisions_total{permission="web.read",outcome="deny"}`.
Verify by reading `internal/api/metrics.go:206-215` — if the counter were
behind the redirect rather than before it, the refusal would be invisible and
would need moving. Do not add a second instrument.

- [ ] **Step 6: Warn at boot when running open**

In `internal/api/server.go`, after the OIDC and GitHub configuration blocks
(`:258`, just past `s.oidc = v`'s enclosing `if`):

```go
	if s.oidc == nil && cfg.WebOpen {
		s.log.Warn("web UI is serving unauthenticated: no login provider is " +
			"configured and LODE_WEB_OPEN is set; every page and every " +
			"creation form is reachable by anyone who can reach this port")
	}
```

- [ ] **Step 7: Opt the test helper in**

In `internal/api/server_test.go:28`, `newTestServer` builds its server with a
config that has no OIDC, so every web test it backs would now get a 503. Set
the opt-in there:

```go
	// Web tests drive pages anonymously; they are the deliberate open case.
	// The refusal itself is tested through newTestServerWithConfig in
	// authz_test.go, which does not go through this helper.
	handler, admin, err := api.NewServer(st, api.Config{BootstrapToken: token, WebOpen: true})
```

Match the helper's actual current `api.Config` literal — add the field, do not
retype the struct. Check whether `newTestServerAdmin` (`:48`) and
`newTestServerWithConfig` (`skills_test.go:39`) build their own literals; the
first needs the same addition, the second must **not** (it is how the refusal
tests get a closed instance).

- [ ] **Step 8: Run the whole api package**

Run: `go test ./internal/api/... -v`
Expected: PASS. Any web test still failing with 503 is one that builds its own
`api.Config` — add `WebOpen: true` to it, and never by weakening a new
assertion.

- [ ] **Step 9: Build and vet**

Run: `go build ./... && gofmt -l internal/ && go vet ./...`
Expected: PASS, `gofmt -l` silent.

- [ ] **Step 10: Commit**

```bash
git add internal/api/authz.go internal/api/authz_test.go \
        internal/api/server.go internal/api/server_test.go
git commit -m "Refuse to serve the web UI without a login provider

webSubject returned the anonymous open subject whenever OIDC was
unconfigured, so an instance with no login provider served the whole cockpit
-- and accepted tasks and deliverables through the creation forms -- to
anyone who could reach the port. Gate that on an explicit LODE_WEB_OPEN, warn
at boot when it is set, and refuse with a 503 naming the missing
configuration when it is not."
```

---

## Task 2: Wire `LODE_WEB_OPEN` through serve, compose, and e2e

The flag exists but nothing sets it, so `lode serve` and `docker compose up`
now refuse to serve the cockpit. This task makes the documented local loop
work again and fixes the e2e suite, which drives web pages anonymously through
its own `api.Config` literals.

**Files:**
- Modify: `internal/cmd/serve.go:78-100` (env → `Config.WebOpen`)
- Modify: `docker-compose.yml:38-52` (server service env)
- Modify: `e2e/cockpit_test.go:119`, `e2e/create_test.go:60`,
  `e2e/smoke_test.go:153`, and any other `api.NewServer` call in `e2e/` whose
  test fetches a web page
- Test: `internal/cmd` has no server test; the e2e suite is the proof

**Interfaces:**
- Consumes: `api.Config.WebOpen` (Task 1)
- Produces: `LODE_WEB_OPEN` as a documented server environment variable.
  Task 3 documents it in the spec and the README.

- [ ] **Step 1: Parse the env var in `serve.go`**

`api.Config` takes raw strings from the environment and parses inside
`NewServer` for most fields, but `ClusterEnvMap` is already parsed in
`serve.go` before the struct literal. Follow that, so `Config.WebOpen` stays a
`bool` and tests set it as one. Above the `api.NewServer` call:

```go
			// LODE_WEB_OPEN serves the web UI unauthenticated on an instance
			// with no login provider (the local stack, CI). An unparseable
			// value is a typo in a security-relevant setting, so it fails the
			// boot rather than defaulting quietly to either answer.
			webOpen := false
			if v := os.Getenv("LODE_WEB_OPEN"); v != "" {
				b, err := strconv.ParseBool(v)
				if err != nil {
					return fmt.Errorf("LODE_WEB_OPEN: %q is not a boolean", v)
				}
				webOpen = b
			}
```

and add `WebOpen: webOpen,` to the `api.Config` literal, next to
`SessionSecret`. Add `strconv` to the imports if it is not already there;
`fmt` is.

- [ ] **Step 2: Verify the binary refuses and permits**

```bash
go build -o /tmp/lode ./cmd/lode
LODE_WEB_OPEN=maybe LODE_DSN=postgres://localhost/x /tmp/lode serve
```

Expected: exits with `LODE_WEB_OPEN: "maybe" is not a boolean`, before any
database connection is attempted. If it reaches a DSN error first, the check
is below the store open and should move above it.

- [ ] **Step 3: Set it in the local stack**

In `docker-compose.yml`, in the server service's `environment` block, after
`LODE_BOOTSTRAP_TOKEN`:

```yaml
      # The local stack configures no OIDC, so the cockpit would otherwise
      # refuse to serve. Compose is a trusted-network deployment by
      # definition; a real one configures LODE_OIDC_* instead.
      LODE_WEB_OPEN: ${LODE_WEB_OPEN:-1}
```

The `${VAR:-1}` form keeps it overridable from the host, matching the comment
already at `docker-compose.yml:40`.

The port comment at `docker-compose.yml:29-34` explains the loopback binding
with "the web board has no auth (bearer tokens cover /api/v1 only)". That is
now a consequence of this setting rather than an unconditional fact — extend
it to say the board has no auth *because* `LODE_WEB_OPEN` is set here, and
that exposing the port means configuring OIDC or clearing the flag.

- [ ] **Step 4: Fix the e2e servers**

Every `api.NewServer(st, api.Config{...})` in `e2e/` whose test fetches a page
(`getPage`, `postForm`) needs `WebOpen: true`. Find them:

```bash
grep -rn "api.Config{" e2e/
grep -rln "getPage\|postForm" e2e/
```

Add the field to the config in each file the second command lists. Do not add
it to a file that only drives `/api/v1` with a bearer token — those are
unaffected, and adding it there would suggest they depend on it.

- [ ] **Step 5: Run the e2e suite**

Run: `go test -race -count=1 -tags e2e ./e2e/`
Expected: PASS. A 503 in a cockpit or create test means that file's server
config was missed.

- [ ] **Step 6: Prove the compose stack still serves**

```bash
export LODE_BOOTSTRAP_TOKEN=wl_$(openssl rand -hex 20)
docker compose up -d --build
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/
docker compose logs server | grep -i "serving unauthenticated"
```

Expected: `200`, and the boot warning from Task 1 Step 6 present in the logs.
Then check the refusal path:

```bash
LODE_WEB_OPEN=0 docker compose up -d server
curl -s localhost:8080/ | head -c 200
```

Expected: a 503 page naming `LODE_WEB_OPEN`. Restore with `docker compose up -d`.

- [ ] **Step 7: Full build and test**

```bash
go build ./... && gofmt -l internal/ && go vet ./...
go test ./... && go test -race -count=1 -tags e2e ./e2e/
```

Expected: PASS throughout.

- [ ] **Step 8: Commit**

```bash
git add internal/cmd/serve.go docker-compose.yml e2e/
git commit -m "Set LODE_WEB_OPEN for the local stack and the e2e servers

The web UI now refuses to serve without a login provider, which the compose
stack and the e2e suite both are. Parse LODE_WEB_OPEN in serve (rejecting a
non-boolean rather than defaulting quietly), default the compose server to
open, and opt the e2e servers that drive web pages in."
```

---

## Task 3: Amend the specs and close Q021.4

Spec 001 §6 says "When OIDC is unconfigured the UI stays open as today" and
spec 021 §4 mirrors it, both on `accepted` documents. Spec 021 §15's third
acceptance criterion asserts the old blob behaviour explicitly. Leaving them
is worse than the original defect: the code would now contradict two accepted
specs, and the next reader would trust the specs.

**Files:**
- Modify: `docs/specs/001-identity-and-authentication.md` §6, §13
- Modify: `docs/specs/021-images-in-task-bodies.md` §4, §14, §15
- Modify: `CLAUDE.md` (the `internal/api` architecture paragraph)
- Modify: `README.md` (the quickstart env, if it lists server env vars)
- Modify: `docs/follow-ups.md` (the `[P0]` entry becomes a pointer at this plan)

**Interfaces:** none — documentation only. No Go code changes in this task.

- [ ] **Step 1: Amend spec 001 §6**

001 is `accepted`, so the anchor is frozen but the prose is not. Replace the
last sentence of the opening paragraph (`001-identity-and-authentication.md:113-115`):

```markdown
When OIDC is unconfigured the web routes refuse to serve at all — a 503 naming
the missing configuration — unless `LODE_WEB_OPEN` is set, which serves them
to anonymous callers on a trusted network. The opt-in is ignored when OIDC is
configured: it is about the absence of a provider, never about weakening one.
```

- [ ] **Step 2: Record the setting where the other server config lives**

001 §5 (`Server OIDC configuration and verification`) enumerates the server's
OIDC environment variables. Add `LODE_WEB_OPEN` there with one line saying it
is the deliberate no-provider opt-in and that it is ignored when OIDC is on.
Read §5 first and match its existing list formatting.

- [ ] **Step 3: Amend spec 021 §4 and retire Q021.4**

In §4 (`021-images-in-task-bodies.md:178-186`), the paragraph beginning "The
consequence is worth stating plainly" no longer holds. Replace it:

```markdown
The consequence follows the UI's auth model rather than restating it: an
install with no web auth provider serves no web surface at all unless it sets
`LODE_WEB_OPEN`, and the blob route inherits exactly that — refused on a
closed instance, open on one that opted in. The blob route is still not the
place to tighten the auth model unilaterally; it is now inheriting a model
that is closed by default.
```

In §14, replace the Q021.4 body with its resolution, keeping the question's
identifier so an inbound reference still lands:

```markdown
- **Q021.4 — The web UI is unauthenticated without an SSO provider.**
  *Resolved 2026-08-14.* The web surface now refuses to serve without a login
  provider unless `LODE_WEB_OPEN` is set (001 §6), and §4's mirroring means
  blobs inherit the closed default. Nothing in this spec changed.
```

- [ ] **Step 4: Fix spec 021 §15's third acceptance criterion**

`021-images-in-task-bodies.md:191-195` says "with no provider configured it
passes through, matching `webAuth`". Two things are stale — `webAuth` was
renamed `webGuard`, and the pass-through is now conditional:

```markdown
3. `GET /blob/{hash}` 302s to a presigned URL for both a bearer token and a
   web session. With a web auth provider configured it `401`s with neither;
   with no provider configured it refuses with a `503` unless `LODE_WEB_OPEN`
   is set, matching `webGuard`. The presigned response carries the sniffed
   `Content-Type`, a correct `Content-Length`, and `Content-Disposition:
   attachment` for a non-embeddable type.
```

The blob route is unimplemented (no `/blob/` row in `routeGuards`), so this is
a promise about future code, not a claim about shipped code. Whoever
implements spec 021 must route it through `webGuard` for this to hold.

- [ ] **Step 5: Run the doc checks**

```bash
./scripts/secfmt.py -l
./scripts/secmeta.py
```

Expected: both silent. They report and never rewrite, so a complaint is yours
to fix by hand. `secfmt.py` will refuse if a section number moved — none
should have; only prose changed.

- [ ] **Step 6: Update CLAUDE.md**

The `internal/api` line in the Architecture section says "development mode
remains open when no provider is configured". Replace that clause with
"development mode is open only when `LODE_WEB_OPEN` is set and no provider is
configured". Keep the sentence's shape — this is one clause, not a rewrite.

- [ ] **Step 7: Update the README's "Network exposure" section**

`docker-compose.yml:29-34` points readers at README "Network exposure", which
is where the unauthenticated board is explained today. Say there that the
board is unauthenticated *because compose sets* `LODE_WEB_OPEN=1`, that a
server without it and without OIDC refuses to serve web pages, and that
exposing the port means configuring `LODE_OIDC_*` rather than opening the
binding. Compose sets the flag already (Task 2), so a reader following the
quickstart verbatim needs no new step — say that, so nobody adds an export
that is already handled.

- [ ] **Step 8: Point the follow-up entry at this plan**

In `docs/follow-ups.md`, replace the `[P0]` **The web UI is unauthenticated
without an SSO provider** entry with:

```markdown
- `[P0]` **The web UI is unauthenticated without an SSO provider.** Planned in
  `docs/plans/2026-08-14-web-ui-requires-a-login-provider.md`.
```

Once every task here is landed and verified, strike the entry entirely instead
— an item whose work is done reads as unfixed while it is still listed.

- [ ] **Step 9: Commit**

```bash
git add docs/specs/001-identity-and-authentication.md \
        docs/specs/021-images-in-task-bodies.md \
        CLAUDE.md README.md docs/follow-ups.md
git commit -m "Record the closed-by-default web UI in specs 001 and 021

001 §6 and 021 §4 both stated that the UI stays open when OIDC is
unconfigured, which the code no longer does, and 021 §15's third acceptance
criterion asserted the old blob pass-through against a middleware that has
since been renamed. Amend all three and resolve Q021.4."
```

---

## Verification

```bash
docker compose up -d
go build ./... && gofmt -l internal/ && go vet ./...
go test ./... && go test -race -count=1 -tags e2e ./e2e/
./scripts/secfmt.py -l && ./scripts/secmeta.py
```

Then the behaviour itself, which no unit test covers end to end:

```bash
# closed: refuses, naming the setting
docker compose run --rm -e LODE_WEB_OPEN=0 -p 8081:8080 -d server
curl -s localhost:8081/ | grep -o LODE_WEB_OPEN
# open: serves, and says so in the log
docker compose logs server | grep -i "serving unauthenticated"
```

`internal/api` and `internal/store` tests skip silently when Postgres is
unreachable. State explicitly, when reporting this work, whether Postgres was
actually up — a green suite without it proves almost nothing about a change
whose tests all build a server against a database.

## Follow-ups this plan deliberately does not close

- **Authorization is still a seam, not a model.** Roles stay global, there is
  no ownership rule, and `Decide` still ignores `Request.Resource`. That is the
  `[gated]` follow-up of the same name, waiting on spec 029 §6's Crew.
- **The open subject is still unattributable.** A write on an opted-in open
  instance records a NULL `created_by`, as it always has. Making the open case
  attributable would mean inventing an actor, which is worse than a null.
- **`/metrics` and `/healthz`** are untouched: they are served on the separate
  admin listener with no authentication at all, by construction
  (`router.go:49-51`).
- **The blob route** (spec 021) is unimplemented. Task 3 fixes what its spec
  promises; routing it through `webGuard` is the implementer's job.
