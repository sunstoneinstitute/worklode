---
implements: docs/specs/023-keycloak-primary-auth.md
---
# Keycloak-Primary Auth 1 — Keycloak is the only login

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement spec 023 §A and §B: delete the GitHub web-login provider, the provider chooser, and org/team role derivation, so Keycloak is the sole login; and carry a Keycloak-asserted `github_username` claim onto the actor as `expected_github_login`, re-synced on every login.

**Architecture:** GitHub stops being an identity provider and becomes a link-only OAuth client. `internal/githubauth.Client` keeps `AuthCodeURL`/`Exchange`/`FetchIdentity` (plan 2 reuses them for the link flow) and loses `Roles`, the org/team membership reads, and the `Org`/`AdminTeam` fields. `/auth/github/*` routes disappear entirely in this plan; plan 2 re-adds `/auth/github/link` and `/auth/github/callback` as authenticated link routes. The expectation column exists so the link flow can strict-match long after login, when no Keycloak token is in hand.

**Tech Stack:** Go 1.25+, Postgres (golang-migrate `*.up.sql`/`*.down.sql`), net/http `ServeMux`, `internal/oidc/oidctest` fake issuer.

**Read first:** `docs/specs/023-keycloak-primary-auth.md` §3.1–§3.2 and §3.6, `internal/api/githubweb.go` (everything deleted here), `internal/api/oidcweb.go:57-71` (`webAuth`, `loginTarget`'s caller), `internal/api/oidcauth.go:37-55` (`provisionActor`), `internal/store/actors.go:49-80`.

**Conventions:**
- Run `go test ./internal/...`. **Store and API tests need Postgres with pgvector**; default DSN `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`, override with `TEST_POSTGRES_DSN`.
- Commit after every task, imperative mood, **no** `Co-authored-by:` or any other trailer.
- New migrations must also be listed in `deploy/base/kustomization.yaml`. If `./scripts/check-migrations.sh` renumbers `0010`, use the number it assigns.

---

## File structure

| File | Responsibility |
|---|---|
| `deploy/base/migrations/0010_actor_github_expectation.{up,down}.sql` (new) | `actors.expected_github_login` |
| `internal/store/actors.go` | `Actor.ExpectedGitHubLogin`; `UpsertHumanActor` writes it |
| `internal/oidc/oidc.go` | `Claims.GitHubUsername` |
| `internal/api/oidcauth.go` | `provisionActor` passes the claim through |
| `internal/api/githubweb.go` | **deleted** |
| `internal/api/githubweb_test.go` | **deleted** |
| `internal/api/oidcweb.go` | `webAuth` gates on OIDC alone; `loginTarget` moves here |
| `internal/api/cliauth.go` | CLI login discovery gates on OIDC alone; providers = `["keycloak"]` |
| `internal/api/server.go` | Drop chooser/GitHub-login routes, `GitHubOrg`/`GitHubAdminTeam` config |
| `internal/githubauth/githubauth.go` | Drop `Org`, `AdminTeam`, `Roles`, `activeMembership` |
| `internal/cmd/serve.go`, `deploy/overlays/hzdev/kustomization.yaml` | Config removal |

---

## Task 1: `expected_github_login` on actors

**Files:**
- Create: `deploy/base/migrations/0010_actor_github_expectation.up.sql`, `…down.sql`
- Modify: `deploy/base/kustomization.yaml`, `internal/store/actors.go`
- Test: `internal/store/actors_test.go`

- [ ] **Step 1: Write the migration**

`deploy/base/migrations/0010_actor_github_expectation.up.sql`:

```sql
-- The GitHub login Keycloak asserts for this actor (spec 023 §3.2). Recorded at
-- login so the link flow can strict-match later, when no Keycloak token is in
-- hand. NULL means the Keycloak account carries no github_username attribute.
ALTER TABLE actors ADD COLUMN expected_github_login text;
```

`deploy/base/migrations/0010_actor_github_expectation.down.sql`:

```sql
ALTER TABLE actors DROP COLUMN expected_github_login;
```

Add both filenames to the `worklode-migrations` file list in `deploy/base/kustomization.yaml`, after the `0009_task_kinds` pair.

- [ ] **Step 2: Write the failing test**

Append to `internal/store/actors_test.go` (match the file's existing store-opening helper — `openTestStore(t)`):

```go
func TestUpsertHumanActorSyncsGitHubExpectation(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()

	if err := s.UpsertHumanActor(ctx, "stig", "Stig", false, "stigsb"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	a, err := s.GetActor(ctx, "stig")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.ExpectedGitHubLogin != "stigsb" {
		t.Fatalf("expected_github_login = %q, want stigsb", a.ExpectedGitHubLogin)
	}

	// Re-synced on every login exactly like the admin flag: a cleared Keycloak
	// attribute must clear the expectation, not leave a stale one behind.
	if err := s.UpsertHumanActor(ctx, "stig", "Stig", true, ""); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	a, err = s.GetActor(ctx, "stig")
	if err != nil {
		t.Fatalf("get actor after re-sync: %v", err)
	}
	if a.ExpectedGitHubLogin != "" || !a.Admin {
		t.Fatalf("actor = %+v, want cleared expectation and admin=true", a)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestUpsertHumanActorSyncsGitHubExpectation -v`
Expected: FAIL — too many arguments to `UpsertHumanActor`, and `a.ExpectedGitHubLogin` undefined.

- [ ] **Step 4: Write the implementation**

In `internal/store/actors.go`, add the field to `Actor`:

```go
type Actor struct {
	ID          string
	Kind        string
	DisplayName string
	Admin       bool
	// ExpectedGitHubLogin is the github_username Keycloak asserts for this
	// human, empty when the attribute is absent. The GitHub link flow refuses
	// any link whose GitHub login does not match it.
	ExpectedGitHubLogin string
}
```

Replace `UpsertHumanActor`:

```go
// UpsertHumanActor inserts a human actor, or on repeat login updates its
// display name, admin flag, and expected GitHub login. All three are re-synced
// on every login, so revoking a Keycloak group or clearing the github_username
// attribute takes effect at the actor's next sign-in. An empty
// expectedGitHubLogin is stored as NULL.
func (s *Store) UpsertHumanActor(ctx context.Context, id, displayName string, admin bool, expectedGitHubLogin string) error {
	var gh any
	if expectedGitHubLogin != "" {
		gh = expectedGitHubLogin
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO actors (id, kind, display_name, admin, expected_github_login)
		 VALUES ($1, 'human', $2, $3, $4)
		 ON CONFLICT (id) DO UPDATE SET display_name = excluded.display_name,
		   admin = excluded.admin, expected_github_login = excluded.expected_github_login`,
		id, displayName, admin, gh,
	)
	if err != nil {
		return fmt.Errorf("upsert human actor %s: %w", id, err)
	}
	return nil
}
```

In `GetActor`, widen the query and scan:

```go
	var a Actor
	var displayName, ghLogin sql.NullString
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kind, display_name, admin, expected_github_login FROM actors WHERE id = $1`, id)
	if err := row.Scan(&a.ID, &a.Kind, &displayName, &a.Admin, &ghLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get actor %s: %w", id, err)
	}
	a.DisplayName = displayName.String
	a.ExpectedGitHubLogin = ghLogin.String
	return &a, nil
```

