---
status: draft
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-8.4
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-14-project-crew-participants.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-8.2
    coverage: none
blockedBy:
  - 2026-08-14-project-crew-participants.md
---

# Google Chat crew spaces

Part 7 of the spec 029 plan series (P9), and the smallest. It builds the
consuming half of 029 §8.4: the `gchat-crew-space` offset-tracked subscriber
that gives every research-work project a Google Chat space kept in sync with
its Crew, plus the `projects.chat_space_name` column that makes space
creation idempotent, a small Chat API client on a dedicated GCP identity,
and the first GCP/Workload Identity wiring in this repo.

The producing half already landed:
`docs/plans/2026-08-14-project-crew-participants.md` emits
`crew.member_added` / `crew.member_removed` from every mutating call site
(`internal/api/crew.go:95,274`), in the same transaction as the store write.
This plan consumes those events and nothing upstream of them changes.

## Coverage notes

- **029 §8.4 `partial`** — the crew-participants plan delivered the event
  emission; this plan delivers everything else the section names. Together
  they are `full`: §8.4's own closing list (archiving on project close,
  lead manager status, role labels in the space topic) is declared out of
  scope *by the section*, so it owes no further plan.
- **029 §8.2 `none`** — §8.2's rule that no producing handler gains a
  hardcoded notifier governs this plan, and §8.4 is its one stated exception
  that proves the rule: the subscriber is itself offset-tracked, reacting to
  Crew events, never a notifier wired into the Crew mutation handler.

## Global constraints

- **Spellings are pinned.** Consumed event types: exactly
  `crew.member_added` and `crew.member_removed` (payload
  `{"project", "actor", "roles", "lead", "by"}`, source `"cli"` on the JSON
  API and `"web"` on the cockpit form — all fixed by the crew-participants
  plan). Emitted event type: exactly `gchat.crew_space.member_skipped`,
  source `"system"` (already in the `events.source` CHECK — no migration
  needed for it). Subscriber name: exactly `gchat-crew-space`. Space display
  name: exactly `"{project.Key} {project.Name} Crew"`, e.g.
  `"COW Coastal Offshore Wind Crew"`. Chat scopes:
  `https://www.googleapis.com/auth/chat.app.spaces.create` and the
  membership-management equivalent
  (`https://www.googleapis.com/auth/chat.app.memberships`).
- **Event-type constants.** Task 4 hoists the two crew event strings into
  package constants in `internal/api` (the package that both emits and, after
  this plan, consumes them) — **not** into `internal/eventbus/vocab.go`.
  `vocab.go` is the hand-mirror of `ns/ontology.ttl`'s `wl:` RDF types with
  payload validation; dotted backbone events bypass that validation by design
  (see the comment on `handleDocLifecycle`,
  `internal/api/docwatch.go`), so a vocab entry would be a lie about what
  validates them. Existing tests keep their string literals — they pin the
  wire spelling against exactly this kind of refactor.
- **`chat_space_name` never crosses the HTTP boundary.** It is read and
  written only by the subscriber, so it stays out of `internal/model.Project`
  and out of `projectColumns` (`internal/store/projects.go:84`) — dedicated
  store accessors instead. ADR 036 is not implicated: nothing is serialized.
- **At-least-once discipline** (025 §15 / §15.7): one active consumer via the
  advisory lock `eventbus.Run` already takes; the handler acks only once its
  Chat API calls succeed — on failure it returns the error and the event is
  redelivered; it never returns `OutcomeError` itself. `chat_space_name` is
  the idempotency guard `crew.member_added` checks before creating, and it is
  persisted immediately after the create call returns, before any membership
  call, to shrink the duplicate-space window on a crash.
- **Skip vocabulary**: reasons are exactly `no_actor`, `no_email`, `domain` —
  the bounded metric label and the `reason` value in the
  `member_skipped` payload. Precedence when several apply: `no_actor`, then
  `no_email`, then `domain`.
