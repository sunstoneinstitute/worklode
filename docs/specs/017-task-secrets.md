---
status: draft
issued: 2026-07-28
requires:
- docs/specs/004-execution-backbone.md
- docs/specs/008-worklode-plugin.md
- docs/specs/016-org-wide-skills.md
amendedBy:
  "#sec-1":
  - 042-secret-templates.md#sec-2
  - 043-secrets-catalog-home.md#sec-2
  - 047-loader-sensitive-secret-names.md#sec-2
  "#sec-3":
  - 042-secret-templates.md#sec-3
  - 048-exit-purge-on-a-gone-lease.md#sec-2
  "#sec-4":
  - 042-secret-templates.md#sec-4
  - 048-exit-purge-on-a-gone-lease.md#sec-2
  - 050-scrub-inherited-environment.md#sec-2
  "#sec-6":
  - 042-secret-templates.md#sec-5
  - 048-exit-purge-on-a-gone-lease.md#sec-3
  "#sec-8":
  - 043-secrets-catalog-home.md#sec-2
  "#sec-10":
  - 050-scrub-inherited-environment.md#sec-2
---
# Spec 017 — Task-declared secrets

## 0. Purpose & scope {#sec-0}

Tasks declare which secrets they need, by symbolic name, the same way they pin skills (016).
Machinery resolves the names against an org-wide catalog and materializes the values — through a
ceremony the operator participates in — before execution goes unattended. Three failure modes this
removes:

- **Stalling.** An agent that discovers mid-task it needs a credential stops making autonomous
  progress: it probes `op`, waits on an interactive prompt that times out, or improvises.
- **Sandboxability.** Detached and remote executors cannot fetch secrets interactively at all;
  a declared, pre-materialized set is what makes execution off the operator's laptop possible.
- **Least privilege.** In a multi-player org, a session should hold exactly the credentials its
  task declared — auditable in the event log — not whatever the operator's environment happens
  to contain.

**Worklode is not a security broker.** It stores symbolic names and initiates a ceremony; it never
stores, transports, or sees a secret value. 1Password is the source of truth for values, `op://`
URLs are the addressing scheme, and the operator's own `op` session is the decryption authority.
This is what keeps Employee-vault secrets usable: `op://Employee/…` resolves against each
operator's private vault by construction, with no remote service account crossing that privacy
boundary.

**v1** is the local-laptop workflow (macOS primary, Linux best-effort). **v1.5** packs the same
materialized set for remote executors. **v2** adds deferred grants via mobile approval
(Google Chat). Only v1 is normative below; the later phases are sketched so v1 artifacts stay
forward-compatible.

## 1. Names & catalog {#sec-1}

> **Amended by spec 042 §2.** An entry whose value would exceed the OS
> keystore item cap (a full kubeconfig) is declared as a plaintext `template`
> (a sibling ConfigMap key) plus `cred.<PLACEHOLDER>` references — only the
> credentials take the `op://` path — with an optional `env` exported name.

> **Amended by ADR 043 §2.** The catalog is not a git-tracked ConfigMap. It is
> the 1Password item `worklode-secrets-catalog`, projected per environment into
> a Kubernetes Secret of the same name by an ExternalSecret using
> `dataFrom.extract`; `catalog.toml` and each 042 template are field labels on
> that item. Changes are 1Password edits, not deployment-repo PRs; the mount
> path and everything the server does are unchanged.

> **Amended by ADR 047 §2.** The grammar additionally denies loader-sensitive
> names: anything beginning `LD_` or `DYLD_`, plus an enumerated set of shell
> and language-runtime loading variables (`PATH`, `IFS`, `ENV`, `BASH_ENV`,
> `PYTHONPATH`, …) listed in ADR 047 §3. A name matching the pattern but naming
> one of those is rejected at every gate.

**Namespace.** Secret names are env-var style (`^[A-Z][A-Z0-9_]*$`) and **org-unique** — never
per-project, because a repo may participate in multiple projects. Examples: `GIT_SIGNING_KEY`,
`GITHUB_TOKEN`, `KUBECONFIG_HZDEV`, `OPENALEX_API_KEY`, `SEMANTIC_SCHOLAR_API_KEY`.

**Catalog.** One authoritative map from name to 1Password reference plus policy, TOML:

```toml
[GITHUB_TOKEN]
ref = "op://Employee/GitHub agent token/credential"
description = "GitHub credential the agent operates as (operator's own identity)"
baseline = true            # packed for every task; no consent prompt

[KUBECONFIG_HZDEV]
ref = "op://Infrastructure/hzdev kubeconfig/kubeconfig"
description = "Kubernetes access to the hzdev cluster, for troubleshooting tasks"
# baseline defaults to false: must be declared per task, listed at the consent prompt
```

- **Storage:** a ConfigMap in the worklode service deployment, mounted into the server. Changes
  go through the deployment repo's normal PR flow. Deliberately not in a potentially-public repo:
  vault and item names are themselves mildly sensitive.
- **Serving:** `GET /api/v1/secrets/catalog`, **authenticated** (any actor token). No well-known
  or unauthenticated URL — the name → `op://` map must not leak vault/item structure.
- **`baseline = true`** marks secrets every task needs regardless of declaration (commit signing
  key, GitHub credentials — remote executors can never assume ssh-agent forwarding). Baseline
  secrets are packed for every claim and are exempt from the consent prompt. Everything else
  must be declared per task and is consented to at the ceremony.
- The catalog carries no values and no per-user state. Per-operator resolution falls out of
  `op://Employee/…` naming.

## 2. Declaration {#sec-2}

Mirrors 016's skill pins exactly:

- **Task:** a `secrets` name list on the Task (backbone field, settable at create/update).
- **Design docs:** `secrets: [NAME, …]` in doc frontmatter, ingested when 025 lands; until then
  plans state them and the planner copies them onto tasks at `lode task add` time.
- **Resolution at claim:** task pins ∪ governing-doc pins ∪ baseline set.
- A declared name missing from the catalog is a **brief warning, never a failure** — same
  degradation posture as an unknown skill pin (016).

Declaring is the **planner's** job: writing plans includes deciding, per task, which catalog
names the executor will need. The `lode-secrets` skill (below) makes that a standing instruction.

## 3. Claim-time ceremony & materialization {#sec-3}

> **Amended by spec 042 §3.** A templated entry materializes one keystore item
> per credential, named `<NAME>__<PLACEHOLDER>`; consent and the
> `secrets_materialized` event stay at entry granularity, and the local
> manifest additionally records the entry's item names, exported env name, and
> template text so exec stays offline.

> **Amended by ADR 048 §2.** The "exit" in this section's trigger list is a
> *conditional* purge: the exit hook asks the backbone about the task's lease
> and purges only on a definite "gone" (no lease, or the task 404s) — never on
> a live lease, a timeout, or any error.

Claim time is the one moment a human is guaranteed present (they just ran `/lode-next`), so that
is when consent and decryption happen — everything after may be unattended.

Sequence, appended to the spec-008 claim flow (after lease + worktree bind, before brief
injection):

1. **Fetch & intersect.** The hook fetches the catalog and resolves the task's declared ∪
   baseline names to `op://` refs.
2. **Consent.** Non-baseline names are shown as one list with descriptions; the operator gives a
   single yes/no for the set. Decline ⇒ no materialization; the claim still succeeds (see
   Degradation).
3. **Render template.** The hook writes `.worklode/secrets.env` in the worktree, in `op run`
   env-file format — `NAME=op://vault/item/field` lines, **references only, never values**. The
   file is the portable packing manifest: v1.5 re-uses it verbatim when packing for a remote
   executor.
4. **Materialize — one approval.** The hook runs
   `op run --env-file .worklode/secrets.env -- lode secrets pack`.
   A single `op run` invocation resolves every reference under **one** 1Password authorization
   (Touch ID) instead of one prompt per item. `lode secrets pack`, running as the child with the
   resolved values in its environment, writes each into the OS keystore and exits; values never
   touch disk or the shell.
5. **Record.** The backbone logs a `secrets_materialized` event — task, actor, and the **name
   list only**. Values and `op://` refs never enter the event log. This is the least-privilege
   audit trail.

**Keystore (macOS, primary).** One keychain item per secret: service `worklode:<task-id>`,
account `<NAME>`, created by the `lode` binary. Creator-application access means later unattended
reads by `lode` do not re-prompt. **Keystore (Linux, best-effort).** Same CLI surface backed by a
file encrypted to an ephemeral key held in ssh-agent — the decryption key lives only in agent
memory, never on disk. Weaker than the keychain; acceptable for v1.