- [ ] **Step 5: Fix the other callers**

`internal/api/oidcauth.go:51` and `internal/api/githubweb.go:73` each gain a final argument — pass `""` at both for now (Task 2 fills in the real value at the OIDC call site; `githubweb.go` is deleted in Task 4). Update the five `UpsertHumanActor` call sites in `_test.go` files the same way: `grep -rn "UpsertHumanActor" --include=*_test.go .` and append `, ""`.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/store/ ./internal/api/ 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add deploy/base/migrations/0010_actor_github_expectation.up.sql \
  deploy/base/migrations/0010_actor_github_expectation.down.sql \
  deploy/base/kustomization.yaml internal/store/actors.go internal/store/actors_test.go \
  internal/api/oidcauth.go internal/api/githubweb.go
git commit -m "Record the GitHub login Keycloak asserts for an actor"
```

---

## Task 2: Carry `github_username` from the ID token

**Files:**
- Modify: `internal/oidc/oidc.go`, `internal/api/oidcauth.go`
- Test: `internal/api/oidcauth_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/oidcauth_test.go`:

```go
// The github_username claim is stored on the actor and re-synced on every
// login, since the link flow strict-matches against it long after login.
func TestOIDCTokenExchangeSyncsGitHubUsername(t *testing.T) {
	st, h, iss := newOIDCServer(t)
	ctx := t.Context()

	exchange := func(ghUser string) {
		t.Helper()
		claims := map[string]any{
			"preferred_username": "stig",
			"name":               "Stig",
			"groups":             []string{"user"},
		}
		if ghUser != "" {
			claims["github_username"] = ghUser
		}
		rr := doReq(t, h, "POST", "/auth/oidc/token", "",
			map[string]string{"id_token": iss.SignToken(t, claims)})
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
		}
	}

	exchange("stigsb")
	a, err := st.GetActor(ctx, "stig")
	if err != nil {
		t.Fatalf("get actor: %v", err)
	}
	if a.ExpectedGitHubLogin != "stigsb" {
		t.Fatalf("expected_github_login = %q, want stigsb", a.ExpectedGitHubLogin)
	}

	// Login must still succeed when the attribute is absent — it is required to
	// link, never to sign in.
	exchange("")
	a, err = st.GetActor(ctx, "stig")
	if err != nil {
		t.Fatalf("get actor after claimless login: %v", err)
	}
	if a.ExpectedGitHubLogin != "" {
		t.Fatalf("expected_github_login = %q, want cleared", a.ExpectedGitHubLogin)
	}
}
```

`doReq` JSON-encodes a non-nil body, and the endpoint answers **201 Created** — both match the neighbouring `TestOIDCTokenExchangeMintsToken`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestOIDCTokenExchangeSyncsGitHubUsername -v`
Expected: FAIL — `expected_github_login = "", want stigsb`.