- **Configuration**: `LODE_GCHAT_WORKSPACE_DOMAIN` on the server `Config`,
  following the existing off-unless-set pattern (OIDC, blob storage): unset
  means no subscriber loop starts and nothing else changes, so the local
  compose stack and CI need no Google project. 029 §8.4 places this setting
  "alongside `approval_flow`" as instance configuration; P3
  (`2026-08-25-approvals-2-flows-and-requirements`) establishes where
  instance configuration lives, so Task 7 records a follow-up to move the
  env var there once P3 lands. An env var now keeps this plan independent of
  P3's ordering.
- **Metrics** (spec 022): `worklode_gchat_api_calls_total{op, outcome}` with
  `op ∈ {create_space, add_member, remove_member}`,
  `outcome ∈ {ok, error}` in `internal/gchat/metrics.go`, and
  `worklode_gchat_member_skips_total{reason}` in the subscriber's owning
  package. Nil-safe structs, `prometheus.Registerer` threaded from the
  server, bounded labels — never a project, actor, or space id. Per-event
  loop metrics come free from the shared eventbus instruments.
- **Migration** 0065 is a new `.up.sql`/`.down.sql` pair listed in
  `deploy/base/kustomization.yaml`; never edit a shipped migration.
  `./scripts/check-migrations.sh --no-fix` must exit 0.
- **Store and handler tests need Postgres with pgvector**
  (`TEST_POSTGRES_DSN`); a skipped test proved nothing. No e2e task: this
  plan adds no public surface — the subscriber is proven by handler tests
  over real Postgres with a faked Chat API, and `e2e/` stays
  public-surfaces-only.
- **Every task leaves `go test ./...` green** and ends with a commit.
- All tasks except Task 7 are specified for a Sonnet-tier implementer per
  `MODEL_SELECTION.md`. Task 7 has human prerequisites and an external
  system; escalate on the first sign reality diverges.

## Tasks

### Task 1 — Migration 0065 and chat-space store accessors

```yaml
kind: chore
priority: high
skills:
  - golang-migrate:migration
blockedBy: [ ]
```

`deploy/base/migrations/0065_project_chat_space.up.sql`, listed in
`deploy/base/kustomization.yaml` after the 0064 entries (P8's block; if it
has not landed, after the highest present — `./scripts/check-migrations.sh`
renumbers on collision):

```sql
-- Google Chat crew space (spec 029 §8.4): the created space's resource name
-- ("spaces/AAA..."). NULL until the gchat-crew-space subscriber creates the
-- space on the project's first crew.member_added. It is both the address for
-- membership calls and the idempotency guard against at-least-once delivery
-- creating a second space for one project.
ALTER TABLE projects ADD COLUMN chat_space_name text;
```

`0065_project_chat_space.down.sql` drops the column.

Store accessors in `internal/store/projects.go`, deliberately **not** added
to `projectColumns` or `model.Project` (see Global constraints):

```go
// ProjectChatSpace returns the project's Chat space resource name, "" when
// none has been created. Unknown project: ErrNotFound.
func (s *Store) ProjectChatSpace(ctx context.Context, projectID string) (string, error)

// SetProjectChatSpace records the created space's resource name. It refuses
// to overwrite a non-NULL value: the space is created once per project.
func (s *Store) SetProjectChatSpace(ctx context.Context, projectID, name string) error
```

`SetProjectChatSpace` is `UPDATE projects SET chat_space_name = $2 WHERE id
= $1 AND chat_space_name IS NULL`, returning an error naming the project
when zero rows match and the project exists — under the subscriber's
advisory lock that path indicates a logic error, not a race, so it should be
loud.

Store test in `internal/store/projects_test.go`: read on a fresh project
returns `""`; set then read round-trips; a second set errors; unknown
project errors.

- [ ] Write the migration pair and the kustomization entry;
      `./scripts/check-migrations.sh --no-fix` exits 0.
- [ ] Round-trip up → down → up against a scratch database
      (`golang-migrate:test-roundtrip`).
- [ ] `go test -trimpath ./internal/store -run TestProjectChatSpace` with
      Postgres up — green.
- [ ] Commit: `Add projects.chat_space_name and its store accessors`.

### Task 2 — Pure routing: decide create / add / remove / skip

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

New package `internal/gchat`, this task stdlib-only. Two pure functions,
table-tested with no database and no Chat API — every later task consumes
them instead of re-deriving the rules.

