---
status: draft
kind: adr
requires:
- 004-execution-backbone.md
- 032-project-cockpit.md
---
# ADR 036 — One model across packages

## 0. Decision {#sec-0}

A single `internal/model` package owns every shape that crosses the HTTP
boundary. `internal/store` scans rows into it, `internal/api` serializes it,
`internal/cli` decodes it, `internal/ui` embeds it. Re-shaping a value on its
way between packages is the exception, and every exception is named in §3 or
in an amendment to this ADR.

## 1. The problem {#sec-1}

A task exists as three hand-maintained struct declarations. `store.Task`
(`internal/store/tasks.go`) is the stored shape. `api.taskJSON`
(`internal/api/tasks.go`) restates its thirteen fields with JSON tags, reached
through a `toTaskJSON` field-by-field copy. `cli.Task`
(`internal/cli/client.go`) restates them again — its own comment says
"matching internal/api's taskJSON", which is the whole problem in one line:
the agreement is a comment, not a compiler error.

The drift is already visible. `api.briefJSON` and `cli.Brief` are the same
struct with `Parent` third in one and last in the other, because the field was
added to each by hand at different times. Nothing catches a field added to one
and forgotten in the other except a reader holding two files open.

The scale: 102 struct declarations in `internal/api` against 63 exported ones
in `internal/cli`, most of the overlap being response shapes rather than
entities.

`internal/ui` shows the same split applied inconsistently. `ui.TaskView`
embeds `store.Task` directly, while `ui.BoardItem` restates its fields — two
answers to one question inside a single file.

## 2. The rule {#sec-2}

**If a value crosses the HTTP boundary, it is a `model` type.** Entities,
response projections, and request bodies alike: `internal/api` encodes it and
`internal/cli` decodes it, so exactly one declaration can be correct and a
second one is a latent bug.

The field name is the wire name. `model.Task.Project` carries
`json:"project"`; the stored column name stays in the scan, where it already
lives explicitly.

This test — *does it cross the wire* — replaces the entity-versus-projection
distinction, which sorts types by what they mean rather than by who has to
agree about them.

## 3. What stays package-local {#sec-3}

A closed list. Adding to it takes an amendment.

- **`internal/ui` view types** — `BoardView`, `PageProps`, `TimelineRow`.
  They carry page-shell state and pre-formatted presentation strings, and are
  never serialized. They compose model types rather than restating them:
  `TaskView{Task model.Task}` is the pattern, `BoardItem`'s field-by-field
  restatement is not.
- **`internal/store` scan plumbing** — `rowScanner`, filter and argument
  builders. They never leave the package.
- **`internal/api` transport internals** — `Subject`, session records, router
  guard entries. Request-scoped machinery, not domain.

## 4. Package constraints {#sec-4}

`internal/model` imports the standard library only — no pgx, no `net/http`, no
templ runtime. Every layer can then depend on it and nothing depends back, so
the package graph stays acyclic without a rule to remember.

The model type owns its own wire invariants — the rules for what its JSON must
always look like. `toTaskJSON` today normalizes a
nil `Skills` slice to `[]string{}` so the JSON reads `[]` rather than `null`;
with no conversion step left to host it, that becomes a guarantee the store
upholds when it fills the struct.

## 5. What this costs {#sec-5}

When the stored shape and the wire shape genuinely diverge, one struct cannot
serve both. That is the real special case: `internal/store` keeps a private
type, and this ADR gains an entry recording which field diverged and why. The
list being short is the point — a growing list means the rule is wrong.

A schema change and an API-compatibility change now land on the same file.
That is useful under review, where the two are easy to conflate, and a
nuisance under merge, where two branches touch one struct instead of two.

Renaming `ProjectID` to `Project` touches every store call site. It is
mechanical, and it is the price of the wire name being the only name.

## 6. Migration {#sec-6}

Staged, not a big bang, under a numbered plan in `docs/plans/`:

1. Entities — `Task`, `Lease`, `Project`, `Actor`, `AgentSession`.
2. Response projections — `Brief`, `TaskDetail`, board and claim responses.
   This is where the `internal/api` ↔ `internal/cli` duplication lives and
   where most of the benefit is.