- [ ] **Step 3: Write the implementation**

In `internal/oidc/oidc.go`, add to `Claims`:

```go
type Claims struct {
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
	// GitHubUsername is the realm's github_username user attribute, exposed by
	// a protocol mapper on the worklode client. Empty when unset; login never
	// depends on it (spec 023 §3.2).
	GitHubUsername string `json:"github_username"`
}
```

In `internal/api/oidcauth.go`, pass it through in `provisionActor`:

```go
	if err := s.st.UpsertHumanActor(ctx, c.PreferredUsername, c.Name, c.HasRole("admin"), c.GitHubUsername); err != nil {
		return "", err
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/oidc/... ./internal/api/ -run 'OIDC' -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oidc/oidc.go internal/api/oidcauth.go internal/api/oidcauth_test.go
git commit -m "Sync the github_username claim onto the actor at login"
```

---

## Task 3: GitHub stops being a CLI login provider

**Files:**
- Modify: `internal/api/cliauth.go`
- Test: `internal/api/cliauth_test.go`

**Decision (spec 023 §3.1):** interactive login — web and CLI — exists iff
OIDC is configured. A server with only the GitHub App configured has a link
flow but **no** login: `/auth/cli/login` and `/.well-known/lode-login` 404,
and the discovery document always advertises exactly `["keycloak"]`.
`internal/cli` needs no change — it prints whatever the server advertises,
and its own tests fake the server response.

- [ ] **Step 1: Update the tests that pin GitHub as a login provider**

In `internal/api/cliauth_test.go`:

`TestCLILoginValidatesLoopback` builds its server with `gh:
&githubauth.Client{}`. Replace that with a real verifier against the fake
issuer, and expect the final redirect to point at `/auth/login`:

```go
	iss := oidctest.NewIssuer(t)
	v, err := oidc.New(context.Background(), iss.URL(), iss.ClientID)
	if err != nil {
		t.Fatalf("oidc verifier: %v", err)
	}
	s := &server{cfg: Config{SessionSecret: "sek"}, oidc: v,
		cliCodes: newCLICodeStore(func() time.Time { return time.Unix(1000, 0) })}
```

and at the end of that test:

```go
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/auth/login") {
		t.Fatalf("redirect = %q; want /auth/login", loc)
	}
```

Replace `TestWellKnownLoginReportsProviders` wholesale, and add the 404 case:

```go
func TestWellKnownLoginReportsProviders(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	v, err := oidc.New(context.Background(), iss.URL(), iss.ClientID)
	if err != nil {
		t.Fatalf("oidc verifier: %v", err)
	}
	s := &server{oidc: v, cfg: Config{PublicURL: "https://wl.example.com"}}
	req := httptest.NewRequest(http.MethodGet, "/.well-known/lode-login", nil)
	rr := httptest.NewRecorder()
	s.wellKnownLogin(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["authorize_url"] != "https://wl.example.com/auth/cli/login" || m["token_url"] != "https://wl.example.com/auth/cli/token" {
		t.Fatalf("urls wrong: %v", m)
	}
	provs, _ := m["providers"].([]any)
	if len(provs) != 1 || provs[0] != "keycloak" {
		t.Fatalf("providers = %v; want [keycloak]", m["providers"])
	}
}

// A GitHub App configured for account linking is not a login provider: with
// no OIDC there is no interactive login at all (spec 023 §3.1).
func TestCLILoginRequiresOIDC(t *testing.T) {
	s := &server{gh: &githubauth.Client{}, cfg: Config{PublicURL: "https://wl.example.com", SessionSecret: "sek"}}
	for path, h := range map[string]http.HandlerFunc{
		"/.well-known/lode-login": s.wellKnownLogin,
		"/auth/cli/login":         s.cliLogin,
	} {
		rr := httptest.NewRecorder()
		h(rr, httptest.NewRequest(http.MethodGet, path+"?state=x&redirect_uri="+url.QueryEscape("http://localhost:5555/"), nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d; want 404", path, rr.Code)
		}
	}
}
```

Add `github.com/sunstoneinstitute/worklode/internal/oidc` and
`github.com/sunstoneinstitute/worklode/internal/oidc/oidctest` to the file's
imports; `githubauth` stays (the 404 test uses it).

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/api/ -run 'TestCLILogin|TestWellKnownLogin' -v`
Expected: FAIL — `TestCLILoginRequiresOIDC` gets 302/200 instead of 404 (a configured App still counts as a login provider); the rewritten tests already pass.

- [ ] **Step 3: Gate the CLI login on OIDC alone**

In `internal/api/cliauth.go`, change the guard in `cliLogin` (`:157`) and `wellKnownLogin` (`:216`) from `if s.oidc == nil && s.gh == nil` to `if s.oidc == nil`, and in `wellKnownLogin` delete the provider-accumulating block, responding with a fixed list:

```go
	writeJSON(w, http.StatusOK, map[string]any{
		"authorize_url": base + "/auth/cli/login",
		"token_url":     base + "/auth/cli/token",
		"providers":     []string{"keycloak"},
	})
```

Update `wellKnownLogin`'s doc comment: it reports the one provider there is, and 404s when OIDC is unconfigured.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestCLILogin|TestWellKnownLogin' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/cliauth.go internal/api/cliauth_test.go
git commit -m "Gate CLI login discovery on Keycloak alone"
```

---

## Task 4: Delete GitHub web login

**Files:**
- Delete: `internal/api/githubweb.go`, `internal/api/githubweb_test.go`
- Modify: `internal/api/oidcweb.go`, `internal/api/server.go`, `internal/githubauth/githubauth.go`, `internal/githubauth/githubauth_test.go`
- Test: `internal/api/oidcweb_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/oidcweb_test.go`:

```go
// The GitHub login provider and the chooser are gone: only Keycloak signs
// anyone in. These paths must 404 rather than redirect anywhere.
func TestGitHubLoginRoutesRemoved(t *testing.T) {
	_, h, _ := newOIDCServer(t)
	for _, path := range []string{"/auth/choose", "/auth/github/login", "/auth/github/callback"} {
		rr := doReq(t, h, "GET", path, "", nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, rr.Code)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestGitHubLoginRoutesRemoved -v`
Expected: FAIL — `/auth/choose` returns 200 (or 302), not 404.

- [ ] **Step 3: Delete the provider**

```bash
git rm internal/api/githubweb.go internal/api/githubweb_test.go
```

Move `loginTarget` into `internal/api/oidcweb.go`, simplified — Keycloak is the only destination:

```go
// loginTarget returns where webAuth sends unauthenticated users. Keycloak is
// the only login provider (spec 023 §3.1).
func (s *server) loginTarget(next string) string {
	return "/auth/login?next=" + url.QueryEscape(next)
}
```

Add `"net/url"` to that file's imports.

In the same file, `webAuth` no longer consults `s.gh`:

```go
// webAuth wraps a web page handler with session-cookie enforcement. It is a
// passthrough only when OIDC is disabled (the UI stays open, as in v1).
// Unauthenticated requests 302 to /auth/login with the current path in ?next.
func (s *server) webAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.oidc == nil {
			next(w, r)
			return
		}
		if c, err := r.Cookie(sessionCookieName); err == nil {
			if _, ok := verifySession(s.cfg.SessionSecret, c.Value, s.st.Now()); ok {
				next(w, r)
				return
			}
		}
		http.Redirect(w, r, s.loginTarget(r.URL.Path), http.StatusFound)
	}
}
```

Update the `githubweb.go` reference in this file's package comment to name plan 2's link flow instead: replace the `loginTarget (see githubweb.go)` sentence with `loginTarget, which always points at Keycloak`.

**Deliberate behavior change:** a server configured with only the GitHub App
(link-only, no OIDC) now serves the web UI **open**, where it used to gate.
That is intended: the App no longer implies a login provider (spec 023 §3.1),
so OIDC alone decides gating, exactly as in v1.

In `internal/api/server.go`, delete these three route registrations (around `:331-333`):

```go
	mux.HandleFunc("GET /auth/github/login", s.githubLogin)
	mux.HandleFunc("GET /auth/github/callback", s.githubCallback)
	mux.HandleFunc("GET /auth/choose", s.authChoose)
```

- [ ] **Step 4: Trim `githubauth.Client`**

In `internal/githubauth/githubauth.go`, delete `Roles`, the `Roles` method, `activeMembership`, and `membershipResp`; drop the `Org` and `AdminTeam` fields and narrow `New`:

```go
// Client holds GitHub App OAuth config for the account-link flow (spec 023
// §3.3). It is no longer an identity provider: worklode derives no roles from
// GitHub. APIBase and Endpoint default to the public GitHub but are
// overridable in tests.
type Client struct {
	ClientID     string
	ClientSecret string
	APIBase      string          // e.g. https://api.github.com
	Endpoint     oauth2.Endpoint // authorize/token endpoints
}

// New builds a Client for the public GitHub.
func New(clientID, clientSecret string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		APIBase:      "https://api.github.com",
		Endpoint:     githuboauth.Endpoint,
	}
}
```

Update the package doc comment's first sentence to: `Package githubauth wraps the GitHub App user-authorization (OAuth) flow used to link a worklode actor's GitHub account, and the App-authenticated REST reads in app.go.`

Delete every `Roles`-related test from `internal/githubauth/githubauth_test.go` and fix the remaining `New(...)` calls to the two-argument form.

Update the call site in `internal/api/server.go:268` to the two-argument form
(required to compile now that `Org`/`AdminTeam` are gone):

```go
		s.gh = githubauth.New(cfg.GitHubClientID, cfg.GitHubClientSecret)
```

The `GitHubOrg`/`GitHubAdminTeam` config fields and the startup guard stay
until Task 5, which owns the config removal.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/... 2>&1 | tail -20`
Expected: PASS. Compile errors will point at any remaining `s.gh.Org` or `provisionGitHubActor` reference — remove it.