**Re-materialization.** `/lode-resume` (operator present by definition) checks the keystore and
re-runs steps 1–5 if items are missing or the declaration changed. The lease-expiry sweeper is
server-side and cannot purge a laptop keystore; stale items are removed by the next local hook
that notices the lease is gone (resume, exit, or `lode doctor`).

## 4. Execution: `lode secrets exec` {#sec-4}

> **Amended by spec 042 §4.** For a templated entry, exec renders the template
> with the keystore credentials into `.worklode/secrets/<NAME>` (0600) and
> injects the exported env name pointing at that path instead of a value. The
> rendered file lives until purge (materialized lifetime = worktree lifetime);
> exec remains `syscall.Exec`.

> **Amended by ADR 048 §2.** `ExitWorktree` in the purge-trigger list below is
> conditional, unlike the others: exit purges only after a definite backbone
> "lease gone" answer, because 012 §4's multi-task session exits while its
> lease is still held. Removal, `/lode-done` and `/lode-block` stay
> unconditional and local-only.

> **Amended by ADR 050 §2.** The inherited half of the child's environment is
> stated: the child keeps the parent environment minus every credential-shaped
> name (a deny-list — `ANTHROPIC_API_KEY`, `AWS_*`, anything containing
> `TOKEN`/`SECRET`/`PASSWORD`/`AUTH`, ADR 050 §3), keeping the shell plumbing
> `PATH`, `HOME`, `TMPDIR` and the locale variables. Materialized names are
> injected after the scrub, so a credential-shaped secret name is unaffected.

```
lode secrets exec [--] <command> [args…]
```

Resolves the bound task from the worktree (the spec-008 `wt/<id>-<slug>` guard), reads that
task's items from the keystore, injects them as env vars into the child's environment, and execs.
Values exist only in the child process; nothing is written, logged, or echoed. Injected set =
exactly the task's materialized names — not the whole catalog, not the operator's shell
environment.

Supporting commands:

| Command | Purpose |
|---|---|
| `lode secrets catalog` | List catalog names + descriptions (authenticated; humans and planners) |
| `lode secrets status` | Names declared vs materialized for the bound task (names only) |
| `lode secrets pack` | Internal: child of `op run` at ceremony time; writes env → keystore |
| `lode secrets purge [--task <id>]` | Remove a task's keystore items; invoked by release hooks |

**Purge** rides the spec-008 release path: `ExitWorktree` / worktree removal / `/lode-done` /
`/lode-block` all purge the task's items. Materialized lifetime therefore equals worktree
lifetime, matching the lease.

## 5. The `lode-secrets` skill {#sec-5}

The convention travels as a skill loaded in **both** contexts the feature touches:

- **Writing plans:** every task in a plan lists the catalog names it needs (`lode secrets
  catalog` to browse); a task needing a secret that has no catalog entry is a plan-level finding
  — add the entry (deployment-repo PR) before the task is executable.
- **Executing tasks:** run credentialed commands via `lode secrets exec`; never probe `op`, ask
  the operator for values, or read `.worklode/secrets.env` expecting values (it holds
  references). A needed-but-unavailable secret is a **block signal**: `/lode-block` with a
  `missing-secret: NAME` reason, never improvisation.

The skill contains no `op://` refs — only the convention — so it is safe wherever skills live,
public or not. v1 ships it in the worklode plugin's skill set (008); once 016 is running it can
also be a pinned org-wide skill, which is what "always loaded" ultimately means there.

## 6. Degradation {#sec-6}

> **Amended by spec 042 §5.** Adds the templated-entry failure rows: catalog
> validation failures fail loudly server-side; render and env-name-collision
> failures at exec are block signals.

> **Amended by ADR 048 §3.** The "lease expires, worktree remains" row: items
> persist until the next exit of that worktree gets a definite "gone" answer,
> `/lode-resume` re-materializes, or removal purges. A server unreachable at
> exit leaves items in place with a warning.