`internal/gchat/route.go`:

```go
// Facts is everything the subscriber fetched about one crew event.
type Facts struct {
	Added      bool   // crew.member_added; false means crew.member_removed
	Space      string // projects.chat_space_name, "" when NULL
	ActorKnown bool   // an actors row exists for the event's actor id
	Email      string // actors.email, "" when never stored
	Domain     string // the instance's configured Workspace domain
}

// Step ops.
const (
	OpCreateSpace  = "create_space"
	OpAddMember    = "add_member"
	OpRemoveMember = "remove_member"
	OpSkip         = "skip"
)

// Skip reasons (bounded: metric label and member_skipped payload value).
const (
	SkipNoActor = "no_actor"
	SkipNoEmail = "no_email"
	SkipDomain  = "domain"
)

type Step struct {
	Op     string
	Email  string // add/remove only
	Reason string // skip only
}

// Decide is the routing decision of spec 029 §8.4. Pure.
func Decide(f Facts) []Step

// SpaceDisplayName is exactly "{key} {name} Crew" (029 §8.4).
func SpaceDisplayName(key, name string) string
```

The rules `Decide` encodes, exhaustively:

- **Added, no space yet**: `create_space` first, always — a project
  acquiring Crew gets its space even when this particular member cannot be
  invited, because §8.4 says a skip must never block space creation for
  everyone else. Then `add_member` if the member resolves, else `skip`.
- **Added, space exists**: `add_member` if the member resolves, else `skip`.
- **Removed, no space**: no steps — nothing to remove, and a skip event
  here would be noise about a space that never existed.
- **Removed, space exists**: `remove_member` if the member resolves, else
  `skip`.
- **Resolving**: `ActorKnown` false → `no_actor` (§6.1's invited external
  expert, which P8 builds — this handles their absence gracefully rather
  than depending on P8); `Email` empty → `no_email`; the part after `@` not
  equal (case-insensitive) to `Domain` → `domain`.

First test, `internal/gchat/route_test.go`:

```go
func TestDecide(t *testing.T) {
	const dom = "sunstoneinstitute.ai"
	cases := []struct {
		name string
		in   gchat.Facts
		want []gchat.Step
	}{
		{
			name: "first member resolvable: create then add",
			in:   gchat.Facts{Added: true, ActorKnown: true, Email: "ada@sunstoneinstitute.ai", Domain: dom},
			want: []gchat.Step{{Op: gchat.OpCreateSpace}, {Op: gchat.OpAddMember, Email: "ada@sunstoneinstitute.ai"}},
		},
		{
			name: "first member off-domain: create then skip",
			in:   gchat.Facts{Added: true, ActorKnown: true, Email: "bob@example.org", Domain: dom},
			want: []gchat.Step{{Op: gchat.OpCreateSpace}, {Op: gchat.OpSkip, Reason: gchat.SkipDomain}},
		},
		// later member resolvable; later member no email; added no actor;
		// removed with space; removed no space (nil); removed no email;
		// case-insensitive domain match.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gchat.Decide(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Decide(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}
```

Plus `TestSpaceDisplayName` pinning
`SpaceDisplayName("COW", "Coastal Offshore Wind") ==
"COW Coastal Offshore Wind Crew"`.

- [ ] `go test -trimpath ./internal/gchat` — green, no Postgres needed.
- [ ] Commit: `Decide crew-space actions as a pure function`.

### Task 3 — Chat API client on Application Default Credentials

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

`internal/gchat/client.go`: the three Chat operations behind one interface,
so the subscriber and its tests never see HTTP.

```go
// API is the slice of the Google Chat REST API this feature uses.
type API interface {
	// CreateSpace creates a named space and returns its resource name
	// ("spaces/AAA...").
	CreateSpace(ctx context.Context, displayName string) (string, error)
	// AddMember invites the human with this email. Already a member: nil.
	AddMember(ctx context.Context, space, email string) error
	// RemoveMember removes them. Not a member / never added: nil.
	RemoveMember(ctx context.Context, space, email string) error
}
```

The real implementation is a thin REST client — no
`google.golang.org/api` dependency; `golang.org/x/oauth2` is already a
direct dependency and `golang.org/x/oauth2/google` provides ADC, which is
Workload Identity on the cluster and a service-account key or `gcloud auth
application-default login` locally:

