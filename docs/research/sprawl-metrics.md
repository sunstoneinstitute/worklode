# Spec sprawl metrics: old corpus vs new corpus

Computed by `sprawl.py` over the 47 inline-exported WL-SPEC documents (OLD) and the 11 documents of `docs/specs2/` (NEW). Unit is a leaf section (H3, or H2 without H3 children). Tokens are words times 1.3.

## Summary

- Size: OLD 47 docs, 749 leaves, 269,048 tokens. NEW 11 docs, 337 leaves, 88,357 tokens.
- M1 amendment load: OLD 23 effective and 45 pending markers on 5.7% of leaves, amendment text is 0.1% of base text. NEW 0.
- M2 topic dispersion: mean docs per topic OLD 28.3 vs NEW 9.4. Mean topics per doc OLD 5.4 vs NEW 7.7. Dispersion share OLD 82.7% vs NEW 74.7%.
- M3 reading cost: summed over topics, whole-document load OLD 1,756,724 tokens vs NEW 698,451. Ratio whole/leaf OLD 3.4x vs NEW 4.3x.
- M4 cross-references: OLD 3.0 per 1k tokens on 45.0% of leaves. NEW 2.4 per 1k on 32.0%.
- M5 staleness: `lode` invocations not in commands.txt OLD 291 (62 distinct) vs NEW 22 (14 distinct). `/lode-` spellings OLD 41 vs NEW 3.
- M6 redundancy: near-duplicate cross-document leaf pairs OLD 2 (0.5% of leaves) vs NEW 0 (0.0%).
- M7 chain depth: OLD longest amend chain 1, sections amended by two or more documents 4. NEW has no amendment chain.
- Leaves matching no topic: OLD 22.0%, NEW 25.8%.
- The NEW corpus is a rewrite, so lower M1 and M7 are by construction. M2, M3, M5 and M6 are the metrics that measure the reorganisation itself.
- Reading M3: the ratio is how many tokens of unrelated text an agent loads per token it actually needed.

## M1 Amendment load

| Corpus | Effective markers | Pending markers | Leaves with 1+ | Amend tokens | Base tokens | Amend share | Leaves with 2+ |
|---|---|---|---|---|---|---|---|
| OLD | 23 | 45 | 5.7% | 231 | 268,817 | 0.1% | 13 |
| NEW | 3 | 0 | 0.6% | 80 | 88,277 | 0.1% | 1 |

## M2 Topic dispersion

| Topic | OLD docs | OLD tokens | OLD HHI | OLD dispersion | NEW docs | NEW tokens | NEW HHI | NEW dispersion |
|---|---|---|---|---|---|---|---|---|
| data-model | 42 | 102,771 | 0.07 | 85.9% | 11 | 31,669 | 0.14 | 81.2% |
| api | 34 | 46,871 | 0.06 | 85.4% | 11 | 18,896 | 0.13 | 75.9% |
| cli | 18 | 23,903 | 0.13 | 73.5% | 8 | 8,368 | 0.20 | 70.8% |
| ui | 16 | 17,102 | 0.11 | 83.4% | 7 | 6,961 | 0.30 | 50.4% |
| identity-authz | 28 | 64,665 | 0.07 | 87.5% | 10 | 21,748 | 0.15 | 74.8% |
| deployment-ops | 23 | 27,013 | 0.15 | 68.7% | 6 | 6,279 | 0.21 | 70.2% |
| background-jobs | 26 | 49,700 | 0.10 | 80.9% | 11 | 20,043 | 0.13 | 78.5% |
| agent-harness | 30 | 75,838 | 0.09 | 80.4% | 11 | 21,004 | 0.14 | 77.1% |
| design-process | 38 | 112,288 | 0.07 | 83.3% | 10 | 29,078 | 0.18 | 70.4% |

Mean docs per topic: OLD 28.3, NEW 9.4. Mean topics per doc: OLD 5.4, NEW 7.7. Token-weighted dispersion share: OLD 82.7%, NEW 74.7%.

Topics per document (NEW):

- 01-system-and-deployment.md: 9
- 02-identity-actors-and-secrets.md: 7
- 03-tasks-and-execution.md: 9
- 04-done-verification-workflows.md: 7
- 05-documents.md: 7
- 06-design-queries-intents-decks.md: 8
- 07-knowledge-graph-and-search.md: 8
- 08-agent-harness-and-sessions.md: 7
- 09-cli-and-skills.md: 7
- 10-cockpit.md: 9
- 11-design-authority-gate.md: 7

Topics per document (OLD), distribution:

| Topics | Docs |
|---|---|
| 1 | 1 |
| 2 | 1 |
| 3 | 5 |
| 4 | 7 |
| 5 | 8 |
| 6 | 13 |
| 7 | 6 |
| 8 | 5 |
| 9 | 1 |

## M3 Reading cost per topic

