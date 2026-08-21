---
status: draft
issued: 2026-08-20
requires:
- 017-task-secrets.md
amends:
  "#sec-2":
  - 017-task-secrets.md#sec-1
  "#sec-3":
  - 017-task-secrets.md#sec-3
  "#sec-4":
  - 017-task-secrets.md#sec-4
  "#sec-5":
  - 017-task-secrets.md#sec-6
amendedBy:
  "#sec-2":
  - 043-secrets-catalog-home.md#sec-2
  - 047-loader-sensitive-secret-names.md#sec-2
---
# Spec 042 — Secret templates

## 0. Purpose & scope {#sec-0}

An OS keystore item is size-capped: macOS `security(1)` rejects an
`add-generic-password` command over 4096 bytes, so with go-keyring's base64
encoding the ceiling is roughly 2.9 KB of raw value, and Windows wincred caps
an item at 2560 bytes. Spec 017's own worked example, `KUBECONFIG_HZDEV`, is
routinely 3–6 KB, so `lode secrets pack` cannot store it at all — the
materialization path fails on exactly the asset the spec was written around.

This is not a keystore defect and is **not** worked around by chunking a value
across items. A value over the cap is a catalog-modelling error: the only
secret parts of a kubeconfig are the client credentials (a client cert+key
pair, or a bearer token), each well under the cap. The server URL, cluster CA
certificate, context, user and namespace names are configuration, not secrets.

This spec splits such a catalog entry into (a) a **plaintext template**,
shipped as a non-secret asset next to the catalog, and (b) one or more small
**credentials**, which are the only values that take the `op://` → keystore
path. `lode secrets exec` interpolates the credentials into the template,
writes the result to a file with mode 0600, and points the consuming
environment variable (e.g. `KUBECONFIG`) at that file instead of injecting a
multi-kilobyte value.

It amends spec 017: §2 here amends 017 §1 (catalog format), §3 amends 017 §3
(materialization), §4 amends 017 §4 (execution), §5 amends 017 §6
(degradation). Everything 017 says that is not restated here — the ceremony,
consent, the event-log posture, purge riding the release paths — holds
unchanged.

## 1. The split model {#sec-1}

A catalog entry is one of two shapes:

- **Plain** (spec 017 as written): one `op://` reference; the resolved value
  becomes one keystore item; `lode secrets exec` injects it as `NAME=value`.
- **Templated** (this spec): a plaintext template plus one or more named
  credentials, each an `op://` reference resolving to a value under the
  keystore cap. The keystore holds only the credentials. At execution the
  template is rendered — each placeholder replaced verbatim by its
  credential's value — into a file the entry's environment variable points at.

The template carries the same sensitivity as the catalog itself: vault
topology, cluster URLs and CA certificates are mildly sensitive and are served
only to authenticated actors (017 §1), but they are not secrets and never
enter the keystore, the ceremony, or the event log. The security invariant of
017 §0 is preserved exactly: worklode stores names and templates, never
values; 1Password remains the source of truth for every secret byte.

**Rejected: chunking.** Splitting an oversized value across N keystore items
hides the modelling error, multiplies items to keep consistent, and still
injects the full plaintext through the environment — a 6 KB env var is itself
hostile to `ps e`/`/proc/*/environ` hygiene and to the v1.5 remote packing
format. The cap is a useful forcing function: what exceeds it is scaffolding
wrapped around a credential, and the scaffolding is not entitled to keystore
protection.

## 2. Catalog declaration {#sec-2}

> **Amended by ADR 043 §2.** A `template` names a sibling key of the projected
> `worklode-secrets-catalog` **Secret**, not of a ConfigMap: the catalog and
> its templates are field labels on a 1Password item, extracted into that
> Secret per environment. The mechanism below is otherwise unchanged — only the
> object kind and how it gets provisioned.

> **Amended by ADR 047 §2.** The secret-name grammar this section borrows for
> `cred.<PLACEHOLDER>` and `env` now also denies loader-sensitive names
> (`LD_*`, `DYLD_*`, `PATH`, `IFS`, `ENV`, `BASH_ENV`, `PYTHONPATH`, …). An
> `env` name is what an entry is exported under at exec time, so the deny-list
> is load-bearing there.

Amends 017 §1. A templated entry replaces `ref` with a `template` key naming a
sibling file and one `cred.<PLACEHOLDER>` key per credential:

```toml
[KUBECONFIG_HZDEV]
description = "Kubernetes access to the hzdev cluster, for troubleshooting tasks"
template = "kubeconfig-hzdev.yaml"   # sibling ConfigMap key holding the template
env = "KUBECONFIG"                   # exported name at exec (default: the entry name)
cred.CLIENT_CERT = "op://Infrastructure/hzdev kubeconfig/client-cert"
cred.CLIENT_KEY = "op://Infrastructure/hzdev kubeconfig/client-key"
```