- `NewClient(ctx, reg)` builds an `http.Client` from
  `google.DefaultTokenSource(ctx, scopeSpacesCreate, scopeMemberships)`
  (the two scope constants from Global constraints). A `baseURL` field
  defaults to `https://chat.googleapis.com` and is overridable for tests.
- `CreateSpace`: `POST {base}/v1/spaces` with
  `{"spaceType":"SPACE","displayName":...}`; the response's `name` is the
  return value.
- `AddMember`: `POST {base}/v1/{space}/members` with
  `{"member":{"name":"users/{email}","type":"HUMAN"}}`; HTTP 409
  (already a member) is success.
- `RemoveMember`: `DELETE {base}/v1/{space}/members/users/{email}`;
  HTTP 404 is success — idempotent under redelivery and under removal of a
  member who was skipped at add time.
- Verify the exact member-resource shapes against the current Chat REST
  reference (app-credential `spaces.create` / `members.create` /
  `members.delete`) while implementing; the fake-backed tests pin this
  client's contract and Task 7's smoke check validates it against the real
  API. Any other non-2xx: an error carrying status and the response body's
  `error.message`, never the whole body.

`internal/gchat/metrics.go`: nil-safe `Metrics` struct owning
`worklode_gchat_api_calls_total{op, outcome}` (spec 022 shape:
`NewMetrics(reg *prometheus.Registry)`-style constructor taking a
`prometheus.Registerer`, nil registerer means no-op). The client increments
it around every call.

Tests with `httptest.Server` as `baseURL` and a `oauth2.StaticTokenSource`:
request paths, bodies, and `Authorization: Bearer` asserted; 409-on-add and
404-on-delete return nil; a 403 surfaces `error.message`; metrics counted
per op and outcome (`testutil.ToFloat64` as in the other `metrics.go`
tests).

- [ ] `go test -trimpath ./internal/gchat` — green, no network.
- [ ] `go vet ./...` and `go mod tidy` — the only new module-graph entry is
      what `golang.org/x/oauth2/google` itself requires.
- [ ] Commit: `Add the Google Chat REST client`.

### Task 4 — Event constants, config, and the subscriber loop

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

The `gchat-crew-space` subscriber skeleton: registered, offset-tracked,
gated on configuration, acking everything — routing lands in Tasks 5–6.

- `internal/api/crew.go`: hoist the two string literals (lines 95 and 274)
  into package constants `eventCrewMemberAdded = "crew.member_added"` /
  `eventCrewMemberRemoved = "crew.member_removed"` (rationale in Global
  constraints). Existing tests keep their literals.
- `internal/api/server.go` `Config`, following the blob-storage pattern:

```go
// Google Chat crew spaces (spec 029 §8.4). Off unless GChatWorkspaceDomain
// is set: no subscriber loop starts and everything else behaves exactly as
// before, so the local stack and CI need no Google project. When set,
// NewServer builds the Chat client on Application Default Credentials
// (Workload Identity on the cluster) and starts the gchat-crew-space
// subscriber — BackgroundCtx must be non-nil, as for doc-lifecycle.
GChatWorkspaceDomain string // LODE_GCHAT_WORKSPACE_DOMAIN

// GChatForTest injects a gchat.API directly, bypassing ADC construction.
// Tests only; production sets GChatWorkspaceDomain.
GChatForTest gchat.API
```

- `internal/serverapp/serverapp.go`: pass
  `os.Getenv("LODE_GCHAT_WORKSPACE_DOMAIN")`.
- `internal/api/gchatwatch.go`: `const gchatSubscriber = "gchat-crew-space"`
  and `(s *server) handleGChatCrew(ctx, ev) (eventbus.Outcome, error)`,
  modeled on `handleDocLifecycle` (`internal/api/docwatch.go`): any event
  whose type is not one of the two crew constants returns
  `OutcomeApplied` untouched — acking is what keeps the offset moving.
  In this task the crew branch is also a pass-through.
