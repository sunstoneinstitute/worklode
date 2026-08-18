# Review tooling refresh

Research note, 2026-08-16. Question: PR #29 drafted two specs — a *reading
diff* built on [`boldsoftware/meat`](https://github.com/boldsoftware/meat) and
a *review surface* whose interim client was
[`ymansurozer/galley`](https://github.com/ymansurozer/galley). Both were
assessed in early August. What does the landscape look like now, and is either
choice still the best available?

Method: five parallel primary-source sweeps — the two PR-29 candidates, the
agent-review-desk category, self-hostable review stores, document/prose
annotation, and Quarto/markdown rendering under Go control. Claims come from
repository source, `gh api` metadata, official docs, and release pages; no
claim rests on a blog write-up about a project that is not the project's own.
`crit` was read from its actual source tree at v0.18.4 (`0b9c5461`), which is
checked out locally as an installed Claude plugin. Conclusions marked
**Synthesis** are ours, not claims by the projects surveyed.

Where a claim was cheap to test rather than merely read, it was tested: §6's
goldmark and Quarto findings marked **[verified]** were reproduced locally
against Worklode's own spec files.

Caveats: star counts are as of 2026-08-16 and approximate. `criticmarkup.com`
refused connections from the research environment, so CriticMarkup syntax is
cited from the canonical GitHub org instead of the site. Production
self-hosting support for `hypothesis/h` could not be confirmed either way. The
local Quarto used for §6 was 1.9.38, one minor behind current stable — the class
vocabulary matches 1.10.18 source, but the DOM evidence was not re-rendered on
1.10.18. The arm64 status of Quarto's container images is inferred from the
build workflow, not from manifest inspection.

---

## Short answer

**meat still holds up; galley does not; and PR #29 missed the tool that most
closely matches what 030 wants to build.**

- **meat** is viable and unchanged in shape — Apache-2.0-intent, zero external
  dependencies, a clean `Abridge(ctx, model, req)` entry point with a pluggable
  `Model` interface. Three things moved: the default model flipped from
  Anthropic to OpenAI, oversized diffs now chunk rather than error, and it has
  **no releases or tags at all**, so any embed pins a pseudo-version against an
  unversioned API. It has also been quiet since 2026-08-03 with 15 unmerged PRs.
- **galley** should be dropped as the interim client. It is a 13-star personal
  project, static since 2026-07-21, requires Node ≥22, and ships an
  explicitly unauthenticated desk. Its `ReviewResult` contract remains the
  best-written prose spec found and is still worth copying as a *contract*.
- **[`crit`](https://github.com/tomasz-tomczyk/crit) is the finding.** It is
  **Go, MIT, ~903 stars, released weekly**, reviews markdown documents *and*
  diffs, embeds its own UI with zero Node at build or runtime, has a documented
  JSON contract and a shell-hook integration seam, syncs bidirectionally with
  GitHub PRs and GitLab MRs, and its companion **`crit-web` self-hosts as
  Elixir + Postgres + generic OIDC** — no Node service anywhere. It solves the
  document-review case that Stig expects to dominate, and it already implements
  the content-anchored drift recovery that 030 §2.1 specifies.

**Synthesis.** The shape of 030 survives — own the store, copy a contract, run
someone else's desk as a client — but the client should be `crit`, not galley,
and the contract worth copying is now a blend: crit's `Comment` for anchoring,
galley's `ReviewResult` for round semantics.

---

## 1. Status refresh on the PR-29 candidates

### 1.1 meat — still embeddable, still unversioned

Metadata from [`gh api repos/boldsoftware/meat`](https://github.com/boldsoftware/meat):
~2,013 stars, created 2026-07-01, last push **2026-08-03** (`f39f41df add
LICENSE`), not archived, 4 open issues and **15 open pull requests**.

**Dependencies: still none.**
[`go.mod`](https://raw.githubusercontent.com/boldsoftware/meat/main/go.mod) is
28 bytes — `module meat.dev`, `go 1.24.13`, zero `require` blocks, and there is
no `go.sum` in the tree. `meat/anthropic.go` states the intent: "It uses only
the standard library so meat.dev has no third-party dependencies."

**License is muddier than PR #29 assumed.** The
[LICENSE](https://raw.githubusercontent.com/boldsoftware/meat/main/LICENSE)
file added on 2026-08-03 contains the Apache-2.0 *boilerplate header* — "Bold
Software, Inc. / Licensed under the Apache License, Version 2.0" — not the full
license body, which is why the GitHub API reports `spdx_id: NOASSERTION`. The
intent is Apache-2.0; the automated classification is not. Worth raising
upstream before we depend on it.

**Public API surface.** Nothing is under `internal/`. The importable package is
`meat.dev/meat`; the CLI is `meat.dev/cmd/meat`. Embedding is explicitly
supported in the package doc: "The package is provider-agnostic: callers supply
a Model." From
[`meat/meat.go`](https://github.com/boldsoftware/meat/blob/main/meat/meat.go):

```go
func Abridge(ctx context.Context, model Model, req Request) (*Result, error)

type Request struct {
    RepoRoot    string           // read-only tool sandbox; "" disables read_file/grep
    UnifiedDiff string           // required
    MaxTurns    int              // 0 → 24
    Progress    func(msg string) // per turn and per tool call; must not block
}

type Result struct {
    SmartDiff    string `json:"smart_diff"`
    Summary      string `json:"summary"`
    InputTokens  int    `json:"input_tokens"`
    OutputTokens int    `json:"output_tokens"`
}
```

and from [`meat/model.go`](https://github.com/boldsoftware/meat/blob/main/meat/model.go):

```go
type Model interface {
    Generate(ctx context.Context, system string, messages []Message, tools []Tool) (*Response, error)
}
```

Also exported: `ResolveModel`, `NewModelFromEnv`, `NewAnthropicFromEnv`,
`NewOpenAIFromEnv`, `ElisionLine(raw, abridged) string`, and `RubricHash()
string` — an 8-byte hex digest of the entire model-visible prompt surface,
documented as "Callers that cache Abridge results should mix it into their
cache key" ([`meat/rubric.go`](https://github.com/boldsoftware/meat/blob/main/meat/rubric.go)).
Spec 029 §5's cache identity already does exactly this.

**What changed since PR #29 was drafted.**

| Change | Impact on 029 |
|---|---|
| Default model flipped to OpenAI: `DefaultModel = DefaultOpenAIModel = "gpt-5.6-sol"`, `DefaultAnthropicModel = "claude-opus-4-8"` ([`meat/openai.go`](https://github.com/boldsoftware/meat/blob/main/meat/openai.go), [`meat/anthropic.go`](https://github.com/boldsoftware/meat/blob/main/meat/anthropic.go)) | Cosmetic — 029 §2 requires `LODE_READING_MODEL` to be set explicitly, so we never take the default |
| Oversized diffs now chunk instead of erroring (`chunk.go`, 2026-08-02) | 029 §3's `too_large` ceiling still needed, but the threshold moves: `maxDiffBytes` 400 KB per run, `maxTotalDiffBytes` 4 MB hard stop, `maxChunks` 32 |
| "Move chunk bookkeeping out of the exported Request" (2026-08-02) | Deliberate source-compatibility work for embedders — a good sign |
| Still **zero releases and zero tags** | Any embed is a pseudo-version pin against an API with no stability promise |

**Two gaps 029 should know about.**

1. **The edit plan is not exposed.** `editPlan`, `compiledPlan`, `lineFold` and
   friends are all unexported in `meat/editplan.go`; `Result` carries only the
   rendered `SmartDiff`. The provability argument lives in meat's *compiler*
   (plan applied to immutable input), not in an artifact a downstream consumer
   can re-verify. Spec 030 §4.1 recovers `skimBlocks` by line-diffing
   `smart_diff` against the raw diff, and the PR description suggests "029
   simply retaining meat's edit plan" would be cleaner. **That option does not
   exist today** — it needs an upstream change or a fork.
2. **The wall-clock budget is not settable.** `abridgeBudget` is a package-level
   `var` (4 minutes) intended for tests, not an embedder knob.

Also worth flagging: open PR
[#15](https://github.com/boldsoftware/meat/pull/15) "Harden read_file against
symlink escapes" is unmerged, so symlink escape from `RepoRoot` is a known
open gap. `grep` shells out to `git grep`, so the tarball root in 029 §3 must
be a git repo with `git` on PATH.

**Provenance.** `boldsoftware` also owns
[shelley](https://github.com/boldsoftware/shelley) (~627 stars, active coding
agent) and [sketch](https://github.com/boldsoftware/sketch) (~702 stars,
**archived** since 2026-01). meat's docs name "embedders such as Shelley", but
`shelley/go.mod` has no `meat.dev` requirement and a code search returns zero
hits — the embedding story is stated intent, not shipped integration.
**Synthesis:** if we embed meat we may be its first real embedder, which is
both the risk and the opportunity.

### 1.2 galley — drop it as the client, keep the contract

[`gh api repos/ymansurozer/galley`](https://github.com/ymansurozer/galley): **13
stars**, 3 forks, created 2026-06-04, last commit **2026-07-21**, MIT,
TypeScript, 0 open issues. The npm package is
[`galley-diff`](https://registry.npmjs.org/galley-diff) (not `galley`), latest
**0.7.4** published 2026-07-21. `package.json` declares `"engines": {"node":
">=22"}` — v0.7.3 dropped EOL Node 20 — plus `pnpm` and a runtime requirement on
`git`, and on `gh` for PR mode.

**Auth is the disqualifier for anything but a local desk.** The only protection
is an `Origin`-header guard that deliberately permits requests with no `Origin`
so the CLI works. The project's own README says: "**The desk API is
unauthenticated.** Anyone who can reach the bound address can drive the desk —
run your configured editor command, stage and reset changes, mutate the git
index, read any file in the repo." Spec 030 §7.1 already anticipated this and
required the bridge to refuse non-loopback desks; that reasoning was correct.

**The contract is still the best-written one found.** It lives as a single
`SPEC` string in
[`src/spec.ts`](https://github.com/ymansurozer/galley/blob/main/src/spec.ts),
printed by `galley spec`, with TypeScript types in
[`src/types.ts`](https://github.com/ymansurozer/galley/blob/main/src/types.ts).
There is **no JSON Schema file** — a consumer hand-transcribes or shells out.
`ReviewResult` is `{session, repoRoot, mode: "repo"|"file"|"pr", target?, base?,
staged, head, baseDiffHash, accepted[], rejected[], requestedChanges[],
overallNote?, stagedFiles[], approvedFiles[], openQuestions[], artifacts{...}}`,
where each item is `{path, lineNumber, side, title|body}`. `AwaitEvent` is a
tagged union of `{kind:"review", result}` and `{kind:"question", question,
questions}`.

Two ideas in it are genuinely worth keeping regardless of which desk we run:
**`baseDiffHash`** (a hash of the exact diff reviewed, making a stale review
detectable) and the **split between a live read-only question channel and the
Send batch**. Spec 030 §3 already builds on the second.

Breaking changes since PR #29 was drafted: none. The last marked breaking
changes were v0.3.0 and v0.4.0 in June; v0.7.0 (2026-07-15) removed embedded
file contents from state in favour of a `GET /api/file-contents` fetch.

**Synthesis.** Galley's problem is not quality, it is bus factor and reach: one
author, 13 stars, a month of silence, and a Node-22 runtime we would be asking
every reviewer to install. Spec 030 §1.2 already costed "port the UI" as the
right eventual answer. The refresh changes that calculus — there is now a
better-maintained desk that does not need porting.

---

## 2. crit — the candidate PR #29 did not evaluate

[github.com/tomasz-tomczyk/crit](https://github.com/tomasz-tomczyk/crit) ·
[crit.md](https://crit.md) · MIT · **Go**

| | |
|---|---|
| Stars / forks | ~903 / 64 ([`gh api`](https://github.com/tomasz-tomczyk/crit)) |
| Created / last push | 2026-02-16 / 2026-08-15 |
| Latest release | **v0.18.4**, 2026-08-05; ~monthly minors, frequent patches |
| Open issues | 10; multiple outside contributors in recent release notes |
| License | MIT (`LICENSE` line 1: "MIT License / Copyright (c) 2026 Tomasz Tomczyk") |

### 2.1 Why it matters here

**It reviews documents as a first-class mode.** From the
[README](https://github.com/tomasz-tomczyk/crit#readme): "For agents, plans and
code are all the same — it's just text, but for us, humans, reviewing generated
plans and reviewing web application are two very different activities." Four
modes: `crit plan.md` (rendered markdown with review UI), `crit` (auto-detected
git diff), `crit http://localhost:3000` (proxies a running app and overlays a
review interface), `crit landing.html` (static artifact). Given Stig's
expectation that documents get reviewed more than code, a tool whose *headline
example* is commenting on a plan document is better aligned than one built
diff-first.

**It proves the no-Node thesis rather than violating it.**
`web/embed.go` line 5 is `//go:embed *.html *.css *.js *.png *.svg *.ico
*.webmanifest`, and the minified browser libraries — `markdown-it.min.js`,
`highlight.min.js`, `mermaid.min.js`, `diff-match-patch.min.js` — are
**committed to the repo** alongside ~137 hand-written vanilla-JS files.
`package.json` exists, but `copy-deps.js` only copies those libraries into
`web/`; `go build` needs no Node, no bundler, no npm. README: "Everything runs
locally via one single binary." Its `go.mod` requires seven small modules
(`qrterminal/v3`, `x/sys`, `x/term`, `yaml.v3`, `rsc.io/qr`, `x/net`,
`gorilla/websocket`).

**Synthesis.** This is a directly transferable pattern for Worklode's cockpit:
if we ever need client-side markdown rendering or diagrams, vendor the minified
library into git and `go:embed` it. It costs a few MB of repo and zero build
dependencies, and it is exactly how `internal/ui` already treats its
self-hosted HTMX.

### 2.2 The anchoring model — 030 §2.1, already built

From `internal/session/session.go`, `type Comment` (abridged to the
anchor-relevant fields):

```go
StartLine   int    `json:"start_line"`
EndLine     int    `json:"end_line"`
Side        string `json:"side,omitempty"`
Quote       string `json:"quote,omitempty"`
QuoteOffset *int   `json:"quote_offset,omitempty"`
Anchor      string `json:"anchor,omitempty"`   // full text of the commented lines
Drifted     bool   `json:"drifted,omitempty"`
HeadSHA     string `json:"head_sha,omitempty"`
FocusKey    string `json:"focus_key,omitempty"` // "" | "pr:<n>" | "range:<base>..<head>"
DiffScope   string `json:"diff_scope,omitempty"`
Scope       string `json:"scope,omitempty"`     // "line" | "file" | "review"
```

Line numbers are a hint; **the `anchor` string is the identity**.
`internal/session/watch.go` implements a four-stage recovery ladder in
`verifyAndCorrectPosition`: (1) the LCS-remapped position still matches the
anchor exactly; (2) `anchorSimilar` — containment with a `minLen >= 8` guard,
or a Levenshtein ratio ≥ 0.7 — catches in-place edits; (3) `findAnchorInLines`
scans the whole file and, on multiple hits, picks the one nearest the
LCS-predicted line; (4) not found anywhere → `Drifted = true`, keep the LCS
position and surface it. The `minLen >= 8` gate exists so trivial anchors like
`}` or `return nil` do not match every longer line containing them.

`FocusKey` is a second idea worth taking: comments are scoped to the *view*
they were authored in (`range:<base_sha>..<head_sha>`, full 40-char SHAs), so a
comment written against one diff range cannot leak into another.

Live mode adds a second anchor kind on the same `Comment` — `DOMAnchor{pathname,
css_selector, tag_chain, accessible_name, role, landmark, outer_html,
viewport_*}` — which is the same architectural bet 030 §2 makes: one review
object, several anchor kinds, one thread store.

**Synthesis.** 030 §2.1's document/change anchor split is right, and crit is the
existence proof that content-anchored recovery with an explicit `obsolete`
(here, `drifted`) terminal state works in practice. The `minLen` guard and the
"nearest to predicted position" tie-break are details worth copying verbatim.

### 2.3 The contract and the integration seam

The wire format (`CritJSON`, `internal/session/session.go:496`) is `{branch,
base_ref, updated_at, review_round, share_*, review_comments[], cli_args[],
files: map[path]{status, file_hash, comments[]}, active_diff_scope,
review_type, origin, story}`. Three comment scopes: `line` and `file` under
`files.<path>.comments` (file comments use `start_line: 0`), `review` in the
top-level array.

The contract is documented for agents in the shipped
`skills/crit-cli/SKILL.md`, which instructs explicitly: "When edits shift line
numbers, locate content by `anchor` rather than trusting
`start_line`/`end_line`." Bulk agent input is a JSON array of `{file|path, line
(int or "45-47"), end_line, body, author, scope, reply_to, resolve}` fed to
`crit comment --json --file`.

**Command hooks are the clean seam.** From
[`docs/agent-hooks.md`](https://github.com/tomasz-tomczyk/crit/blob/main/docs/agent-hooks.md):
crit fires `on_finish_approved` / `on_finish_unresolved` (optionally suffixed
`:files`, `:diff`, `:story`, `:live`, `:preview`) as `inline:<cmd>` or
`file:<path>`, piping a JSON payload to stdin and setting `CRIT_*` env vars
(`CRIT_REVIEW_PATH`, `CRIT_APPROVED`, `CRIT_UNRESOLVED_COUNT`,
`CRIT_COMMENTS_JSON`, `CRIT_COMMENTS_UNRESOLVED_JSON`, …). Hooks run
synchronously after the review file is persisted, are capped at 60 seconds,
never block finish, and project-level hooks sit behind a trust prompt.

**Synthesis.** `on_finish_* = file:.crit/hooks/lode-review-ingest.sh` running
`lode review ingest < /dev/stdin` is a far simpler bridge than 030 §7.1's
`galley await` long-poll loop: no polling, no Node, no forked process holding a
token, and upstream releases stay an upgrade rather than a merge. The read-only
question channel 030 §3 wants has no crit equivalent, so that piece would have
to be ours regardless.

### 2.4 crit-web — a self-hostable separate service, and not a Node one

[github.com/tomasz-tomczyk/crit-web](https://github.com/tomasz-tomczyk/crit-web)
— MIT, **Elixir/Phoenix**, ~28 stars, pushed 2026-08-16. Its README documents
self-hosting via `ghcr.io/tomasz-tomczyk/crit-web` with **PostgreSQL 17+**,
migrations run automatically on startup, and these environment variables among
others: `SELFHOSTED=true`, `SECRET_KEY_BASE`, `DATABASE_URL`, and — the one
that matters — **`OAUTH_CLIENT_ID` / `OAUTH_CLIENT_SECRET` / `OAUTH_BASE_URL`
for generic OIDC** ("Google, GitLab, Okta, etc."), alongside a GitHub OAuth
path. `LOCAL_REGISTRATION_ENABLED=false` closes registration after seeding
trusted accounts.

The share API is a small HTTP surface: `POST /api/reviews`, `PUT
/api/reviews/:token`, `DELETE /api/reviews`, `GET /api/reviews/:token/comments`.
In `SELFHOSTED` mode all `/api/*` routes except auth and health require a
bearer token, and `/r/:token` redirects unauthenticated visitors to login
(`internal/share/share_selfhosted_integration_test.go`). The CLI points at it
with `CRIT_SHARE_URL` / `--share-url` / `share_url` in config; setting it to
`""` disables sharing entirely.

### 2.5 Limits, honestly

- **Not importable as a Go library.** Everything is `cmd/crit` + `internal/*` +
  `web` + `integrations`. MIT means copying an algorithm is fine; importing a
  package is not possible. Integration is subprocess, JSON file, or HTTP.
- **Pre-1.0** (v0.18.x), single primary author, no stability promise. Better
  bus factor than galley, not infrastructure-grade.
- **The local server is unauthenticated**, binding `127.0.0.1` by default;
  non-loopback requires `CRIT_ALLOW_UNAUTHENTICATED_NETWORK`. Cross-site POSTs
  are rejected via `Sec-Fetch-Site` (v0.18.2). Same operator-machine assumption
  as galley, with better hardening.
- **No Quarto, no Pandoc attributes.** `detectFileType`
  (`internal/session/session.go:1157`) maps only `.md`, `.markdown`, `.mdown`
  to markdown — a `.qmd` file renders as *code*. The vendored `markdown-it` has
  no `markdown-it-attrs` plugin, so `{#sec-N}` is not honored today. This is the
  one place crit does not reach our bonus requirement.

### 2.6 Story mode — a rival to 029's reading diff, and a contrast worth drawing

[`docs/story-mode.md`](https://github.com/tomasz-tomczyk/crit/blob/main/docs/story-mode.md)
documents an optional LLM narrative layer over a diff: a prologue plus ordered
chapters grouping hunks by theme, with a second-class `support[]` bucket for
lockfiles and generated churn. Agents emit only `prologue`, `chapters`,
`support`; crit fills `version`, `generated_at`, `base_sha`, `head_sha`,
`scope_fingerprint` and a `coverage` self-validation report. Hunks are
referenced by `(file_path, old_start)` pairs. The guard rail is stated
explicitly: "It is an explainer, not a reviewer" — there is deliberately no
verdict field.

**Synthesis.** Story and meat solve the same problem with opposite guarantees.
Story is generative narrative *about* the diff, cheap to validate by coverage
but not constrained to be true. meat's reading diff is a provable subset of the
input, which is what 029 §1 rests on when it puts a rendering in front of a
gate. They compose rather than compete: story orients, the reading diff
abridges. If we adopt crit we get story for free and should still build 029.

---

## 3. The wider agent-review-desk category

Power-law distributed, and the two leaders are not Go.

- **[modem-dev/hunk](https://github.com/modem-dev/hunk)** — TypeScript, MIT,
  **~8,442 stars**, created 2026-03-17, pushed 2026-08-16, v0.18.2 on
  2026-08-14 with releases every few days. "Review-first terminal diff viewer
  for agentic coders." Terminal UI, installable as a standalone binary
  (Homebrew/mise/Nix), so Node is avoidable at runtime. Has a loopback daemon on
  `127.0.0.1:47657` driven by `hunk session <cmd>` plus `hunk mcp serve`. Its
  `--agent-context` sidecar is `{version, summary, files:[{path, summary,
  annotations:[{newRange:[start,end], summary, rationale, author}]}]}` —
  **new-side only, no side, no commit pin**. That is an orientation format, not
  a durable comment store; useful reading for the "agent explains its own
  changeset" idea, not for anchoring.
- **[agavra/tuicr](https://github.com/agavra/tuicr)** — Rust, MIT, **~2,751
  stars**, pushed 2026-08-13, v0.22.0 same day. Vim-keybinding review TUI;
  exports to GitHub/GitLab/Bitbucket/Azure DevOps; supports git, jj and
  Mercurial. `docs/REVIEW_CLI.md` documents a headless contract explicitly
  "intended for scripts and coding agents", with sessions keyed by slugs like
  `gh:owner/repo/pr/N`. Embeddable as a Rust library — not from Go.
- **Go entrants are pre-alpha.**
  [charly-vibes/fabbro](https://github.com/charly-vibes/fabbro) has 0 stars, an
  unresolved license conflict (repo metadata says GPL-3.0, README says MIT), and
  a README stating all code was LLM-generated.
  [selyafi/diffsmith](https://github.com/selyafi/diffsmith) has 1 star and
  deliberately no headless mode.
- **Two "review-as-data" specs, both to be treated with caution.**
  [opencodereview-org/opencodereview](https://github.com/opencodereview-org/opencodereview)
  (MIT, 2 stars) ships a JSON Schema and XSD for append-only `activities` on a
  `subject`, but was created and last pushed on the same day, 2026-01-13.
  [nodeselector/agent-review-protocol](https://github.com/nodeselector/agent-review-protocol)
  has 0 stars and no commits since creation. Design sketches, not standards.

A `gh search repos "code review agent" --language go --sort updated` sweep
returned twenty repos of which eighteen had 0–2 stars, almost all "AI reviews
your PR" bots. The *human-reviews-agent-work* category is genuinely narrow:
crit, hunk, tuicr, galley, and a long tail.

---

## 4. Self-hostable review stores

None is embeddable. The value is in their anchoring models and schemas.

**Gerrit** — v3.14.2 (tag dated 2026-07-13), actively developed
([releases](https://www.gerritcodereview.com/releases-readme.html)).
[`CommentInfo`](https://gerrit-review.googlesource.com/Documentation/rest-api-changes.html#comment-info)
carries `patch_set`, `path`, `side` (`REVISION`|`PARENT`), `line`, `range`,
`in_reply_to`, `unresolved`, `commit_id`, and `fix_suggestions`. Thread
resolution is carried by the chronologically last comment in the thread rather
than a separate row. **Robot comments were removed, not deprecated** — the
machine-fix capability was promoted onto every comment as
`FixSuggestionInfo{fix_id, description, replacements[]}`
([3.10 notes](https://www.gerritcodereview.com/3.10.html)). Storage is git notes
on `refs/changes/xx/NNNN/meta` where **`revId` pins every comment permanently to
the commit it was written against**
([note-db](https://gerrit-review.googlesource.com/Documentation/note-db.html));
forward-porting is a *read-time projection* via `GET .../ported_comments`, which
maps positions through a diff transform and degrades to a file- or
patchset-level comment on conflict. The docs disclaim it: "Callers shouldn't
rely on the exact logic… Repeated calls might produce different results."
Weight: JRE 21, a ~93 MiB WAR, run as `java -jar gerrit.war daemon`. Integration
is REST/SSH/stream-events only.

**Gitea and Forgejo** — both Go, and both dead ends for importing. Gitea v1.27.2
(2026-08-13, MIT); Forgejo v16.0.2 (2026-07-30) is **GPLv3**, which rules out
importing any package into a non-GPL binary. Gitea's comment model
(`models/issues/comment.go`) uses a **signed `Line`** — `if c.Line < 0 { return
"previous" }` — plus `TreePath`, `CommitSHA`, `PatchQuoted` and `Invalidated`.
Force-push handling is **invalidation, never re-anchoring**:
`services/pull/review.go` re-runs `git blame` and, if the blamed commit differs
from the stored `CommitSHA`, sets `Invalidated = true` and stops. What keeps the
stale comment readable is the **frozen patch hunk captured at creation** — the
cheapest trick in this whole survey and worth copying regardless of anchoring
model. Importability: `models/issues/review.go` reaches `db.GetEngine(ctx)` at
dozens of sites and registers models in `init()`; importing it means importing
most of the monolith. The clean slice is `modules/structs` (MIT, stdlib-only) if
we want the wire shapes. Note a trap: Gitea **renamed its Go module** —
`go.mod` reads `module code.gitea.io/gitea` at v1.26.0 and `module gitea.dev`
at v1.27.2 and `main`. The rationale is undocumented in any primary source
found.

**reviewdog** — [MIT, ~9,526 stars](https://github.com/reviewdog/reviewdog),
pushed 2026-08-15, but the last release is **v0.21.0 from 2025-09-03** and
recent commits are almost entirely dependency bumps: actively maintained, not
actively developed. It is a **reporter, not a store** — it filters diagnostics
to a diff and posts them, with no threads, history or persistence. But it is
**the only source of genuinely importable MIT Go packages in this sweep**:
  - `reviewdog/proto/rdf` — the RDFormat structs. `Diagnostic{message, location,
    severity, source, code, suggestions[], original_output, related_locations[]}`;
    `Range{start, end}` with **exclusive end**, so a zero-width range expresses
    an insertion point, and omitting `column` makes a range linewise. JSON
    Schemas ship at `proto/rdf/jsonschema/`.
  - `reviewdog/diff` — a pure-Go unified-diff parser. `Line{Type, Content,
    LnumDiff, LnumOld, LnumNew}`, where `LnumDiff` is documented as equivalent
    to GitHub's PR-comment `position`.
  - `reviewdog/filter` — `FilterCheck(results, diff, strip, cwd, mode)`, which
    back-computes base-commit positions via `getOldPosition`. This is precisely
    the diff-position arithmetic 030's change anchors need.

**Also checked, briefly.**
[google/git-appraise](https://github.com/google/git-appraise) (Go, Apache-2.0,
~5,304 stars) stores reviews as git notes with a good shape —
`Comment{Timestamp, Author, Original, Parent, Location{commit, path, range},
Resolved *bool}`, content-hash identity, tri-state `Resolved` where nil means
FYI — but has not been pushed since 2023-08-12.
[git-bug](https://github.com/git-bug/git-bug) is GPL-3.0 and **does not do code
review** (its pull-request issue has been open since 2020). **Reviewable** is
hosted-only with no public API or self-host option surfaced in
[its docs](https://docs.reviewable.io/). **Phorge** is alive but PHP and
operationally heavier than any Go option.

### 4.1 Anchoring models compared

| Model | Mechanism | Survives rewrite? | Fit for Worklode |
|---|---|---|---|
| **crit** | anchor text + LCS remap + fuzzy (Levenshtein ≥ 0.7) + full-file scan → `drifted` | Yes, degrades gracefully | **Best for markdown specs** — content-addressed, no git dependency |
| **Gerrit** | `commit_id` pin + read-time diff-transform porting, unresolved only | Yes, best-effort, explicitly non-contractual | **Best for code across revisions**; needs a diff-transform engine |
| **Gitea/Forgejo** | `git blame` check → `Invalidated` + frozen patch hunk | No — marks stale only | Cheapest to build; steal the frozen hunk |
| **GitLab** | `{base_sha, start_sha, head_sha}` triple + `old_line`/`new_line` ([docs](https://docs.gitlab.com/ee/api/discussions.html)) | Yes, by pinning the diff triple | Elegant, line granularity only |
| **Hypothesis / W3C** | `TextQuoteSelector` verified against a cheap positional hint | Yes | **The document-review answer** — see §5 |
| **reviewdog** | `Range` against the current diff | N/A — no persistence | A library, not a model |
| **hunk / galley** | new-side line range / diff position | No | Transient handoff formats |

---

## 5. Document review and durable anchors

**W3C Web Annotation is still the standard, and is stable because it is
finished.** All three specs are Recommendations of 2017-02-23
([model](https://www.w3.org/TR/annotation-model/),
[protocol](https://www.w3.org/TR/annotation-protocol/),
[vocab](https://www.w3.org/TR/annotation-vocab/)) and the Working Group is
[closed](https://www.w3.org/annotation/). The
[selectors](https://www.w3.org/TR/annotation-model/#selectors) are
`FragmentSelector`, `CssSelector`, `XPathSelector`, `TextQuoteSelector{exact,
prefix?, suffix?}`, `TextPositionSelector{start, end}`, `DataPositionSelector`,
`SvgSelector` and `RangeSelector` — and any selector may be **`refinedBy`**
another. That composition is exactly what our anchors want:
`FragmentSelector{value: "sec-7"}` refined by a `TextQuoteSelector`.

**Synthesis.** Adopt the `oa:` selector *vocabulary* as the stored shape — it
maps cleanly into `ns/ontology.ttl` and gives us a real external namespace
instead of an invented one. Skip the Annotation *Protocol*: it is LDP plus
JSON-LD plus `Prefer` headers, which buys nothing over `/api/v1` and fights ADR
036's one-model rule.

**Hypothesis is where the working algorithm lives.**
[`hypothesis/client`](https://github.com/hypothesis/client) (BSD-2, ~720 stars,
pushed 2026-08-12) resolves anchors in
[`src/annotator/anchoring/html.ts`](https://github.com/hypothesis/client/blob/main/src/annotator/anchoring/html.ts)
as a `catch` chain from cheap to expensive — **RangeSelector → TextPositionSelector
→ TextQuoteSelector** — where every result is validated by `maybeAssertQuote`,
which throws on `range.toString() !== quote.exact`. The position is passed down
merely as a search hint. `matchQuote()` in
[`match-quote.ts`](https://github.com/hypothesis/client/blob/main/src/annotator/anchoring/match-quote.ts)
does an exact `indexOf` scan first, falls back to Myers bit-parallel
approximate matching via
[`approx-string-match`](https://github.com/robertknight/approx-string-match-js),
and scores candidates with weights `quote 50, prefix 20, suffix 20, position 2`.
Note a real bug to fix if we port it: despite its docstring, it applies **no
minimum-quality threshold** and returns the top candidate unconditionally.

Caveats: there is no `hypothesis/anchoring` repo — the old `dom-anchor-*`
packages are archived or stale, so do not build on them.
[apache/incubator-annotator](https://github.com/apache/incubator-annotator) was
**retired from the Apache Incubator on 2025-08-11** and only ever shipped
v0.1.0. **No Go port of robust text anchoring exists** — a pkg.go.dev search
returns zero modules. The nearest Go primitive,
[`sergi/go-diff`](https://github.com/sergi/go-diff)'s `MatchBitap`, caps
patterns at `MatchMaxBits = 32` characters and is byte-oriented, so it is not
UTF-8-safe and is too short for real quotes.

**Outline is the best prior art for an anchored comment store on prose.**
[outline/outline](https://github.com/outline/outline) (very active) uses a
ProseMirror mark as its primary anchor — not applicable to us — but its
*secondary* anchor is directly portable: `CommentAnchor{anchorText?,
anchorPrefix?, anchorSuffix?, anchorNodeId?}`, structurally a
`TextQuoteSelector`, used to create a comment from an API or AI tool call. The
detail worth stealing is the failure path: `ProsemirrorHelper` calls
`findClosestText` and returns a "closest text is…" error rather than silently
dropping the comment, and takes a row lock on the document first.

**CriticMarkup solves a different problem, and is having a moment.** The syntax
(`{++ ++}`, `{-- --}`, `{~~old~>new~~}`, `{>> <<}`, `{== ==}`) is frozen; the
[toolkit](https://github.com/CriticMarkup/CriticMarkup-toolkit) has not
committed since 2021-02-27, with the author stating the spec is "relatively
complete". It has **no anchoring concept at all** — it is purely inline. But it
has been rediscovered as the plain-text format for LLM-suggested edits, with a
cluster of 2026 projects including
[changedown](https://github.com/hackerbara/changedown) and a
[VS Code extension](https://github.com/xinbenlv/critique-markup-vscode-ext)
pitched at "precise comments and review feedback on LLM-generated plans, specs,
and implementation notes". There is **no goldmark extension** and no reusable Go
implementation — the closest is a working tokenizer inside
[aretext](https://github.com/aretext/aretext) that emits byte offsets, useful as
a reference to copy rather than import. Relevant to us as a *suggested-edit*
representation, not as an anchor.

**Quarto's `comments:` option is real and self-hostable.** Verified against
[quarto.org HTML basics](https://quarto.org/docs/output-formats/html-basics.html):
backends are **hypothesis, utterances, giscus**, with per-page opt-out via
`comments: false`. The Hypothesis sub-keys include `client-url`, `assetRoot`,
`sidebarAppUrl` and `services` — Hypothesis's own self-hosting knobs, surfaced
verbatim. **Synthesis:** a Quarto-rendered spec site could point the (BSD-2,
static, vendorable) Hypothesis client at a Worklode `/api/v1/annotations`
endpoint without forking anything. That is a real near-term path for published
documents, distinct from the in-cockpit review surface.

---

## 6. Quarto and markdown rendering under Go control

Findings in this section marked **[verified]** were reproduced empirically
during the sweep against Worklode's own specs, on goldmark v1.8.5/v2.0.0-beta.9
and a local Quarto 1.9.38 with Pandoc 3.8.3.

### 6.1 goldmark solves the `{#sec-N}` requirement completely

Worklode's `go.mod` already carries `github.com/yuin/goldmark v1.7.13` as an
indirect dependency (via `charmbracelet/glamour`); promoting it to direct is
free. The [README](https://github.com/yuin/goldmark#attributes) documents
`parser.WithAttribute()`, and the renderer emits the parsed attributes as real
HTML: `renderHeading` in `renderer/html/html.go` calls `RenderAttributes(w,
node, HeadingAttributeFilter)`.

**[verified]** against real spec text on v1.8.5:

| Markdown | HTML |
|---|---|
| `## Section seven {#sec-7}` | `<h2 id="sec-7">Section seven</h2>` |
| `## Dotted {#sec-1.1}` | `<h2 id="sec-1.1">Dotted</h2>` |
| `### Sub ### {#sec-7-1 .callout class="a b"}` | `<h3 id="sec-7-1" class="callout a b">Sub</h3>` |
| Setext form + `{#sec-8}` | `<h1 id="sec-8">…</h1>` |

So our dotted anchors work unmodified. Attributes are supported on **headings
only** — `ParseAttributes` is called from exactly two places, both heading
parsers — which is exactly and only what we need.

**v1.8.0 (2026-03-24) is the release that matters.** It "add[s] position
information to all nodes"
([release notes](https://github.com/yuin/goldmark/releases/tag/v1.8.0)),
giving `ast.Node.Pos() int`. **[verified]** a ~30-line walk over
`docs/specs/004-execution-backbone.md` with `goldmark-meta` +
`parser.WithAttribute()` yields exactly what comment anchoring needs:

```
id=sec-0     lvl=2 range=[76,1744)
id=sec-1     lvl=2 range=[1744,1995)
id=sec-1.1   lvl=3 range=[1995,3493)
```

Four gotchas, all **[verified]**: (1) an unquoted numeric attribute value
renders as `data-x=""` in v1 — there is a literal `// TODO: convert numeric
values to strings` in `html.go` — and is fixed in v2; (2) non-whitelisted
attribute names are filtered out of the HTML by `HeadingAttributeFilter`, though
they remain readable in the AST (`data-*` is exempt); (3) **explicit ids are not
deduplicated** — two `{#sec-7}` headings both emit `id="sec-7"`, so uniqueness
stays `scripts/secfmt.py`'s job; (4) `WithAutoHeadingID()` alone does not strip
`{#…}`, so both options are needed and explicit then wins.

Current stable is **v1.8.5** (2026-07-28); MIT, ~4,947 stars. **Do not adopt v2
yet.** The module path becomes `.../goldmark/v2`, and `goldmark.New()`,
`goldmark.Convert()` and `goldmark.Extender` are **all removed** in favour of a
split `parser.New(...)` / `renderer` API
([v2 README](https://github.com/yuin/goldmark/blob/v2.0.0-beta.9/README.md)).
Every third-party extension breaks until ported, and the README says so. Also
note goldmark's own warning that the attribute syntax "may possibly change in
the future" as the CommonMark discussion settles.

### 6.2 Quarto-flavored markdown in Go: one good extension, and real gaps

There is **no goldmark-Quarto or goldmark-Pandoc extension**. Of the two fenced-div
extensions, only one is usable:

- **[stefanfritsch/goldmark-fences](https://github.com/stefanfritsch/goldmark-fences)**
  v1.0.0, MIT, stale since 2023 but **[verified]** correct: `::: {.callout-note
  title="Note"}` → `<div class="callout-note" title="Note">`, nesting handled,
  and it composes cleanly with `parser.WithAttribute()` in one pipeline.
- [nemunaire/goldmark-fenced_divs](https://github.com/nemunaire/goldmark-fenced_divs)
  — **rejects three-colon fences** (`if oFenceLength < 5 { return nil, ... }`),
  which is Quarto's canonical form. Do not use.

**Zero Go implementations exist** for bracketed spans (`[text]{.class}`), Pandoc
citations (`[@key]`), or Quarto cross-references (`@fig-x`) — searches for
`goldmark span`, `goldmark bracketed`, `goldmark crossref` and `csl citeproc go`
all return nothing. That is a hard gap, not a choice among options.

Other Go options are worse, not better. `gomarkdown/markdown` is actively
maintained (security fixes 2026-07-25) but **[verified]** gives ids only — no
classes or key-values on the Pandoc trailing form — and its `Attributes`
extension uses mmark's brace-on-the-preceding-line syntax.
`russross/blackfriday` is **dead** (last release v2.1.0, 2020-11-07). For Pandoc
ASTs, [`adnsv/go-pandoc`](https://github.com/adnsv/go-pandoc) v0.2.0 (MIT,
2025-11-23) is the only maintained binding and models the `Attr` triple
correctly, but it is a one-person library. There is **no pure-Go Quarto**; the
nearest upstream signal is `quarto-dev/quarto-markdown`, a **Rust** standalone
parser that emits Pandoc JSON and carries a "not ready for public consumption"
warning.

### 6.3 Quarto as a sidecar: viable, small, but no daemon

`quarto-cli` is [MIT](https://github.com/quarto-dev/quarto-cli/blob/main/COPYING.md)
(GitHub reports NOASSERTION), ~5,934 stars, stable **v1.10.18** (2026-07-24).
Its [`configuration`](https://github.com/quarto-dev/quarto-cli/blob/v1.10.18/configuration)
file pins the bundled runtime: `DENO=v2.7.14`, `PANDOC=3.10`, `TYPST=0.15.1`.
**[verified]** Deno and Pandoc ship per-architecture inside the install; nothing
from the host is used.

**A document with no executable blocks needs no language runtime** —
[engine binding](https://quarto.org/docs/computations/execution-options.html#engine-binding)
selects knitr for `{r}`, jupyter for other executable blocks, and "no engine if
no executable code blocks are discovered". **[verified]**: `quarto render
doc.qmd --no-execute` succeeded on a `.qmd` containing a `{python}` chunk with
no Python installed, and an `.ipynb` that already carries outputs rendered with
nothing but Quarto.

**Official Docker images exist but are unadvertised.**
`ghcr.io/quarto-dev/quarto` is `ubuntu:22.04` plus the Quarto `.deb` — no R, no
Python, no Node — and `ghcr.io/quarto-dev/quarto-full` adds R and TinyTeX. Three
caveats: **`:latest` is stale at 1.9.38, so pin an explicit tag**; the build
workflow copies `quarto-linux-amd64.deb`, so it is amd64-only (inferred from the
workflow, manifest not inspected); and there is no Docker page on quarto.org.

**There is no render daemon and no HTTP API.** `quarto serve` is Shiny-only;
`quarto preview` is a file-watching dev server, not request-driven; and the
`--execute-daemon` flags are **Jupyter kernel** daemons that amortize kernel
startup, not Quarto startup. **Every render pays a cold Deno boot** — budget
process-per-render behind our own queue, and measure cold start before
committing.

Two integration points are genuinely good. **[verified]** `quarto inspect`
emits `{quarto:{version}, engines:[…], formats:{…}, fileInformation:{…}}` where
`fileInformation.<file>.codeCells[]` gives **every chunk with `start`/`end` line
offsets, `source`, `language` and per-cell metadata, without rendering
anything** — a ready-made addressable-unit index, and `engines` says up front
whether a document needs a runtime (note `codeCells` is empty for `.ipynb`
inputs). And `quarto render --to json` emits the Pandoc JSON AST **after
Quarto's Lua filters have run**, so callouts and cross-references are already
desugared; the cost is a warning that "Output format json does not currently
support FloatRefTarget nodes", i.e. cross-referenced figures degrade to
placeholders. `-M code-fold:true` sets options per invocation without touching
the document.

Licensing: keep Quarto a separate process or container. Quarto itself is MIT,
but the bundled **Pandoc 3.10 is GPL-2.0-or-later**; invoking a separate binary
is ordinary mere aggregation, and linking or vendoring is what would change that.

### 6.4 The freeze is the interesting finding

`freeze: true` ("never re-render during project render") or `freeze: auto`
("re-render only when source changes") store computational results in `_freeze/`,
which the [docs](https://quarto.org/docs/projects/code-execution.html) recommend
committing "so that others rendering the project don't need to reproduce your
computational environment".

**[verified]** the stored form is not HTML. `_freeze/<doc>/execute-results/html.json`
has keys `{hash, result{engine, markdown, supporting, filters, includes}}`, and
`result.markdown` is **Pandoc-flavored markdown with executed outputs already
embedded as fenced divs**:

```markdown
::: {#cell-fig-plot .cell execution_count=1}
``` {.python .cell-code}
print("hello from python")
:::
::: {.cell-output .cell-output-stdout}
hello from python
:::
```

**Synthesis.** That is directly renderable by goldmark + `goldmark-fences` +
`parser.WithAttribute()` — code and each individual output are separately
addressable, so a review UI can implement code↔output toggling itself with **no
Quarto in the request path at all** and nothing vendored. Repos already commit
`_freeze/`; we would never need a kernel. **[verified]** a re-render from freeze
with `QUARTO_PYTHON` pointed at a nonexistent binary still produced correct
output.

Preconditions, precisely: a **project context** (a bare `quarto render doc.qmd`
outside a project cannot thaw), `execute: freeze: true|auto` or `--use-freezer`,
and for `freeze: auto` a matching input hash. Do not confuse freeze with
`cache: true` — **cache requires the language runtime; freeze does not.**

### 6.5 Code ↔ output toggling is already structural

Per [quarto.org HTML code options](https://quarto.org/docs/output-formats/html-code.html),
`code-fold` takes `false` | `true` | `show`, is labelled by `code-summary`, and
is implemented with an HTML `<details>` element; `code-tools` adds a header menu
with `source`/`toggle`/`caption`. An undocumented primitive worth knowing is
**`keep-hidden: true`**, which retains content suppressed by `echo: false` /
`output: false` and marks it `.hidden` — exactly what a downstream toggler needs.

**[verified]** the emitted DOM separates cleanly:

```html
<section id="sec-7" class="level2">
<h2 class="anchored" data-anchor-id="sec-7">Section seven</h2>
<div id="cell-2" class="cell" data-execution_count="1">
  <details class="code-fold"><summary>Code</summary> … </details>
  <div class="cell-output cell-output-stdout"><pre><code>hi</code></pre></div>
  <div class="cell-output cell-output-display"><pre><code>4</code></pre></div>
</div>
</section>
```

One `div.cell` holds the source in `details.code-fold` and the results in
**sibling** `div.cell-output-*` containers, nothing interleaved. Two bonuses for
us: Quarto wraps every section in `<section id="sec-7">` with `<h2
data-anchor-id="sec-7">` — a ready-made anchoring container matching our
`{#sec-N}` ids exactly — and callouts, cross-references and citations get stable
classes (`div.callout-note`, `a.quarto-xref`, `span.citation[data-cites]`).

Two sharp edges, both **[verified]**: knitr plot output emits
`cell-output-display` **without** the generic `cell-output` class, so select on
`[class*="cell-output"]`; and `code-fold` applies only to blocks carrying
`.cell-code`, so a plain non-executable fenced block is never folded — for a
review surface over ordinary markdown, `code-fold` does nothing.

Underneath, the nbformat taxonomy survives into the DOM one-to-one:
`output_type` `execute_result`/`display_data`/`stream`/`error`
([nbformat docs](https://nbformat.readthedocs.io/en/latest/format_description.html))
map to `cell-output-display`, `cell-output-stdout`/`-stderr` and
`cell-output-error` — useful if the review surface should flag failed cells.

**Quarto 2 is a Rust rewrite with an Automerge-backed collaborative editor.**
Posit's own [announcement](https://opensource.posit.co/blog/2026-04-06_whats-next-quarto-2/)
(2026-04-06) says Quarto 2 "will ship with a collaborative editor designed to
work directly on the web" with a "robust open-source foundation based on
**automerge**", and that "we don't expect to have a public release of Quarto 2
for at least 6 months" — October 2026 at the earliest. The
[quarto-dev/q2](https://github.com/quarto-dev/q2) repo (Rust, ~237 stars,
pushed 2026-08-14) corroborates it, with forks of `automerge` and `samod` and
crates named `quarto-hub`, `quarto-p2p`. **The announcement does not mention
comments, review, annotation or track changes**, and a code search of q2 for
annotation/cursor code returns nothing. **Synthesis:** the substrate for
Google-Docs-style commenting is being built; the feature is not, and there is no
public design to align with. Watch it; do not block on it.

Nothing review-related shipped in Quarto 1.x during 2026 — 1.10 is described as
a maintenance release and the [1.11 prerelease page](https://quarto.org/docs/prerelease/1.11/)
says "nothing to see here – yet!". No Posit Connect commenting feature was found
in Posit's docs; treat that as absent but not definitively disproven.

---

## 7. Architecture patterns for the non-Go candidates

| Candidate | Runtime | Auth | State | API stability |
|---|---|---|---|---|
| **crit** (local) | single Go binary, no Node | none; loopback-only by default, `Sec-Fetch-Site` guard | `~/.crit/reviews/<key>/review.json` | pre-1.0, documented contract shipped in-repo as a skill |
| **crit-web** (service) | Docker image, Elixir + **Postgres 17+** | **generic OIDC** or GitHub OAuth; bearer on all `/api/*` in `SELFHOSTED` mode | Postgres | 4 documented endpoints; pre-1.0 |
| **galley** | **Node ≥22** + pnpm; needs `git`, `gh` | **none** — Origin guard that permits missing Origin | `~/.galley/<hash>/<session>/` | contract stable since v0.4.0; project static since July |
| **hunk** | TS, standalone binary available | loopback daemon on fixed port 47657 | session store + MCP | releases every few days — fastest-moving, least settled |
| **Gerrit** | JRE 21, ~93 MiB WAR | full account model | git notes in NoteDb | REST API stable and versioned |

**Synthesis.** Only crit offers a credible *separate service* story that does
not import Node: `crit-web` is a container plus a Postgres database plus an OIDC
client id, and Worklode already runs Postgres and already speaks OIDC to
Keycloak (spec 001 §9.2, 023). That said, running crit-web means a second
review store, which is exactly what 030 §1.1 argues against — persistence,
identity and gate semantics must stay in the backbone. The realistic shape is
crit as a **local client** and its hook payload as the **ingest**, with
crit-web as an optional read-only share target for external reviewers, never as
the record.

---

## 8. Decision summary

**Synthesis.** Three recommendations, in order of confidence.

1. **Keep meat for 029, with two amendments.** The library is sound and the
   embedding story is real. But state the pseudo-version pin explicitly rather
   than implying a release exists; raise the incomplete Apache-2.0 LICENSE file
   upstream; and drop or rewrite the suggestion that 029 "retain meat's edit
   plan", because the plan types are unexported and doing so requires an
   upstream change. 030 §4.1's line-diff recovery of `skimBlocks` is therefore
   the only option available today, not merely the less clean one.

2. **Replace galley with crit as 030's interim client.** Same architecture —
   copy a contract, run someone else's desk, own the store — with a client that
   is Go, MIT, 70× the adoption, released weekly, and already good at the
   document case we expect to dominate. Copy crit's `Comment` anchor ladder
   (anchor text + LCS + fuzzy + scan → `drifted`) for documents and galley's
   `ReviewResult` round semantics (`baseDiffHash`, the accepted/rejected/
   requested-changes split, the read-only question channel) for the round
   envelope. Integrate through the `on_finish_*` command hook rather than a
   polling bridge. Note the one gap honestly: crit does not honor `{#sec-N}`
   and treats `.qmd` as code, so document anchoring in crit's own UI is
   line-based until that changes.

3. **Design the anchor once, from the W3C vocabulary, and build it in Go.** All
   five sweeps converge on the same shape:

   ```
   anchor = coarse structural anchor + TextQuoteSelector + positional hint
          = {#sec-N} (frozen by policy) + {exact, prefix, suffix} + {start, end}
   ```

   Resolve cheap→expensive with the quote as the verifier (Hypothesis's
   pattern), store the surrounding hunk alongside the comment (Gitea's
   `PatchQuoted`, crit's `anchor`), pin the revision permanently and treat line
   numbers as a recomputable projection (Gerrit's `revId`), and on failure
   surface "anchor drifted, closest match was…" rather than dropping the comment
   (Outline's `findClosestText`). We are better positioned than anything
   surveyed because 014 §3 *freezes* our section anchors — a stronger coarse
   anchor than Outline's content hash or a CRDT item id, and it costs nothing.
   Three MIT Go packages are importable today and worth using rather than
   rewriting: `reviewdog/proto/rdf`, `reviewdog/diff`, `reviewdog/filter`.

**Two smaller notes for the discussion.**

- **Promote `goldmark` to a direct dependency (v1.8.5, not v2) and enable
  `parser.WithAttribute()`.** It gives us `{#sec-N}` → `id="sec-7"` and, via
  `Pos()` added in v1.8.0, exact byte ranges per section — the entire
  document-anchoring substrate in about thirty lines, verified against our own
  specs. Add `stefanfritsch/goldmark-fences` when Quarto documents arrive.
- **If and when scientific reports need reviewing, render the freeze, not the
  document.** `_freeze/<doc>/execute-results/html.json` stores Pandoc markdown
  with executed outputs already embedded as `::: {.cell-output-*}` fenced divs,
  which the goldmark pipeline above parses directly — giving us code↔output
  toggling with no Quarto in the request path, no kernel, and nothing vendored.
  A `quarto` sidecar container remains available for the cases freeze cannot
  cover, but it has no daemon mode, so every render is a cold Deno boot.
- **Housekeeping:** PR #29's spec numbers have been taken since it was drafted.
  `docs/specs/029-research-work-in-the-backbone.md` exists on `main`, so the
  reading-diff spec needs renumbering before the branch can land.
