---
status: draft
issued: 2026-08-05
requires:
  - 004-execution-backbone.md
  - 005-prioritization-and-pickup.md
  - 008-worklode-plugin.md
  - 011-delivery-lifecycle.md
  - 014-design-documents-as-graph-objects.md
  - 022-prometheus-metrics.md
  - 023-keycloak-primary-auth.md
  - 025-documents-in-the-backbone.md
  - 027-event-watchers.md
  - 028-escalation-and-document-lifecycle.md
  - 029-reading-diffs.md
---
# Spec 030 — The review surface

## 0. Why {#sec-0}

The corpus has claimed a review gate since 000. 000 §4 says design docs are reviewed via **crit**
and that `proposed → accepted` is gated on crit resolution; 003 §1, 006 §1, 007 §2 and 014 §5
repeat it; 025 §3 restates it as "a draft with an open review task, with crit anchoring comments
to sections". Nothing implements any of it. There is no comment, no thread, no resolution, no
approval and no gate — five specs assert a mechanism that does not exist, and 028 §6 has now
written an accept gate on top of it: *a document is not accepted until every assigned reviewer
approves*. That gate reads a table nobody has designed.

The same hole exists one layer down. 011 gives a task an `in_review` state and nothing that
happens in it. 029 spends model tokens producing a diff worth reading and then hands it to a
surface that cannot record what the reader concluded. `docs/follow-ups.md` states the constraint
that decides the shape: **review API before review UI** — Claude reviews Codex's work and Codex
reviews Claude's, so comment, thread, resolve and approve must exist as API and `lode` verbs with
a browser as one client among several. Web handlers first would produce a human-only review
system inside an agent-first product.