- `internal/api/server.go`, in the existing `if cfg.BackgroundCtx != nil`
  block: hoist `busMetrics := eventbus.NewMetrics(reg, st)` above both
  loops so the two `eventbus.Run` goroutines share one registration
  (the labels already carry the subscriber name — update the "first and
  only registration" comment to say so), then, when the feature is on
  (`GChatWorkspaceDomain != ""` or `GChatForTest != nil`):
  `EnsureEventSubscriber(gchatSubscriber)`, build the client
  (`GChatForTest` wins), and start the second `Run` with
  `Name: gchatSubscriber, Handler: s.handleGChatCrew`, logging a stopped
  loop the same way doc-lifecycle does. When the feature is off no
  subscriber row is ever created, so `lode event subscribers` shows no
  perpetually-lagging row.

Handler tests in `internal/api/gchatwatch_test.go` (real Postgres, the
in-process server the docwatch tests use, `Config.GChatForTest` set to a
recording fake): with the feature on, an unrelated event (e.g. a doc event)
is acked and the `gchat-crew-space` offset advances past it; with the
feature off, no `gchat-crew-space` subscriber row exists.

- [ ] `go test -trimpath ./internal/api -run 'GChat|Crew'` — green.
- [ ] `go test -trimpath ./...` — green (the crew.go refactor is
      behavior-neutral).
- [ ] Commit: `Start the gchat-crew-space subscriber loop`.

### Task 5 — Handle crew.member_added: create, invite, or skip

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 4]
```

The tracer: the `crew.member_added` branch of `handleGChatCrew`, joining
Tasks 1–4 over real Postgres.

Flow, in `internal/api/gchatwatch.go`:

1. Parse the payload's `project` and `actor` (malformed payload: return the
   error — redelivery retries, `lode event seek` steps past a truly bad
   event, exactly the docwatch stance).
2. Fetch facts: `GetProject` (key, name), `ProjectChatSpace`, `GetActor`
   (`ErrNotFound` → `ActorKnown: false`, not an error — that is P8's
   invited participant, skipped until linked).
3. `gchat.Decide` with the configured domain; execute the steps in order:
   - `create_space`: `CreateSpace(ctx, gchat.SpaceDisplayName(key, name))`,
     then **immediately** `SetProjectChatSpace` — before any membership
     call (Global constraints: at-least-once discipline).
   - `add_member`: `AddMember(ctx, space, email)` — the space is the one
     just created or the previously stored one.
   - `skip`: record the visibility event and count
     `worklode_gchat_member_skips_total{reason}`:

```go
_, _, err := s.st.RecordEvent(ctx, "system",
	fmt.Sprintf("gchat-crew-space:skip:%d", ev.ID),
	"gchat.crew_space.member_skipped",
	mustJSON(map[string]any{"project": projectID, "actor": actorID, "reason": step.Reason}),
	nil)
