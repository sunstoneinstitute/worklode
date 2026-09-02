---
status: draft
covers:
- docs/specs/042-secret-templates.md#sec-1
- docs/specs/042-secret-templates.md#sec-2
- docs/specs/042-secret-templates.md#sec-3
- docs/specs/042-secret-templates.md#sec-4
- docs/specs/042-secret-templates.md#sec-4.1
- docs/specs/042-secret-templates.md#sec-5
- docs/specs/042-secret-templates.md#sec-6
---
# Secret templates — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.

**Goal:** implement spec 042 — a catalog entry declared as a plaintext
template plus `cred.<PLACEHOLDER>` references, materialized as one keystore
item per credential (`NAME__PLACEHOLDER`), rendered by `lode secrets exec`
into `.worklode/secrets/<NAME>` (0600) with the exported env var pointing at
the file, and unlinked by `lode secrets purge`.

**Shape of the change.** Everything stays in the existing packages:
`internal/secrets` (parse, validate, render, manifest, purge),
`internal/model` (wire fields), `internal/api` (serve template text),
`internal/cmd` (ceremony, pack, exec, status). No new routes, no migration,
no store change — the `secrets_materialized` event keeps its names-only,
entry-granular payload. Values keep touching exactly two places: pack's
inherited environment and exec's child environment, plus (new, by design,
spec 042 §4.1) the rendered file itself.