[galley](https://github.com/ymansurozer/galley) (MIT, Node 22) has already solved the half that
is hard to design and easy to underestimate: the interaction. A localhost desk over a git diff
where a human accepts and rejects blocks, comments, asks questions, and clicks Send; the agent
attaches over a documented JSON contract, answers in-thread, edits, and re-diffs into the same
live tab. It ships a machine contract (`galley spec`), a guided-review schema, a question channel
that is deliberately read-only, and an attention policy with a stated honesty rule. It is tested,
it works, and Worklode is not going to out-design it in one spec.

It also cannot be Worklode's review store, and the reason is not aesthetic. Galley binds
`127.0.0.1`, serves an **unauthenticated** desk, and persists every review to
`~/.galley/<repoHash>/<session>/` on one operator's machine. Worklode is org-wide,
multi-operator, Postgres-backed, event-logged and identity-gated. `CLAUDE.md`'s rule settles it:
**no fact has two owners**, and review state living in a developer's home directory cannot gate
`proposed → accepted` for the organisation.

So this spec does not choose between adopting galley and building review. It splits them along
the line that actually exists — contract, client, store — and says which side each piece is on.

---

## 1. The review is the API; the desk is a client {#sec-1}

**The durable object is a review, not a session at a desk.** A review has a subject, an assigned
reviewer set, threads anchored into that subject, an ordered series of rounds, and a verdict per
reviewer. All of it lives in Postgres, is written through `/api/v1`, is emitted to 027's log, and
is readable by anything holding a token. A browser desk is one way to produce those rows. A
terminal is another. An agent with no browser at all is a third, and none of the three is
privileged.

This is 029's discipline applied one level up. 029 §1 rules that a reading diff is *a rendering,
never the artifact of record*; the same sentence holds here with "desk" in place of "reading
diff". A review surface that is authoritative about anything is a surface Worklode can never
replace, and Worklode will replace this one — §14 says so.

### 1.1 What is copied, what is run, what is owned {#sec-1.1}

**Copied as contract.** Galley's `ReviewResult` (`src/types.ts`) is adopted as the wire shape of
a review round, field for field, including the fields Worklode does not normalize. It is a good
schema — `accepted`/`rejected`/`requestedChanges`/`approvedFiles`/`openQuestions`/`overallNote`
covers every verdict a reviewer can reach without a prose blob anyone has to parse — and it is
already the contract several agent harnesses know how to act on. `POST
/api/v1/reviews/{id}/rounds` therefore takes a `ReviewResult` verbatim, and `GET
/api/v1/reviews/{id}/rounds/{n}` returns one. **The object a remote author agent receives from
the backbone is byte-equivalent to the object `galley await` hands a local one**, which is why
one skill drives both and why adopting the shape is worth more than improving it. The
question/review distinction (§3), the guided-review schema (§4) and the skim/flag attention
policy (§4.2) are copied on the same terms.

**Run as an interim client.** Galley itself, unmodified, from npm, launched by `lode review desk`
on the reviewer's machine, with a bridge process that relays rounds and questions to the backbone
(§7.1). Nothing server-side knows galley exists; no deployment gains a Node dependency; upstream
releases are an upgrade rather than a merge.

**Owned natively, and only these.** Persistence and the anchor model that survives a force-push
(§2.1); identity, so a verdict names an authenticated actor rather than whoever was at the
laptop; the multi-reviewer set and the gate that reads it (§8); provenance through 027's log
(§11); cross-machine delivery, so a question asked in Oslo reaches an agent running in a cluster
(§6); and the ability to block a task on a review. Every one of those is a thing galley
deliberately does not do, because galley is a single-seat tool and is better for it.

The line is not "Worklode is serious and galley is a toy". The line is that **galley owns the
round and Worklode owns the record**, and a round is exactly as long-lived as the browser tab it
happened in.

### 1.2 Alternatives {#sec-1.2}

- **Fork or vendor galley.** Costs a TypeScript build and eight npm runtime dependencies inside a
  product whose whole distribution story is one Go binary, and then buys nothing, because the
  parts that must change — loopback binding, no auth, home-directory state — are its
  architecture, not its configuration. You would rewrite the server and keep the UI, which is
  the *next* option, honestly named.
- **Port the UI, replace the backend.** Galley's frontend (Alpine, shiki, `@pierre/diffs`) is MIT
  and could be served from `internal/api` against the schema of §5. This is the right eventual
  answer and the wrong first one: it adds a JS build to the Go release pipeline and a permanent
  fork of a frontend with no upstream path, in exchange for a surface whose semantics are not yet
  proven. §14 defers it deliberately, and §10's `client` label is how the decision to take it
  becomes a number rather than an opinion.
- **Build the Worklode web review UI first, with no API.** The failure mode is stated in
  `docs/follow-ups.md` and is worth restating concretely: agent reviewers get no path at all, so
  the reviewer population is halved precisely where the product's premise lives, and the review
  semantics accrete inside HTTP handlers and have to be re-derived when the API arrives.
- **Do nothing; keep reviewing in GitHub PRs.** This is the only alternative with real merit, and
  it fails on three specifics. Design documents have no pull request — after 025 they are not
  files — so 028 §6's gate has nothing to read. GitHub's "approved" would become a second owner
  of a fact 028 §6 already assigns to the backbone. And a task whose change spans three repos has
  three PRs and no review. GitHub PR review is not abolished by this spec and stays the surface
  for anything no Worklode task tracks; what it cannot be is the gate.

## 2. One review over documents and changes {#sec-2}

**One review object covers both, with two anchor kinds.** A `review` has a subject that is either
a document (025 §2's `docs`) or a task's change. Everything that makes review hard — threads,
intents, resolution, rounds, multiple reviewers, a verdict that expires when the artifact moves —
is identical across the two. Only the coordinate a comment attaches to differs.

Galley is the existence proof: `repo`, `file` and `pr` are three modes of one desk emitting one
`ReviewResult`, distinguished by a single `mode` field telling the agent how to read the
verdicts. Two Worklode review systems would mean two thread stores, two resolution semantics, two
accept gates and two skills for every reviewing agent — and the predictable outcome is that
document review ships, code review is left to GitHub forever, and the split calcifies.

The subject of a change review is the **task**, not the pull request. A task may carry several
PRs (004's `pull_requests` is many-per-task) and 011's `in_review` is a state of the task. Anchor
the review to a PR and a change that spans repos becomes unreviewable; anchor it to the task and
the cross-repo case is free, which is a capability GitHub structurally cannot offer.

### 2.1 Anchors {#sec-2.1}

A thread anchors to content, never to a position, because positions do not survive the thing
review exists to cause — the author changing the artifact.

- **Document threads** anchor to a frozen `{#sec-N}` anchor plus the quoted text the commenter
  selected. 014 §3 freezes anchors at first acceptance and forbids renumbering; that guarantee is
  what makes a section anchor a durable coordinate and it is the reason document review is the
  *easier* of the two cases, not the harder one. A comment on prose inside a section binds to the
  section and carries its quote; it never binds to a line number, because line numbers in a
  rendered document are an artifact of rendering.
- **Change threads** anchor to `(repo, path, side, line_number)` — galley's tuple, kept — plus
  `anchor_text`, the exact text of the anchored line at creation, and the new-side blob sha.
  Galley already uses `anchorText` to re-anchor a thread when the agent's edits shift a line
  (`reanchorComments` in `state.ts`); the backbone needs it for the harder case galley never
  faces, a force-push where the line number is meaningless and the text survives.

A thread whose anchor no longer resolves becomes `obsolete` rather than being deleted or silently
re-pointed. Galley marks these `unanchored` and shows them in a file-level strip; Worklode does
the same and keeps them in the review, because a comment that vanished when the author rewrote
the line is a comment the author answered by deleting the question.

## 3. Threads, and the question that is not a change request {#sec-3}

A thread carries an `intent`, and the three values are galley's, because the distinction it draws
is correct and load-bearing:

| Intent | Means | Obliges |
|---|---|---|
| `note` | a remark | nobody |
| `change_request` | change this | the author, before the next round |
| `question` | explain this | the author, **now**, without touching the artifact |

**A question is read-only, and that is the whole point.** Galley states it in the imperative:
answering a question means reading for context and replying in-thread, and *never* editing
tracked files, because the reviewer is mid-review and edits under them invalidate the decisions
they have already made. This is not a UI nicety — it is the only rule that makes a live review
possible at all, and it is exactly what a review system built as CRUD over comments gets wrong.
Worklode adopts it as a server-enforced property: while a review has an open `question` thread,
the author agent may reply, and a round submitted by the author is refused. The escape hatch is
galley's — if the question's own text asks for a change, the author edits and opens a new round,
which is a different act.

**A bare note is a thread with no review**, which is why `lode doc note` (028 §4) writes into
this table with `review_id IS NULL`. 028 §4 defines the verb, its two callers and its promise
never to block execution; this spec supplies the row. The alternative — a `doc_notes` table
beside `review_threads` — would put two owners on "a remark anchored to a section", and the query
028 §4 actually wants ("what is outstanding on §4.3, from any source") would become a union
across two schemas that drift. A note is the degenerate thread: same anchor, same author, same
resolution verb, no review obliged to clear it.

### 3.1 Where a question sits on 028's ladder {#sec-3.1}

028 §1 gives a rule for who resolves what: **a synchronous subagent when the work is bounded and
tier is the only thing missing; a minted task when the work needs human judgment or another
operator.** A reviewer's question is a third case and it resolves the same way the first does,
one rung lower: it is answered synchronously, in the thread, by the agent that authored the
change and still holds the lease. Nothing is minted, the lease is not released, and the task does
not leave `in_review` — because the reviewer is present *right now* and the latency budget is
seconds, not the unbounded queue latency 028 §1 rejects a minted task for.

The two mechanisms meet where they should. If the author cannot answer a question because the
plan or spec does not cover the case, that is 028's `task.gap_found` and the answer is `lode task
escalate` (028 §2): the review stays open, the question thread carries the escalation's task id,
and the reviewer sees why the answer is late rather than watching an agent go quiet. **A question
that turns out to be about the design is the cheapest gap detector the system has**, because a
human is already looking at the consequence.

A question asked of an author agent whose lease has expired has no live answerer. It stays open
and is folded into the next round exactly as galley folds `openQuestions` into a `ReviewResult` —
the reviewer's question is not lost because the agent went away.

## 4. Guides {#sec-4}

**Guide generation is a Worklode surface, and the guide is the mirror image of
`lode task brief`.** 008 §6's brief is deterministic, bounded context assembly that shapes an
executor's attention *going in*: this task, this governing section, these skills, this
definition of done. A guide is the same act pointed the other way — an overview, a per-file
`orientation`, an explicit `flag` on what deserves scrutiny, and `skim`/`skimBlocks` on what does
not — shaping a reviewer's attention *coming out*. 003 §5 calls the brief the biggest token win
in the system; the guide is the second one, and for the same reason: judgment is what tokens
should be spent on, and locating the judgment is deterministic work.

The guide schema is galley's, unchanged: `title`, `overview`, `prDescription`, `files[]` with
`path`/`orientation`/`order`/`category`/`flag`/`skim`/`skimReason`/`skimBlocks`/`movedFrom`, and
the top-level `focused` boolean. `GET /api/v1/reviews/{id}/guide` emits exactly that JSON, so it
pipes into `galley --guide` with no translation, and `lode review show --json` embeds the same
object for an agent reviewer that will never open a browser. One artifact, every client.

Galley's own rule that a guide must be written **outside the working tree** is inherited by the
bridge (§7.1): working mode surfaces untracked files, so a guide materialized in the repo shows
up in the review as a stray addition.

### 4.1 Composing with 029 {#sec-4.1}

029 produces a summary and a reduced diff. Both belong in the guide, and the seam has one rule
that is easy to get wrong:

**029 elides; the desk collapses; never both on the same surface.** A reading diff removes lines
from the text it renders. A guide's `skimBlocks` collapses change blocks behind a one-click
expand strip and hides nothing. Where a desk exists, collapse strictly dominates elision — the
reviewer can always disagree with the shaping — so the desk is fed the **raw** diff and the
reading diff's judgment arrives as guide fields:

- 029's `summary` becomes the guide's `overview`, labelled as machine-produced per 029 §1.
- 029's omissions become `skimBlocks` spans, **only** when the reviewer asked for a focused
  review; a plain guide skims nothing, which is galley's rule and §4.2's reason for it.
- meat's own file ordering becomes `order`; nothing invents a `category` a human did not give.

Recovering the spans is sound because of the property 029 §1 exists to guarantee: a reading diff
is provably a *subset* of the real diff, so a line-level comparison of `smart_diff` against the
raw diff yields exactly the omitted spans. Nothing is inferred and no second model call is made.
029 could retain meat's edit plan and hand the spans over directly — cheaper, and worth doing —
but this spec deliberately does not require it, because a composition that depends on a
neighbouring spec adding a column is a composition that ships late.

Where no reading diff exists — 029 §8's degradation, `too_large`, a document rather than code —
the guide is authored by the agent under review, with no `overview` from 029 and no skims. The
guide is optional throughout: galley works without one and so does every Worklode surface.

### 4.2 The honesty rule {#sec-4.2}

**No agent may lower the gate on its own work.** This is one principle with three existing
instances, and stating it once is the point of this section.

028 §3.1 names the mechanism: the fixer subagent has an incentive to rule its own change
non-substantive, because "substantive" stops the executor it was spawned to unblock — so
*uncertain counts as substantive*, and the mechanical rules are enforced server-side (028 §3.2)
rather than left to the agent the gate exists to check. Galley names the same failure in the same
words one domain over: *skim LOWERS attention — never skim your own risky or non-obvious
changes*, and a shaped review is badged `focused: true` so the human knows attention was
deliberately narrowed. These are not two coincidental rules of thumb. They are the same fact
about the same incentive, and Worklode should not have to rediscover it a third time.

The rule has teeth here because a skim means two different things to the two reviewer
populations. To a human it is advisory — the strip expands. **To a reviewer agent it is
authoritative**, because the agent will simply not read what the guide told it to skip. A skim on
the agent path is therefore a real reduction of the gate, and it must not be a private
arrangement between the authoring agent and the reviewing one. Three consequences, all enforced
server-side:

1. **The guide handed to a reviewer agent is produced by the backbone**, from the author's
   proposal plus policy, never passed agent-to-agent. `GET .../guide` is the only source.
2. **An author's proposed `skim` is refused on a file it edited non-mechanically.** The
   mechanical set is 029's and galley's, and it is narrow: lockfiles, generated output, vendored
   code, snapshots, import-only and formatting-only blocks, rename ripples. Anything else is
   refused with the rule that fired, exactly as 028 §3.2 refuses a silent patch.
3. **Self-approval is refused.** A verdict from the actor holding the task's lease, or from the
   agent session that authored the round, is rejected at the API. 028 §6 leaves reviewer
   assignment social; who may *not* be assigned is mechanical.

`focused` is stored on the round (§5) so "was this approval cast over a deliberately narrowed
diff, and who narrowed it" is a query rather than an archaeology exercise. §10 counts skimmed
blocks by who decided them.

## 5. Schema {#sec-5}

Migration `0011_reviews`, listed in `deploy/base/kustomization.yaml`. `docs` is 025 §2's table;
until 025 lands, the `doc_id` column and its constraint ship in the same migration as `docs`.

```sql
CREATE TABLE reviews (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Exactly one subject. A change review hangs off the TASK, not a pull request:
    -- one task's change may span repos (004's pull_requests is many-per-task) and
    -- 011's in_review is a state of the task, so a PR-scoped review could not cover
    -- the change as shipped.
    task_id    text REFERENCES tasks (id) ON DELETE RESTRICT,
    doc_id     text REFERENCES docs  (id) ON DELETE RESTRICT,
    -- Denormalized discriminator so every query, index and bounded metric label
    -- reads one column instead of testing two NULLs.
    subject    text NOT NULL,
    state      text NOT NULL,
    -- What was reviewed, so a verdict can be told apart from a later artifact.
    -- galley's baseDiffHash verbatim for a change; docs.version for a document.
    base_ref   text NOT NULL,
    round      integer NOT NULL DEFAULT 0,
    -- Per-review message cursor for `lode review await` (§6). Allocated under this
    -- row's FOR UPDATE, which is affordable because a review is a low-rate object;
    -- 027 §1's commit horizon exists because serializing every webhook and claim
    -- through one lock is not, and that trade is deliberately not repeated here.
    next_seq   bigint NOT NULL DEFAULT 1,
    opened_by  text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    opened_at  timestamptz NOT NULL,
    closed_at  timestamptz,
    CONSTRAINT reviews_one_subject CHECK ((task_id IS NULL) <> (doc_id IS NULL)),
    CONSTRAINT reviews_subject_agrees CHECK (
        (subject = 'change' AND task_id IS NOT NULL) OR
        (subject = 'doc'    AND doc_id  IS NOT NULL)),
    CONSTRAINT reviews_state_known CHECK (state IN
        ('open', 'changes_requested', 'approved', 'closed'))
);

-- One open review per subject: a second would give "is this approved" two answers.
CREATE UNIQUE INDEX reviews_open_task ON reviews (task_id)
    WHERE closed_at IS NULL AND task_id IS NOT NULL;
CREATE UNIQUE INDEX reviews_open_doc ON reviews (doc_id)
    WHERE closed_at IS NULL AND doc_id IS NOT NULL;

-- The assigned reviewer set. 028 §6 states the gate — not accepted until every
-- assigned reviewer approves — and this is the table it reads. Assignment stays a
-- social choice; the gate over the rows is mechanical.
CREATE TABLE review_reviewers (
    review_id     bigint NOT NULL REFERENCES reviews (id) ON DELETE RESTRICT,
    actor_id      text   NOT NULL REFERENCES actors  (id) ON DELETE RESTRICT,
    assigned_at   timestamptz NOT NULL,
    verdict       text,
    -- A verdict is cast against a ROUND, never against the review. A new round from
    -- the author invalidates every approval — the same reason galley resets a block
    -- to pending on reload when the agent has rewritten it.
    verdict_round integer,
    verdict_at    timestamptz,
    PRIMARY KEY (review_id, actor_id),
    CONSTRAINT review_reviewers_verdict_known CHECK (verdict IS NULL
        OR verdict IN ('approved', 'changes_requested'))
);

-- One row per Send. The normalized rows elsewhere are the fact; `payload` is
-- provenance.
CREATE TABLE review_rounds (
    review_id    bigint NOT NULL REFERENCES reviews (id) ON DELETE RESTRICT,
    round        integer NOT NULL,
    actor_id     text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    -- Which surface produced it. Bounded, and the number that decides when the
    -- interim client (§7.1) can be retired (§10).
    client       text NOT NULL,
    base_ref     text NOT NULL,
    -- galley's focused flag: an approval cast over a deliberately narrowed diff is
    -- a different fact from one cast over the whole of it (§4.2).
    focused      boolean NOT NULL DEFAULT false,
    overall_note text,
    -- The ReviewResult exactly as the client sent it. Kept verbatim because the
    -- shape is upstream's, not ours: a field we do not yet model must not be
    -- silently dropped, and per-block accept/reject is meaningful only against this
    -- round's base_ref, so it is deliberately not normalized (§1.1).
    payload      jsonb NOT NULL,
    submitted_at timestamptz NOT NULL,
    PRIMARY KEY (review_id, round),
    CONSTRAINT review_rounds_client_known CHECK (client IN
        ('galley', 'web', 'cli', 'agent'))
);

-- The guide attached for a round (§4). Keyed by the round it applies to, because a
-- guide is regenerated between rounds and a stale one must be identifiable rather
-- than overwritten.
CREATE TABLE review_guides (
    review_id     bigint NOT NULL REFERENCES reviews (id) ON DELETE RESTRICT,
    round         integer NOT NULL,
    guide         jsonb NOT NULL,
    -- Who proposed it, and whether the backbone rewrote it. §4.2 forbids an author
    -- agent from shipping a skim straight to a reviewer agent, so the vetoes are
    -- recorded rather than applied invisibly.
    proposed_by   text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    skims_refused integer NOT NULL DEFAULT 0,
    -- Set when §4.1 derived the overview and skim spans from a reading_diffs row.
    from_reading_diff boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL,
    PRIMARY KEY (review_id, round)
);

CREATE TABLE review_threads (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- NULL for a bare note (028 §4): the same shape with nothing obliged to resolve
    -- it. One table, because a second one would put two owners on "a remark
    -- anchored to a section" (§3).
    review_id   bigint REFERENCES reviews (id) ON DELETE RESTRICT,
    -- Subject anchors are denormalized onto the thread so a note outlives the review
    -- that hosted it and "everything outstanding on §4.3" is one index scan.
    doc_id      text REFERENCES docs  (id) ON DELETE RESTRICT,
    -- A frozen 014 §3 anchor — never a line number, which is an artifact of
    -- rendering (§2.1).
    doc_anchor  text,
    task_id     text REFERENCES tasks (id) ON DELETE RESTRICT,
    repo        text,
    path        text,
    side        text,
    line_number integer,
    -- The exact anchored text at creation. galley re-anchors with it when edits
    -- shift a line; the backbone needs it for the case galley never faces — a
    -- force-push, where the line number is meaningless and the text survives.
    anchor_text text,
    blob_sha    text,
    -- The task and agent session that raised it, so 028 §4's note keeps its
    -- provenance and a question can be traced to the work that prompted it.
    raised_by_task    text   REFERENCES tasks          (id) ON DELETE RESTRICT,
    raised_by_session bigint REFERENCES agent_sessions (id) ON DELETE SET NULL,
    intent      text NOT NULL,
    status      text NOT NULL,
    author_id   text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    created_at  timestamptz NOT NULL,
    resolved_by text REFERENCES actors (id) ON DELETE RESTRICT,
    resolved_at timestamptz,
    CONSTRAINT review_threads_intent_known CHECK (intent IN
        ('note', 'change_request', 'question')),
    -- `obsolete` is an anchor that stopped resolving. The thread is kept, never
    -- deleted and never silently re-pointed (§2.1).
    CONSTRAINT review_threads_status_known CHECK (status IN
        ('open', 'resolved', 'obsolete')),
    CONSTRAINT review_threads_side_known CHECK (side IS NULL
        OR side IN ('additions', 'deletions'))
);

CREATE INDEX review_threads_open ON review_threads (review_id) WHERE status = 'open';
CREATE INDEX review_threads_doc ON review_threads (doc_id, doc_anchor)
    WHERE doc_id IS NOT NULL;

CREATE TABLE review_messages (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    thread_id  bigint NOT NULL REFERENCES review_threads (id) ON DELETE RESTRICT,
    review_id  bigint REFERENCES reviews (id) ON DELETE RESTRICT,
    -- Per-review monotonic cursor for the await long-poll (§6). A client cursor must
    -- be per-review: a client is not a 027 subscriber, and giving the global log a
    -- second role as a client-facing stream would put two consumers on one offset
    -- space with different retention needs.
    seq        bigint,
    actor_id   text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    -- Which side is speaking, not what kind of thing is speaking: both are held by
    -- humans and agents alike, and an agent reviewing an agent has a `reviewer` role.
    role       text NOT NULL,
    body       text NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (review_id, seq),
    CONSTRAINT review_messages_role_known CHECK (role IN ('reviewer', 'author'))
);
```

Rows are never deleted. A closed review is the provenance of an acceptance and outlives every
interest in it, on the same argument 027 §2 makes for the log.

## 6. API and `lode` verbs {#sec-6}

```
POST   /api/v1/reviews                                   open; assigns reviewers
GET    /api/v1/reviews?subject=&reviewer=&state=
GET    /api/v1/reviews/{id}                              review, threads, current guide
GET    /api/v1/reviews/{id}/guide                        galley-schema guide JSON (§4)
POST   /api/v1/reviews/{id}/rounds                       a ReviewResult, verbatim (§1.1)
GET    /api/v1/reviews/{id}/rounds/{n}                   the same object back
POST   /api/v1/reviews/{id}/threads                      open a thread
POST   /api/v1/reviews/{id}/threads/{tid}/messages       reply
POST   /api/v1/reviews/{id}/threads/{tid}/resolve
POST   /api/v1/reviews/{id}/verdict                      approved | changes_requested
GET    /api/v1/reviews/{id}/events?after=<seq>&timeout=  long-poll; 204 on timeout
```

```
lode review open <task|doc> --reviewer a,b     open and assign
lode review list [--mine] [--open]
lode review show <id> [--json]                 threads + guide + reading diff (029 §7)
lode review guide <id> [--focused]             emit the guide JSON
lode review desk <id>                          interim: launch galley and bridge (§7.1)
lode review comment <id> --anchor <a> --intent question|change_request|note --body "…"
lode review reply <thread> --body "…"
lode review resolve <thread>
lode review approve <id> | lode review request-changes <id>
lode review await <id> [--timeout <s>]         author-side; galley's await, cross-machine
lode review submit <id> --result <file>        ingest a ReviewResult from disk
```

`lode review await` is galley's `await` with the desk replaced by the backbone, down to the exit
semantics: it blocks, prints one tagged JSON envelope and exits; `--timeout` prints nothing and
exits 0; a non-zero exit means there is no open review. That fidelity is not stylistic. An agent
that already knows galley's loop drives Worklode with a changed command name and no changed
logic, and the `question`/`review` branch it already implements is the branch it needs.

`lode review submit --result <file>` exists for the file-poll fallback galley itself documents:
every Send also writes the `ReviewResult` to `artifacts.resultJson`, so a reviewer who cannot run
the bridge — no long-poll, no background process — can still land a round.

## 7. Surfaces and clients {#sec-7}

| Client | Path | Notes |
|---|---|---|
| Human, browser desk | galley + `lode review desk` (§7.1) | v1's primary human surface |
| Human, terminal | `lode review comment/approve` | no browser, no Node |
| Reviewer agent | `lode review show --json`, `comment`, `verdict` | guide + 029 reading diff as brief (029 §7) |
| Author agent | `lode review await`, `reply`, `rounds/{n}` | same envelopes as galley's local loop |
| Worklode web UI | read-only review view in v1 | inherits 023's OIDC gate |

The web UI in v1 renders reviews and does not produce rounds. That is a deliberate ordering, not
an omission: the follow-up's whole argument is that handlers written before the API produce a
human-only system, and a read-only view proves the API without being able to grow semantics of
its own.

### 7.1 The interim galley bridge {#sec-7.1}

`lode review desk <id>` is client-side only, in `internal/cmd`. Nothing on the server knows galley
exists.

1. Resolve the review, fetch its guide, and materialize it to a temp file **outside** the working
   tree — galley's own rule, because working mode surfaces untracked files and an in-repo guide
   shows as a stray addition in the very diff under review.
2. Launch the mode the subject implies: `galley pr <ref> --guide` for a change with a branch,
   `galley --diff working --guide` for an uncommitted one, `galley file <export> --guide` for a
   document (§7.2). `--session` is the review id, so a restart reattaches to the same desk on the
   same port and the reviewer keeps one tab.
3. Loop on `galley await`. A `review` event `POST`s its `ReviewResult` to
   `/reviews/{id}/rounds` with `client=galley`. A `question` event opens a `question` thread per
   entry, then long-polls `/reviews/{id}/events` and relays each answer back with `galley
   comment --path --line --side`, posting `galley status` lines while it waits so the reviewer
   sees that something is happening rather than nothing.
4. `galley stop` when the review closes.

**The bound on this is worth stating rather than discovering.** Galley's desk is unauthenticated
and the bridge holds the operator's token, so every round is attributed to whoever ran the
bridge. That is correct for a single-seat loopback desk and it means the bridge **must refuse to
run against a desk bound beyond loopback** (`--host` / `GALLEY_HOST`, galley's remote-dev mode),
because it would otherwise attribute a stranger's clicks to the token holder. The refusal is a
check in `lode review desk`, not a note in a skill.

The durability bound is the same one galley already lives with: everything Sent is in Postgres;
only the un-Sent decisions of the round currently open in the tab live in `~/.galley`, and they
die with the laptop. Nothing else about a review is ever recovered from a home directory.

### 7.2 Documents through `lode doc export` {#sec-7.2}

Galley's `file` mode reviews one markdown file with a Rendered/Source toggle and per-block
comments — which is design-document review, already built. To use it, a document must be a file
with stable coordinates, and `lode doc export` (already a follow-up item, justified there by "git
history and grep survive") is that file.

**The export is worth shipping for this reason more than that one.** `lode doc export <id>
--anchors <map>` writes the document plus a sidecar mapping line ranges to `{#sec-N}` anchors, and
the bridge uses the map to convert a galley comment at line 412 into a thread on `#sec-4.3` with
the quoted text. That is what makes any file-oriented reviewer — galley today, an editor
tomorrow, a `git diff` of two exports next year — able to write into the frozen anchor space 014
§3 defines. Without the map, every document-review client re-implements markdown heading parsing
and they disagree at the first letter-suffix anchor.

The export is read-only and regenerable. Nothing is ever read back from it: the thread carries
the anchor and the quote, and the document itself stays in the backbone.

## 8. Verdicts and gates {#sec-8}

**This spec supplies the evidence; the gates stay where they already are.**

- **Documents.** 028 §6's rule is unchanged: not accepted until every assigned reviewer approves.
  `lode doc accept` reads `review_reviewers` and refuses while any assigned reviewer has no
  `approved` verdict against the current round. 028 §6's `in_review` — "a human has begun the
  review work" — is set by the first `GET /reviews/{id}` from an assigned reviewer, which is what
  "entering the document in the review UI" means once the UI is one client among several.
- **Changes.** 011's state machine is unchanged. Opening a review over a task's change moves it
  to `in_review`; a round carrying any `change_request` thread or a `changes_requested` verdict
  returns it to `in_progress` and releases nothing, because the author still holds the lease. The
  merge transition stays 011's.
- **Rounds invalidate verdicts.** A new round from the author clears every approval cast against
  an earlier round (§5). Galley does this within a session — a block the agent edits resets to
  pending on reload — and it is more important across machines, where an approval could otherwise
  sit unnoticed against a diff that no longer exists.
- **Review tasks take an assignee.** `lode review open --reviewer` mints a `kind = 'review'`
  task (025 §6) per assigned reviewer, assigned to them, which is what makes "I asked Kim to
  review it" visible as routing rather than as authority — the follow-up item adjacent to the one
  this spec answers. 028 §8 already routes `--kind review` to a high tier, so an agent reviewer
  picks the task up through the same `claim --next` every other kind uses.

**`tasks` has no assignee column, and no spec owns adding one.** 028 §2 assigns an escalated
`design` task to the plan's author and the bullet above assigns a `review` task to its reviewer;
both assume a column that 0001's `tasks` does not have. Assignment is not a lease — a lease is
held by whoever is working now, an assignment records who is *expected* to, and collapsing them
would make "assigned to Kim" mean "Kim's worktree is bound", which is false for every task in a
queue. `0011_reviews` therefore adds `tasks.assignee text REFERENCES actors (id)`, nullable,
with no effect on ranking or the pickup gate in this spec — 005's ordering is untouched, and an
assignee is routing rather than authority. It is called out here rather than folded in silently
because it is the one piece of this design that changes a table two other specs already own.

## 9. Degradation {#sec-9}

- **No galley, no Node, no browser.** Every review verb works: comment, reply, resolve, approve,
  await. `lode review show` prints the guide as text and the diff raw. The desk is a renderer and
  its absence costs ergonomics, never capability. This is a hard requirement — the moment a
  verdict can only be cast from a browser, the agent-reviewer path is second-class and §0's
  argument is lost.
- **No reading diff** (029 §8, `too_large`, `failed`, or `LODE_READING_MODEL` unset). The guide
  ships without a machine `overview` and without skims, and every surface renders as before.
- **No guide.** Galley works without one and so does `lode review show`; guide surfaces simply do
  not render, which is galley's own behaviour on an absent guide.
- **Stale guide.** Galley flags a guide whose diff has advanced past it. Worklode does the same
  from `review_guides.round` versus `reviews.round`, and `lode review guide` regenerates.

## 10. Metrics {#sec-10}

Per 022's conventions and §8 of that spec — a nil-safe `Metrics` in `internal/review/metrics.go`,
the registerer threaded from `serve.go`, every label value bounded by a `CHECK` or an enum:

| Metric | Type | Labels / buckets |
|---|---|---|
| `worklode_review_rounds_total` | counter | `subject` ∈ `doc` \| `change`, `client` ∈ `galley` \| `web` \| `cli` \| `agent` |
| `worklode_review_threads_total` | counter | `intent` ∈ `note` \| `change_request` \| `question` |
| `worklode_review_open_threads` | gauge | `subject` |
| `worklode_review_question_latency_seconds` | histogram | 5, 15, 60, 300, 900, 3600 |
| `worklode_review_time_to_verdict_seconds` | histogram | 300, 1800, 7200, 86400, 604800 |
| `worklode_review_verdicts_total` | counter | `verdict`, `reviewer_kind` ∈ `human` \| `agent` |
| `worklode_review_skimmed_blocks_total` | counter | `decided_by` ∈ `author` \| `policy` |
| `worklode_review_guide_skims_refused_total` | counter | `rule` |
| `worklode_review_await_waiters` | gauge | — |

Three of these are load-bearing rather than decorative. **`question_latency`** measures whether
the attach loop works at all: a question channel whose median answer arrives in an hour is a
comment box, not a live review, and §3's whole design premise is the seconds-to-minutes case.
**`rounds_total{client}`** is how the interim path's retirement becomes a number — when the
`galley` share stops growing and `web` or `agent` dominates, §1.2's UI port has an argument
behind it. **`verdicts_total{reviewer_kind}`** is the check on §0's premise: if no agent ever
casts a verdict, an agent-first review system was built for humans anyway.

`skims_refused_total{rule}` is bounded by the refusal rules of §4.2 and is the honesty rule's
audit trail — a rising count means authoring agents are routinely trying to narrow their own
reviews, which is worth knowing whether or not the refusals hold.

## 11. Events and `ns/` {#sec-11}

Per 027 §3 and §4 — RDF-shaped, curie in `events.type`, JSON-LD payload, emitted in the
transaction that makes the change:

| Event | Emitted when |
|---|---|
| `wl:ReviewOpened` | a review is created, with its reviewer set |
| `wl:ReviewRoundSubmitted` | a round lands — counts, `focused`, `client` |
| `wl:ReviewQuestionAsked` | a `question` thread opens |
| `wl:ReviewQuestionAnswered` | the first author reply on a question thread |
| `wl:ReviewThreadResolved` | a thread reaches `resolved` or `obsolete` |
| `wl:ReviewVerdictRecorded` | a reviewer approves or requests changes |
| `wl:ReviewClosed` | the review closes, with the outcome |

`ns/ontology.ttl` gains `wl:Review` and `wl:ReviewThread` as classes, `wl:reviews` (Review →
Document | Task), `wl:reviewer` (Review → Agent), `wl:threadIntent`, `wl:reviewVerdict`, and one
`rdfs:subClassOf wl:Event` per row above; `ns/concept.ttl` gains `wlc:ReviewIntent`
(`note`, `change_request`, `question`) and `wlc:ReviewVerdict` (`approved`,
`changes_requested`). Per 025 §9 the Go constants and the `CHECK` fragments of §5 are generated
from `ns/`, and per `CLAUDE.md` the terms land in `ns/` alongside this spec's implementation, not
in this change.

**A review is a row, not a query** — 025 §1's test, and it passes: a review exists because
someone asked for one, which is an act. Its *outcome* ("is this document approved") is derived
from `review_reviewers`, and no column stores it twice.

This also closes a leftover. 006's mint table lists `reviewer` among the facts authored
graph-side; 025 §2 moved documents into the backbone, so `reviewer` follows them and reaches the
graph by projection like everything else. There is no authoring surface for a reviewer in the
graph, and there never was one built.

## 12. Testing {#sec-12}

- **Contract fidelity, pinned.** A recorded `ReviewResult` fixture from a real galley session
  round-trips through `POST /rounds` and back out of `GET /rounds/{n}` byte-identically modulo
  server-added fields, and a golden copy of `galley spec`'s `ReviewResult` and guide schemas is
  checked in so an upstream change is a red test rather than a silent drift. This is the single
  most important test in the spec: §1.1's entire value is that the shapes agree.
- **Anchors survive.** A thread anchored at `path:412` re-anchors by `anchor_text` after the
  author rewrites the file, and goes `obsolete` — not deleted, not re-pointed — when the line is
  gone.
- **The question rule.** With an open `question` thread, a round from the author is refused; a
  reply is accepted; the task does not leave `in_review` and the lease is not released.
- **The honesty rule.** A guide proposing `skim` on a file its author edited non-mechanically is
  refused with the rule that fired and `skims_refused_total` increments. A verdict from the
  lease-holding actor is refused. Both are server-side, per 028 §3.2's argument.
- **Verdict invalidation.** Approve at round 2, submit round 3, assert the document cannot be
  accepted.
- **Both reviewer paths.** The same review is driven to approval once entirely over the API with
  no desk, and once from a recorded galley round; the resulting rows are equivalent.
- **029 composition.** A reading diff whose `smart_diff` omits three spans yields exactly those
  three `skimBlocks`, and yields none at all when the review is not focused.
- **Metrics:** a fresh `prometheus.NewRegistry()` per 022 §7, plus the family check in
  `TestMetricsEndpointDomainFamilies`.
- **e2e** (`e2e/`, public surfaces only): open a review over a document through the HTTP API,
  comment, ask and answer a question, approve from two reviewers, accept the document — no direct
  store writes, and no galley process, which is what proves §9.

## 13. Changes to other specs {#sec-13}

| Spec | Change |
|---|---|
| 000 | §4's "reviewed via crit" resolves to this spec: the gate is `review_reviewers`, and crit names an interaction Worklode now specifies rather than an external tool it adopts |
| 011 | `in_review` gains content: entered by opening a review, left by a `changes_requested` round or by the existing merge transition (§8) |
| 025 | §3's "crit anchoring comments to sections" is §2.1's document anchor; §10's doc surface gains `lode doc export --anchors` (§7.2) |
| 028 | §4's notes are `review_threads` rows with no review (§3); §6's reviewer set and accept gate read §5's tables; §1's ladder gains the in-review question below the fixer (§3.1) |
| 029 | §7's brief entry for `kind = 'review'` is served through `lode review show`; §12's "the review surface is a separate spec" resolves here. Optionally: retain meat's edit plan so §4.1 need not recover spans by comparison |
| `ns/` | `wl:Review`, `wl:ReviewThread`, the event subclasses and two SKOS schemes (§11) |
| 005 | unchanged — `tasks.assignee` (§8) is routing, and does not enter ranking or the pickup gate |
| Migration | `0011_reviews` (§5) plus `tasks.assignee` (§8), listed in `deploy/base/kustomization.yaml` |
| follow-ups | "Review API before review UI" and "Review tasks take an assignee" are answered; `lode doc export` gains a second, stronger justification |

## 14. Out of scope {#sec-14}

- **A native web review desk.** The port of galley's UI onto §5's schema (§1.2). It is the
  intended end state and it is deliberately not v1: the API must be proven by two independent
  clients first, and §10's `client` label is the evidence that decides when.
- **Applying a review automatically.** `requestedChanges` are instructions to an agent, not
  patches. Galley makes the same choice and it is the right one — a review that edits code is a
  review nobody read.
- **Mirroring GitHub PR review threads.** A one-way projection of GitHub comments into
  `review_threads` is plausible and cheap; two-way sync is not, because it would give "approved"
  two owners. v2, one-way, or not at all.
- **Review analytics.** §10's metrics are operational. "Which reviewers approve fastest" is a
  question about people and needs an argument this spec does not have.
- **Threaded discussion outside an anchor.** Every thread anchors to something. A review-level
  conversation is `overallNote` on a round, which is where galley puts it.
- **Reviewing a review.** 028 §10 asks whether the fixer's substantivity calls should be
  spot-audited; the same question applies to skim refusals and is left open with it.

## 15. Acceptance criteria {#sec-15}

1. A galley `ReviewResult` fixture is accepted verbatim by `POST /api/v1/reviews/{id}/rounds` and
   returned unchanged by `GET .../rounds/{n}`; a golden copy of the upstream schema is checked in
   and CI fails when it drifts.
2. A review is driven from open to approved entirely over the HTTP API, with no browser, no
   galley process and no Node on the machine — and the resulting rows are equivalent to those
   produced by the galley path.
3. A `question` thread blocks a new round from the author, admits a reply, and neither releases
   the lease nor moves the task out of `in_review`.
4. `lode doc note` writes a `review_threads` row with `review_id IS NULL`, never blocks, and
   appears in the same "outstanding on this section" query as review comments.
5. A thread survives the author rewriting its anchored line by re-anchoring on `anchor_text`, and
   becomes `obsolete` rather than being deleted when it cannot.
6. A verdict cast by the actor holding the task's lease is refused, and a guide proposing a skim
   on a non-mechanically-edited file is refused with the rule that fired.
7. Submitting a new round clears every approval cast against an earlier one, and `lode doc
   accept` refuses while any assigned reviewer has no current approval.
8. `GET /api/v1/reviews/{id}/guide` emits JSON that `galley --guide` accepts without translation,
   and the same object appears in `lode review show --json`.
9. With a focused review and a `ready` reading diff, the guide's `overview` is 029's summary and
   its `skimBlocks` are exactly the spans `smart_diff` omitted; with an unfocused review, no
   skims are emitted.
10. The metric families of §10 appear on `/metrics` with bounded labels and are asserted by
    `testutil`; `worklode_review_rounds_total{client}` distinguishes the interim path from every
    other.