```

   The `(source, external_id)` key makes the skip record idempotent under
   redelivery — a redelivered event re-records nothing.
4. Any Chat API error: return it. The event is not acked and is redelivered;
   on redelivery a space persisted in step 3 is found via
   `ProjectChatSpace`, so `Decide` yields no second `create_space` — that
   is the idempotency guard §8.4 names.

Handler tests (real Postgres, recording fake `gchat.API`), driving the
subscriber through recorded events and polling the fake:

```go
func TestGChatFirstMemberCreatesSpaceAndInvites(t *testing.T) {
	// project COW "Coastal Offshore Wind"; actor ada with
	// email ada@sunstoneinstitute.ai; domain sunstoneinstitute.ai.
	// Record a crew.member_added event through the JSON API (the real
	// producing path), then wait for the fake to see:
	//   CreateSpace("COW Coastal Offshore Wind Crew") -> "spaces/TEST1"
	//   AddMember("spaces/TEST1", "ada@sunstoneinstitute.ai")
	// and assert ProjectChatSpace(ctx, "COW") == "spaces/TEST1".
}
```

Then: a second member added → exactly one `CreateSpace` ever, one more
`AddMember`; a member with no stored email → space still created, no
`AddMember`, exactly one `gchat.crew_space.member_skipped` event with
`reason: "no_email"` (and calling the handler again with the same event
records no second one); an off-domain email → `reason: "domain"`; an
unknown actor id → `reason: "no_actor"`; a fake returning an error from
`AddMember` → the handler call (invoked directly, as the docwatch error
tests do) returns the error and the space name is already persisted.

- [ ] `go test -trimpath ./internal/api -run GChat` with Postgres up — green.
- [ ] Commit: `Create crew spaces and invite members on crew.member_added`.

### Task 6 — Handle crew.member_removed

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

The `crew.member_removed` branch, through the same fact-fetch and
`gchat.Decide` path — the actor row outlives crew membership, so the email
lookup works after removal:

- Space exists, member resolves: `RemoveMember(ctx, space, email)`. The
  client's 404-is-success (Task 3) makes removal of a member who was
  skipped at add time, or a redelivered removal, a clean ack.
- No space: ack with no steps and no skip event.
- Member unresolvable: the same `member_skipped` recording and metric as
  Task 5 (same external-id scheme, so also idempotent).
- Chat API error: return it, no ack, redelivered.

Handler tests: add-then-remove round-trip through the JSON API (fake sees
`RemoveMember` with the right space and email); removal on a project with no
space acks without touching the fake; removal of the no-email member records
one `member_skipped`; a failing `RemoveMember` leaves the event unacked.

- [ ] `go test -trimpath ./internal/api -run GChat` with Postgres up — green.
- [ ] `go test -trimpath ./...` — green.
- [ ] Commit: `Remove crew-space members on crew.member_removed`.

### Task 7 — GCP identity, Workspace approval, and deploy wiring

```yaml
kind: chore
priority: high
skills: [ ]
blockedBy: [6]
```

The riskiest piece of this plan, isolated so Tasks 1–6 never wait on it:
worklode's server has no GCP or Workload Identity wiring today — this is
the first. The Chat App identity is **new rather than reused**: unlike the
Flux alert bot it creates spaces and manages membership rather than just
posting messages, and its scopes need a one-time Workspace-admin approval
beyond the Flux bot's `chat.bot` role.

**Human prerequisites — name them in the task's progress notes and hand
them to an operator; do not simulate them:**

1. A GCP service account (e.g. `worklode-gchat@<project>.iam.gserviceaccount.com`)
   with the Chat API enabled, configured as a Google Chat App, following the
   existing `flux-notification-proxy` Chat App in the provisioning repo as
   the pattern for App configuration and Workload Identity binding.
2. Workload Identity binding between that GSA and worklode's Kubernetes
   ServiceAccount (provisioning-repo change; the KSA is created below).
3. **One-time Workspace-admin approval** of the two scopes from Global
   constraints for this Chat App. This is a human act in the Workspace admin
   console; until it happens, staging calls fail with a permission error and
   the affected events sit unacked, visible in `lode event subscribers` lag —
   which is the correct failure mode, not something to code around.

**Repo changes:**

- `deploy/base/`: a `ServiceAccount` manifest for the server (today the
  deployment uses the namespace default — `deploy/base/deployment.yaml` has
  no `serviceAccountName`), `serviceAccountName` on the deployment, and the
  Workload Identity annotation value supplied per overlay.
- `LODE_GCHAT_WORKSPACE_DOMAIN` added to the server's environment in the
  overlay that turns the feature on (staging first).
- `docs/follow-ups.md`, one entry per its format: the Workspace-domain
  setting is an env var until P3
  (`2026-08-25-approvals-2-flows-and-requirements`) establishes the
  instance-configuration home 029 §8.4 places it in ("alongside
  `approval_flow`"); move it there and drop the env var.

**Smoke check, staging** (the one test that runs against the real Chat
API): create a throwaway project, `lode project crew add` yourself, and
verify the space appears in Chat with the pinned display-name format and
you in it; remove yourself and verify the membership goes;
`worklode_gchat_api_calls_total` shows the three ops with
`outcome="ok"`, and no `member_skipped` event was recorded. Record the
observed space resource name in the task's progress notes as evidence.

- [ ] Manifests applied; server pod runs under the annotated KSA.
- [ ] Smoke check passes as described.
- [ ] Follow-up entry recorded.
- [ ] Commit (repo changes only):
      `Wire the server onto Workload Identity for Google Chat`.