Key rules, extending the hand-rolled TOML subset (`internal/secrets.ParseCatalog`):

- `template` names another key **in the same ConfigMap**
  (`worklode-secrets-catalog`), holding the template text. `template` and
  `ref` are mutually exclusive; an entry must have exactly one of them.
- `cred.<PLACEHOLDER>` maps a placeholder to an `op://` reference. Placeholder
  grammar is the secret-name grammar, `^[A-Z][A-Z0-9_]*$`. At least one
  `cred.` key is required with `template`, and `cred.` keys are invalid
  without it. The parser change is a `strings.Cut` on the key — no dotted-key
  or multi-line-string machinery is added.
- `env` (optional, any entry shape) is the environment-variable name the
  entry is exported under at exec time; it defaults to the entry name and
  takes the same grammar. It exists because the consumer of a rendered file is
  usually a tool with a fixed env contract (`kubectl` reads `KUBECONFIG`, not
  `KUBECONFIG_HZDEV`); without it, `lode secrets exec -- kubectl …` cannot
  work unassisted. Two entries materialized for one task that resolve to the
  same exported name are an exec-time error naming both entries.
- `baseline` and `description` keep their 017 meanings on both shapes.

**Template syntax.** A placeholder is `{{ PLACEHOLDER }}` (inner whitespace
optional). Rendering is verbatim byte substitution — no escaping, no
conditionals, no nesting. Every placeholder in the template must have a
matching `cred.` key and every `cred.` key must be used by the template; any
other `{{` sequence is an error, so a typo fails catalog validation instead of
rendering a broken artifact. There is no escape for a literal `{{` in v1; no
motivating asset needs one.

**Why a sibling ConfigMap key, not an inline TOML string.** The template is a
multi-kilobyte, multi-line document. Inlining it would force multi-line-string
support into a deliberately minimal parser and reduce the template to an
escaped blob in review diffs. As a sibling key it stays a plain file: a
kubeconfig template is valid YAML an admin can read, lint, and diff in the
deployment repo's PR flow, and the ConfigMap (1 MiB cap) has room for many.
The server already mounts the ConfigMap to read `catalog.toml`
(`LODE_SECRETS_CATALOG_PATH`); template files are read from the same mount
directory, so no new deployment surface appears.

**Why not per-credential catalog entries.** Modelling `CLIENT_CERT` and
`CLIENT_KEY` as free-standing entries would leave composition — which
credentials belong to which template, and what file they render into — with
no owner. The entry is the unit of declaration, consent, and audit; a task
declares `KUBECONFIG_HZDEV`, not its parts.

**Serving.** `GET /api/v1/secrets/catalog` (unchanged route, auth, and guard)
inlines each templated entry's template text, exported name, and
placeholder → `op://` map in its response entry. The response stays a few KB;
a separate template endpoint would add a route and a failure mode to save
nothing. The server validates the catalog — template file present, placeholder
set ↔ `cred.` set equality — when it reads it, and a validation failure is a
500 with a log line naming the entry: the catalog is admin-controlled, and a
broken entry should fail loudly at the source rather than degrade per-claim.

## 3. Materialization {#sec-3}

Amends 017 §3. The ceremony is unchanged in shape — one consent for the
non-baseline set, one `op run`, one names-only event. What changes is what a
templated entry contributes:

- **Env file.** Each credential contributes one line to
  `.worklode/secrets.env`, under the **item name** `<NAME>__<PLACEHOLDER>`
  (e.g. `KUBECONFIG_HZDEV__CLIENT_KEY=op://…`). The double underscore keeps
  item names inside the existing name grammar, so pack, keystore validation,
  and the v1.5 remote packing format need no new cases.
- **Keystore.** `lode secrets pack` stores one item per credential under the
  item name. Each credential is well under every OS cap; the entry's size is
  no longer bounded by any single item. Storing the credentials as one
  JSON-encoded item was rejected: an RSA client cert+key pair can itself
  approach the macOS cap, which would re-create this defect one level down.
- **Consent and audit.** The consent prompt lists the entry once, by name and
  description — placeholders are plumbing, not a consent surface. The
  `secrets_materialized` event records **entry names only**, exactly as
  before; item names, templates, and refs stay out of the event log.
- **Manifest.** The local manifest (0600, outside the worktree) records, per
  materialized templated entry: its item names (purge's only enumeration,
  as before), its exported env name, and its **template text**. Persisting
  the template locally is what keeps `lode secrets exec` offline — exec never
  fetches the catalog. The template is catalog-sensitivity data in a 0600
  file already holding vault-adjacent names; no value ever enters it.
  Re-materialization (`lode resume`) refreshes it, so a catalog template edit
  propagates the same way a `ref` edit does.

## 4. Rendering & execution {#sec-4}