3. Request bodies — the create, patch, and edge inputs.

Each stage leaves the tree green on its own.

## 7. Relationship to other documents {#sec-7}

Spec 032 §12 already requires that a cockpit component "takes the assembled
domain structs as arguments" so presentation logic keeps one source of truth.
This ADR moves the code toward that sentence rather than away from it, so 032
needs no amendment.

Spec 004 §1 and §7 continue to own what a task *is* and how it is stored. This
ADR governs only how many Go declarations that one meaning gets.

## 8. Enforcement {#sec-8}

`internal/model/rule_test.go` decides §2 mechanically, because a rule about
how many declarations a shape gets is one nobody can hold in their head
across a year of handlers. It reports four things:

- a **named** json-tagged struct declared in `internal/api` or
  `internal/cli` — the original check;
- an **anonymous** json-tagged struct in those packages or in `internal/cmd`.
  Deleting the type name is the cheapest way past a declaration check, and
  an undeclared body is exactly what this ADR forbids;
- a **map handed to an HTTP body argument** — `writeJSON`'s third argument
  or the CLI client's `do` body, whether written inline, built up over
  several statements, or made with `make`. A map body is a wire shape with
  no struct to find;
- a **json-tagged `map[...]any` field in `internal/model` itself**, directly
  or under a slice or pointer. Moving a shape into `internal/model` and
  leaving it a map satisfies every rule above while keeping this ADR's
  problem intact: an envelope with a name around entries with none. Only
  `any`-valued maps are reported — a `map[string]string` is a dictionary
  whose shape is fully stated, and an opaque stored payload passing through
  is `json.RawMessage` (§3), not a map.

How much of that a package is held to depends on who else declares its
shapes:

- `internal/api` and `internal/cli` get all three of the rules that apply
  to a scanned package. Both ends are ours. (The fourth rule is not about
  them: it reads `internal/model`'s own declarations.)
- `internal/cmd` gets the anonymous-shape rule only. Its json-tagged types
  are `--json` stdout contracts: they cross no HTTP boundary and have one
  declaration by construction, so §2's test does not select them. They must
  still be named — a contract nobody can grep for is one nobody reviews. The
  consequence is deliberate: a *named* struct there that decodes an HTTP
  response is not reported, and review is what catches it.
- `internal/hooks` gets the body rule only. Its declared shapes are GitHub's
  and Flux's inbound payloads — foreign schemas this ADR does not govern —
  while what it answers with is ours.

Exemptions live in the test's `allowed` map, keyed by package and type and
carrying the reason (a cookie, a state parameter, an on-disk file — §3). An
entry that matches no declaration is reported as stale, the way
`internal/api/router.go` treats an unused route guard.

The guard's own reach is checked rather than assumed. `TestGuardCatchesTheDodges`
and `TestUntypedMapGuardCatchesTheDodges` run the checks over known-bad source,
so each rule is known to still match something. A scanned package that parses
no files is a failure, so renaming or splitting one cannot turn the guard
green while it inspects nothing.

What it does not see: a body assembled by a helper that returns a map, or
marshalled to bytes before it reaches a body argument. Both take deliberate
work to arrange; the cases above are what a handler reaches for by accident.

The timeline was this ADR's one deferred shape and is now typed.
`model.TimelineEntry` is a flat union discriminated by `type`: `at` and
`type` on every entry, the rest `omitempty` and populated per type. A per-type
struct behind a `payload` object was the alternative and was not taken — the
entries are flat on the wire, seven fields are shared by two or more of the ten
types, and Go has no sum type, so nesting the payload would not even earn a
consumer the exhaustiveness checking that could justify the cost. A consumer
switches on `type` either way; the difference is only whether the fields it
then reads are declared.
`change` stays a `json.RawMessage`: it is a stored `state_log` payload passing
through, and `LogChange` writes `{"field","old","new"}` for a field update but
`{"field","names"}` for materialized secrets, so there is no one struct to
decode it into (§3).