| Topic | OLD whole docs | OLD leaves | OLD ratio | NEW whole docs | NEW leaves | NEW ratio |
|---|---|---|---|---|---|---|
| data-model | 258,743 | 102,771 | 2.5x | 88,357 | 31,669 | 2.8x |
| api | 225,502 | 46,871 | 4.8x | 88,357 | 18,896 | 4.7x |
| cli | 145,277 | 23,903 | 6.1x | 67,482 | 8,368 | 8.1x |
| ui | 119,675 | 17,102 | 7.0x | 61,996 | 6,961 | 8.9x |
| identity-authz | 198,014 | 64,665 | 3.1x | 82,476 | 21,748 | 3.8x |
| deployment-ops | 180,364 | 27,013 | 6.7x | 51,204 | 6,279 | 8.2x |
| background-jobs | 191,896 | 49,700 | 3.9x | 88,357 | 20,043 | 4.4x |
| agent-harness | 193,446 | 75,838 | 2.6x | 88,357 | 21,004 | 4.2x |
| design-process | 243,807 | 112,288 | 2.2x | 81,865 | 29,078 | 2.8x |
| total | 1,756,724 | 520,151 | 3.4x | 698,451 | 164,046 | 4.3x |

## M4 Cross-reference density

| Corpus | References | Per 1k tokens | Leaves with 1+ |
|---|---|---|---|
| OLD | 796 | 3.0 | 45.0% |
| NEW | 215 | 2.4 | 32.0% |

`lode doc lint` on the live corpus: 0 dangling references.

## M5 Staleness

| Corpus | Stale `lode` invocations | Distinct | `/lode-` spellings |
|---|---|---|---|
| OLD | 291 | 62 | 41 |
| NEW | 22 | 14 | 3 |

Most frequent stale forms, OLD: `lode hook` (22), `lode secrets exec` (19), `lode worktree next` (15), `lode reconcile` (14), `lode statusline` (13), `lode serve` (12), `lode worktree resume` (11), `lode drift` (11)

Most frequent stale forms, NEW: `lode review show` (3), `lode review set` (3), `lode review add` (2), `lode review exec` (2), `lode review note` (2), `lode review` (2), `lode plugin ships` (1), `lode plugin with` (1)

## M6 Redundancy

| Corpus | Near-duplicate pairs (Jaccard >= 0.5, 5-word shingles, different docs) | Leaves with a near-duplicate elsewhere |
|---|---|---|
| OLD | 2 | 0.5% |
| NEW | 0 | 0.0% |

## M7 Chain depth

| Corpus | Longest amend/supersede chain | Sections amended by 2+ documents |
|---|---|---|
| OLD | 1 | 4 |
| NEW | 1 | 1 |

## Keyword lists

A leaf is on a topic when at least two distinct keywords from the list match as whole words (case-insensitive, except all-caps keywords which match exactly).

- data-model: `table`, `tables`, `column`, `columns`, `schema`, `migration`, `migrations`, `row`, `rows`, `edge`, `edges`, `primary key`, `foreign key`, `index`
- api: `route`, `routes`, `endpoint`, `endpoints`, `POST`, `GET`, `PATCH`, `DELETE`, `handler`, `handlers`, `JSON body`, `/api/v1`, `status code`, `HTTP`
- cli: `lode `, `flag`, `flags`, `command`, `commands`, `--json`, `subcommand`, `stdout`, `exit code`
- ui: `page`, `pages`, `cockpit`, `panel`, `templ`, `button`, `click`, `tab`, `modal`, `render`, `web UI`
- identity-authz: `token`, `tokens`, `OIDC`, `permission`, `permissions`, `role`, `roles`, `actor`, `actors`, `session`, `Keycloak`, `bearer`, `login`
- deployment-ops: `cluster`, `pod`, `pods`, `Flux`, `Postgres`, `metrics`, `Prometheus`, `env var`, `environment variable`, `deploy`, `Kubernetes`, `namespace`, `Dockerfile`, `compose`
- background-jobs: `subscriber`, `subscribers`, `sweeper`, `sweep`, `loop`, `indexer`, `webhook`, `webhooks`, `event`, `events`, `informer`, `background`, `cron`
- agent-harness: `hook`, `hooks`, `worktree`, `worktrees`, `statusline`, `plugin`, `plugins`, `skill`, `skills`, `Claude Code`, `Codex`, `agent`, `transcript`
- design-process: `spec`, `specs`, `plan`, `plans`, `task kind`, `review`, `acceptance`, `amend`, `amended`, `supersede`, `superseded`, `ADR`, `draft`, `accepted`

## What each metric fails to see

- M1 counts the folded markers the export emits (`**[amending ...]**`, `**[superseding ...]**`, `> Pending ...`). Amendments stated in prose inside the amending document are not counted.
- M1 amendment tokens count only the paragraph after each folded marker, so a multi-paragraph amendment is under-counted.
- M1 counts markers, not how much the amendment changed the meaning. A one-line clarification and a full rewrite count the same.
- M2 and M3 depend on keyword lists. A section about the claim transaction that never says `table` is invisible to data-model, and a glossary that mentions everything looks like it is about everything. The two-keyword threshold cuts noise but also drops short sections.
- M3 assumes an agent loads whole documents. With `--section` selectors or search the real cost is between the two columns.
- M4 counts references without knowing which ones are needed. Low density can mean self-contained or under-linked.
- M5 matches the first two words after `lode`. Prose like `lode itself` is filtered by a stop list, but a valid command with a renamed flag passes, and a command used only in an example of old behaviour is counted as stale.
- M6 catches copied text only. Two sections that state the same rule in different words are not seen, and a deliberately repeated definition counts as redundancy.
- M7 reads chain depth from folded markers, so a chain deeper than two shows as two.
- None of the metrics measure whether the text is correct against the code.