- [ ] **Step 6: Commit**

```bash
git add -A internal/api internal/githubauth
git commit -m "Remove GitHub as a login provider"
```

(`git add -A internal/api` picks up the `server.go` constructor change.)

---

## Task 5: Remove the org/team configuration

**Files:**
- Modify: `internal/api/server.go`, `internal/cmd/serve.go`, `deploy/overlays/hzdev/kustomization.yaml`, `docs/follow-ups.md`
- Test: `internal/api/oidcauth_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/oidcauth_test.go`:

```go
// GitHub linking needs only the App client credentials and the encryption key;
// there is no org to be a member of any more, so NewServer must not demand one.
func TestNewServerAcceptsGitHubWithoutOrg(t *testing.T) {
	st := newTestStore(t)
	iss := oidctest.NewIssuer(t)
	_, _, err := api.NewServer(st, api.Config{
		OIDCIssuer:         iss.URL(),
		OIDCClientID:       iss.ClientID,
		PublicURL:          "http://localhost:8080",
		SessionSecret:      "test-session-secret",
		GitHubClientID:     "cid",
		GitHubClientSecret: "csecret",
		TokenEncKey:        strings.Repeat("2a", 32),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
}
```

Add `strings` to `internal/api/oidcauth_test.go`'s imports if absent (for `strings.Repeat`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestNewServerAcceptsGitHubWithoutOrg -v`
Expected: FAIL — `LODE_GITHUB_ORG is required when GitHub auth is enabled`.

- [ ] **Step 3: Write the implementation**

In `internal/api/server.go`, delete the `GitHubOrg` and `GitHubAdminTeam` fields from `Config` (`:63-66`) and delete the `LODE_GITHUB_ORG` guard at `:257-259`. (The constructor call is already the two-argument form — Task 4 changed it.)

Retitle the block comment on the `gh`/`tokenCipher` fields at `:143-146`:

```go
	// gh and tokenCipher are nil unless the GitHub App is configured. They
	// serve the account-link flow only; login never touches them.
```

In `internal/cmd/serve.go`, delete lines 91-92 (`GitHubOrg` / `GitHubAdminTeam`).

In `deploy/overlays/hzdev/kustomization.yaml`, delete the two `op: add` entries for `/data/LODE_GITHUB_ORG` and `/data/LODE_GITHUB_ADMIN_TEAM`.

- [ ] **Step 4: Record the follow-ups**

Append to `docs/follow-ups.md`:

```markdown
- **Orphaned `github:<id>` actors.** Spec 023 §3.1 leaves the actor rows the
  removed GitHub login provisioned in place. Nothing references them; a cleanup
  migration can drop them whenever convenient.
- **GitHub App permission ceiling (spec 023 §3.6).** Ops-side, no code: the App
  must stay Contents/Actions/Deployments/Pull requests **read** (Issues: write
  only when the spec 008 mirror lands), installed on selected repos excluding
  the provisioning and admin-cluster repos, with no-bypass rulesets on those
  repos. Never grant `contents: write`. Also drop the Organization → Members:
  read permission, which only the deleted role derivation used.
- **Keycloak `github_username` mapper.** The realm's worklode client needs a
  user-attribute protocol mapper exposing `github_username` in ID tokens
  (spec 023 §3.2) before anyone can link.
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/server.go internal/api/oidcauth_test.go internal/cmd/serve.go \
  deploy/overlays/hzdev/kustomization.yaml docs/follow-ups.md
git commit -m "Drop the GitHub org and admin-team configuration"
```

---

## Done when

`lode serve` offers exactly one login path (`/auth/login`), `/auth/choose` and `/auth/github/*` are 404, `/.well-known/lode-login` advertises only `keycloak`, no role derives from GitHub, and every Keycloak login records `expected_github_login` on the actor. Plan 2 (`2026-08-02-keycloak-primary-auth-2-link-and-tokens.md`) builds the link flow on top of that column.