Amends 017 §4. For each materialized templated entry, `lode secrets exec`:

1. Fetches the entry's credential items from the keystore (a missing item is
   the existing block-signal failure).
2. Renders the manifest's template, substituting each placeholder's value.
3. Writes the result to `.worklode/secrets/<NAME>` in the worktree — directory
   mode 0700, file mode 0600, written to a temp file and renamed so concurrent
   execs never expose a partial file and the path stays stable.
4. Injects `<env>=<absolute path>` into the child environment (stripping any
   ambient assignment of the exported name, as 017 §4 already requires) and
   execs as before. Plain entries are untouched: still `NAME=value`.

The rendered path is stable and re-rendered on every exec: rendering is
microseconds of work, and re-rendering makes the file self-healing after
deletion, worktree moves, or re-materialization — there is no staleness state
to track. `.worklode/secrets/` is added to the repo's local git exclude the
same way `.worklode/secrets.env` is today, and `lode secrets status` reports
each templated entry's credentials and whether a rendered file exists.

### 4.1 Rendered-file lifetime {#sec-4.1}

The rendered file holds real secret bytes on disk, and `lode secrets exec`
ends in `syscall.Exec` — the process is replaced, so nothing survives to
clean up after the child. Two contracts were on the table: make exec
fork/wait so a cleanup hook can unlink the file when the child exits, or let
the file live until an explicit purge. **The file lives until purge.**

- **Deleting per-invocation requires fork/wait**, which changes process
  semantics every caller currently gets for free: the child is no longer the
  caller's direct child (PID expectations in scripts and hooks), signals and
  the exit code must be forwarded by a parent that must itself never die
  first — and a SIGKILLed parent leaks the file anyway, so purge must exist
  as the backstop regardless. Fork/wait would buy a shorter on-disk window at
  the cost of a changed contract plus the same cleanup machinery.
- **A stable file also outlives the exec on purpose.** A long-running child
  that spawns its own subprocesses (anything reading `KUBECONFIG` late)
  needs the path valid for its whole lifetime, which per-invocation cleanup
  cannot know.

So the lifetime contract is the one 017 already gives keystore items:
**materialized lifetime equals worktree lifetime**. `lode secrets purge`
unlinks each rendered file recorded in the manifest (by absolute path, so
`--task` purges from anywhere) alongside the keystore items, and purge
already rides every release path — `lode done`, `lode block`, worktree
removal, exit hooks. The residual exposure is a 0600 file, on the operator's
own single-user machine, inside a directory git ignores, for exactly the
window the same task's credentials sit in the local keystore — and on Linux
the 017 keystore is itself file-backed, so the marginal exposure is nil.
Q17.2's staleness question (force re-materialization after N days) covers the
rendered file for free, since re-materialization re-renders.

## 5. Degradation {#sec-5}

Amends 017 §6 with the templated-entry rows; every 017 row still applies.

| Condition | Behavior |
|---|---|
| `template` names a missing ConfigMap key, or placeholder set ≠ `cred.` set | Catalog read fails server-side: 500 + log naming the entry. Claims degrade per 017 ("catalog unavailable"); the fix is an admin PR. |
| Credential item missing from keystore at exec | Existing 017 row: exit non-zero naming it; the skill directs `lode block`, never a workaround. |
| Rendered-file write fails (permissions, disk) | Exec exits non-zero naming the path; block signal, not a retry loop. |
| Two entries export the same `env` name for one task | Exec exits non-zero naming both entries; fix is the task's declaration or the catalog. |
| Manifest predates this spec (no template recorded) | Templated entry is reported unmaterialized; `lode resume` re-runs the ceremony. |

## 6. Acceptance criteria {#sec-6}

1. A catalog with the §2 example parses; a template using an undeclared
   placeholder, an unused `cred.` key, or a missing template file is rejected
   with the entry and placeholder named.
2. `GET /api/v1/secrets/catalog` returns the templated entry with template
   text, exported name, and placeholder refs to an authenticated actor;
   auth posture is unchanged from 017.
3. Claiming a task that declares a templated entry with two credentials
   performs one 1Password authorization, stores two keystore items
   (`NAME__…`), each under every OS cap, and logs one `secrets_materialized`
   event naming the entry only.
4. In the worktree, `lode secrets exec -- sh -c 'wc -c <"$KUBECONFIG"'` sees a
   rendered file of the full (>4 KB) size with mode 0600 under
   `.worklode/secrets/`, git reports the tree clean, and no secret value
   appears in any env var, log, event row, or the manifest.
5. `lode secrets purge` (and every 017 release path) removes the keystore
   items and the rendered file; a subsequent exec fails with the
   materialize hint.
6. Deleting the rendered file by hand and re-running exec re-renders it; the
   ceremony re-run after a catalog template edit updates the manifest and the
   next exec renders the new text.