| Condition | Behavior |
|---|---|
| Catalog endpoint unreachable at claim | Claim succeeds; brief warns `secrets: catalog unavailable`; agent treats any credentialed step as blocked. |
| Declared name not in catalog | Brief warning naming it; ceremony proceeds for the rest. |
| Operator declines consent | No materialization; brief records the declined names; credentialed steps block rather than prompt mid-run. |
| `op` not installed / not signed in | Ceremony fails fast at claim with an install/signin hint — while the operator is still present. |
| Keystore read fails unattended | `lode secrets exec` exits non-zero with the missing name; the skill directs the agent to `/lode-block`, not to retry or work around. |
| Lease expires, worktree remains | Items persist until the next local hook purges or re-materializes; server cannot reach the laptop keystore. |

## 7. Later phases (non-normative) {#sec-7}

- **v1.5 — remote executors.** Pack = resolve `.worklode/secrets.env` via the same single
  `op run` ceremony, encrypt the resolved set to the remote executor's public key (age), ship it
  with the task; the remote `lode secrets exec` decrypts on demand. The template format and the
  keystore-backed CLI surface are unchanged — only the backing store differs.
- **v2 — deferred grants.** A mid-task need for an undeclared secret triggers a mobile approval
  (Google Chat first; a dedicated app later) instead of stalling on a laptop prompt. Requires a
  channel for the operator to run the ceremony remotely; out of scope until remote execution
  exists.

## 8. Dependencies {#sec-8}

> **Amended by ADR 043 §2.** The external dependency is no longer a deployment
> repo hosting a catalog ConfigMap. It is the 1Password item
> `worklode-secrets-catalog`, plus External Secrets Operator and the
> per-environment `ClusterSecretStore` (`onepassword-hzdev-worklode` /
> `onepassword-hzprod-worklode`) that project it into the cluster.

- **004 (backbone)** — `secrets` field on Task; `secrets_materialized` event.
- **008 (plugin)** — claim/resume/exit hooks host the ceremony and purge; brief carries the
  declared/materialized names; the `lode-secrets` skill joins the plugin skill set.
- **016 (org-wide skills)** — the pin pattern and brief-integration shape this mirrors; doc
  frontmatter pins ride 025 in both.
- **External** — 1Password CLI (`op`) on every executing laptop; the deployment repo hosting the
  catalog ConfigMap; macOS keychain / ssh-agent.

## 9. Open questions {#sec-9}

- **Q17.1 — Consent granularity.** v1 consents to the non-baseline set with one yes/no. Is
  per-name consent worth the extra friction once catalogs grow?
- **Q17.2 — Staleness.** Materialized values live as long as the worktree. Long-lived worktrees
  hold long-lived copies; decide whether re-materialization should be forced after N days.
- **Q17.3 — Catalog visibility.** v1 serves the full catalog to any authenticated actor. Should
  visibility narrow per role/project once the actor model supports it?
- **Q17.4 — Catalog home.** The ConfigMap is deliberately minimal. If catalog churn grows,
  promote to a backbone table + admin CLI the same way 016 indexes git — without ever storing
  values.

## 10. Acceptance criteria {#sec-10}

> **Amended by ADR 050 §2.** Criterion 4 also requires the negative case: the
> same `lode secrets exec -- env` shows no credential-shaped variable inherited
> from the operator's shell — in particular no `ANTHROPIC_API_KEY` exported
> there — while the shell plumbing of ADR 050 §3's keep set is present.

1. `lode task add --secrets KUBECONFIG_HZDEV,OPENALEX_API_KEY …` stores the list; the task brief
   shows it; a name absent from the catalog surfaces as a brief warning, not an error.
2. `GET /api/v1/secrets/catalog` returns the catalog to an authenticated actor and 401s without
   a token; no unauthenticated route exposes any `op://` ref.
3. Claiming a task with one baseline and two declared secrets performs exactly **one** 1Password
   authorization, materializes all three into the keystore, and logs one `secrets_materialized`
   event listing the three names and no values.
4. In the claimed worktree, `lode secrets exec -- env` shows exactly the materialized names;
   the same command in a plain checkout fails the worktree guard. No secret value appears in any
   file, log, or event row.
5. Declining consent at claim still yields a held lease and injected brief; `lode secrets
   status` reports the declined names as unmaterialized.
6. `/lode-done` (and worktree removal) purges the task's keystore items; a subsequent
   `lode secrets exec` finds nothing.
7. On a machine without `op`, the ceremony fails at claim time with a remediation hint; nothing
   stalls later in the session.