**Manifest reshape (tasks 3–4).** `secrets.Manifest` gains
`Entries []ManifestEntry` where `ManifestEntry` is
`{Name, Env, Template string; Items []string; Rendered string}` —
`Items` are the keystore item names (purge's only enumeration), `Env` the
exported name, `Template` the text (what keeps exec offline), `Rendered` the
absolute rendered path (set by exec, unlinked by purge). A plain entry is
`{Name: N, Env: N-or-alias, Items: [N]}` with no template. The top-level
`Materialized`/`Declined` name lists stay entry-granular so
`secretsSatisfied`, `lode secrets status`, and the event payload keep their
meaning; a manifest without `Entries` (pre-042) makes templated entries
report unmaterialized, per spec 042 §5.

## Tasks

### Task 1 — Catalog parser: template, env, and cred keys

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
```

`internal/secrets`: teach the catalog model the templated entry shape and add
the template scan/validate/render primitives (spec 042 §2).

- `catalog.go`: `Entry` gains `Template string` (the sibling key in the
  projected `worklode-secrets-catalog` Secret named by the `template` TOML
  key), `Env string`, and
  `Creds []Cred` (`Cred{Placeholder, Ref string}`, file order).
  `ParseCatalog` accepts `template = "..."`, `env = "..."`, and
  `cred.<PLACEHOLDER> = "op://..."` keys (`strings.Cut` on `.` — no dotted-key
  machinery). Enforce: `template`/`ref` mutually exclusive, exactly one
  present per entry (replaces the current "ref is required" final check);
  `cred.` keys require `template` and at least one is required with it;
  placeholder and `env` values must match `ValidName`; duplicate placeholders
  rejected.
- New `template.go`: `Placeholders(text string) ([]string, error)` scans
  `{{ NAME }}` (inner whitespace optional) and errors on any `{{` that does
  not form a valid placeholder; `ValidateTemplate(e Entry, text string) error`
  checks placeholder set == `cred.` set both directions, naming the entry and
  the offending placeholder; `Render(text string, values map[string]string)
  (string, error)` does verbatim substitution and errors on a missing value.
- `names.go`: `ItemName(entry, placeholder string) string` returning
  `ENTRY__PLACEHOLDER`, and `Items(e Entry) []string` (a plain entry's items
  are `[e.Name]`).

Tests (`catalog_test.go`, new `template_test.go`): the spec 042 §2 example
parses into the expected Entry; each rejection (ref+template, cred without
template, template without cred, bad placeholder grammar, bad env, duplicate
cred); `Placeholders` on whitespace variants and on a stray `{{`;
`ValidateTemplate` both mismatch directions; `Render` round-trip.

- [x] Extend `Entry` + `ParseCatalog` with tests
- [x] Add `template.go` + tests
- [x] Add `ItemName`/`Items` + tests
- [x] `go test -trimpath ./internal/secrets`

### Task 2 — Serve templates through the catalog endpoint

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Wire shape and server side (spec 042 §2 "Serving").

- `internal/model/secrets.go`: `SecretCatalogEntry` gains
  `Template string` (the template **text**, not the key name),
  `Env string`, and `Creds map[string]string` (placeholder → `op://` ref),
  all `omitempty` so plain entries are wire-identical to today.
- `internal/api/secrets.go` (`secretsCatalog`): after `ParseCatalog`, for
  each templated entry read the named sibling file from
  `filepath.Dir(s.cfg.SecretsCatalogPath)` and run
  `secrets.ValidateTemplate`; any missing file or mismatch is a 500 with a
  log line naming the entry (admin-controlled input fails loudly, spec 042
  §5). Reject a `template` value containing a path separator or `..` — it
  names a key in the projected Secret, never a path. Populate the new wire
  fields.
- Check `plugins/obsidian/src/api/types.ts`: if it hand-mirrors
  `SecretCatalogEntry`, update the mirror (worklode-obsidian-mirror skill);
  if it does not, this step is a no-op.

Tests (`internal/api`): catalog response carries template text, env, and
creds for a templated entry alongside an unchanged plain entry; missing
template file → 500; placeholder/cred mismatch → 500; auth posture untouched
(existing guard tests keep passing).

- [x] Extend `SecretCatalogEntry` (+ obsidian mirror check)
- [x] Template loading + validation in `secretsCatalog`, with tests
- [x] `go test -trimpath ./internal/api ./internal/model`

### Task 3 — Ceremony and pack materialize credential items

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

Materialization (spec 042 §3): item-per-credential in the keystore, manifest
carries the entry structure, consent and the event stay entry-granular.

- `internal/secrets/manifest.go`: add `ManifestEntry` and
  `Entries []ManifestEntry` as described in the plan header; `SaveManifest`
  validates entry names, env names, and item names with `ValidName`.
- `internal/secrets/envfile.go`: `WriteEnvFile` writes one line per **item**
  — `NAME=ref` for plain entries, `NAME__PLACEHOLDER=ref` per credential for
  templated ones.
- `internal/cmd/secretsceremony.go`: build `secrets.Entry` values from the
  wire response including `Template`/`Env`/`Creds`; consent display is
  unchanged (entry name + description). Instead of passing `--names`, the
  ceremony writes the **pending manifest** (entries, env names, templates,
  declined — everything but `Rendered`/`At`) to a 0600 temp file and invokes
  `op run … lode secrets pack --task <id> --plan <file>`; the event recorded
  on success still carries entry names only.
- `internal/cmd/secrets.go` (`pack`): replace `--names`/`--declined` with
  `--plan <file>` (pack is hidden/internal; grep `docs/agent-surfaces.md` and
  `TestAgentSurfaces` to confirm nothing external names the old flags). Pack
  reads the plan, requires every item name resolved in its environment,
  `Put`s each item, prunes previously-materialized items the new plan drops
  (existing logic, now over item names), and saves the manifest.
- `secretsSatisfied` (`secretsceremony.go`): verify every item of every
  manifest entry is fetchable; declared-name accounting is unchanged.

Tests: env file contains item-name lines for the templated entry; pack
stores one keystore item per credential (mock keyring), saves the manifest
with entries and templates, and prunes dropped items; ceremony records
entry names only; `secretsSatisfied` false when one credential item is gone.

- [x] Manifest reshape + envfile item lines, with tests
- [x] Ceremony builds the plan file; pack takes `--plan`, with tests
- [x] `go test -trimpath ./internal/cmd ./internal/secrets`

### Task 4 — Exec renders, purge unlinks, status reports

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

Rendering and lifetime (spec 042 §4, §4.1, §5).

- `internal/secrets`: `RenderEntry(worktreeDir string, e ManifestEntry,
  values map[string]string) (string, error)` — render the template, create
  `<worktreeDir>/.worklode/secrets/` (0700), write a temp file (0600) and
  rename to `.worklode/secrets/<NAME>`, return the absolute path.
- `internal/cmd/secrets.go` (`exec`): iterate manifest entries; fetch each
  entry's items (missing item keeps the existing block-signal error); plain
  entries inject `Env=value`, templated entries render and inject
  `Env=<absolute path>`, updating the entry's `Rendered` in the manifest.
  Before exec, fail on two entries resolving to the same exported name,
  naming both (spec 042 §5). `childEnv` strips the exported names (aliases
  included), not just entry names.
- `internal/secrets/keystore.go` (`PurgeTask`): unlink each entry's
  `Rendered` path (missing file is fine) before removing items and the
  manifest.
- `internal/cmd/secretsceremony.go` (`excludeSecretsEnv`): also append
  `.worklode/secrets/` to the repo-local git exclude; add the same line to
  this repo's `.gitignore` next to `.worklode/secrets.env`.
- `lode secrets status`: for a templated entry, report per-credential item
  state plus whether the rendered file currently exists.

Tests: exec injects `KUBECONFIG=<path>` (via the stubbed `execFn`) and the
file holds the rendered content at 0600; a >4 KB template round-trips
(spec 042 §6.4); deleting the file and re-running exec re-renders it; env
collision fails naming both entries; purge unlinks the rendered file and
items; `git status` clean in a worktree fixture after rendering.

- [x] Render helper + exec wiring, with tests
- [x] Purge unlinks rendered files; gitignore/exclude lines, with tests
- [x] Status rows for templated entries, with tests
- [x] `make test`

### Task 5 — Deployment example, skill, and surfaces

```yaml
kind: chore
priority: medium
blockedBy: [4]
```

Roll the new shape out to the surfaces that describe the old one.

- `deploy/secrets-catalog.example.toml`: extend the commented, never-deployed
  example with a templated entry — the spec 042 §2 TOML plus a commented
  sibling `kubeconfig-hzdev.yaml` key — so the admin adding WL-103's real
  entry has the shape in front of them. Per ADR 043, the real templated entry
  and its sibling template key are added as fields on the
  `worklode-secrets-catalog` 1Password item, not committed here.
- `plugins/claude/lode/skills/lode-secrets/SKILL.md`: the "capped at
  ~2.5-3 KB, has to be split" paragraph now states the mechanism exists —
  declare the entry name as usual; exec exports the entry's env name pointing
  at a rendered file; never read or commit `.worklode/secrets/`. Follow the
  worklode-lode-plugin skill (codex mirror regeneration, `TestAgentSurfaces`).
- `docs/agent-surfaces.md` checklist pass for the pack flag change (task 3)
  and the new `.worklode/secrets/` path.

- [x] Deployment example
- [x] Skill text + codex mirror
- [x] Agent-surfaces checklist, `make test`
