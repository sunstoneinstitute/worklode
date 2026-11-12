# Vault-LD as a wire format for `lode show`

Research note, 2026-11-12. Question: could Vault-LD (RDF carried in
Markdown frontmatter) serve the `lode show` family (`lode show <ref>`,
`lode doc show`, `lode task show`, `lode rule show`, `--closure`,
`--inline`) as a wire format: one Markdown response carrying the entity's
body plus all of its edges and metadata, read as prose by an agent and
extracted as RDF by a machine?

**Short answer: do not adopt Vault-LD by name. Decline for now, with a
trigger.** Vault-LD is real, open (Apache-2.0) and small. It is four months
old, at 0.5.0, with no release artifacts, one Python reference script and no
Go code. Its unit is a file in a vault, not a response: identity comes from
the file name, links resolve against other files, the context lives outside
the note, and the body carries no triples. Each of those is a mismatch for a
single-response format, and the spec defines no container for several notes,
which `--closure` needs. What the format gets right for Worklode is its unit
of meaning: one note is one RDF subject with a prose body, and Worklode
already made the rule, not the section, the identity-bearing unit. So the
shape fits and the standard does not. If a machine-readable header on `show`
output is ever wanted, the safe form is a Worklode-defined YAML-LD
frontmatter generated from the triples `internal/graphproj` already emits:
Vault-LD-shaped without claiming conformance. Build it only when a consumer
that needs triples from `show` exists. Today none does.

Method: primary sources only, read on 2026-11-12. Vault-LD facts come from
the repository `The-Knowledge-Graph-Guys/vault-ld` on GitHub at `main`
(`SPEC.md`, `README.md`, `HISTORY.md`, `.github/CHANGELOG.md`,
`scripts/vault_to_rdf.py`) and its metadata page. The docs site
`vault-ld.org` does not resolve from this session's network, so the spec
was read from the repository file; the GitHub API also refused this session
for that repository, so dates come from the changelog. Worklode facts come
from the source tree at the current checkout; `make build` succeeded and
the `--help` of the four show commands was captured. No server or Postgres
was reached, so response shapes are read from `internal/model` and the
sizes in §4 come from a hand-written example. No code was changed.
Statements marked **Inference** or **Recommendation** are ours.

---

## 1. What Vault-LD is, from the spec

**Provenance and maturity (observed).** The repository describes itself as
"an open spec for Markdown vaults as linked data. YAML-LD frontmatter + a
shared `@context` = an RDF knowledge graph"
([repo](https://github.com/The-Knowledge-Graph-Guys/vault-ld)). Apache-2.0.
247 stars, 24 forks, 53 commits, 6 open issues. The Releases section is
empty and no tags are shown. The changelog lists 0.1.0 (2026-07-02), 0.2.0
(2026-07-03), 0.3.0 (2026-07-07), 0.4.0 and 0.5.0 (both 2026-07-14); the
entries after 0.3.0 are docs-site and SEO work
([CHANGELOG](https://github.com/The-Knowledge-Graph-Guys/vault-ld/blob/main/.github/CHANGELOG.md)).
No individual author is named; `HISTORY.md` says it is written "by humans
and their agents". `SPEC.md` carries no version number, date or status
field; it calls itself "the normative reference" and uses RFC 2119 keywords,
so the spec is versioned only through the repository's release numbers.

**Adoption (observed).** The only external use found is a draft ADR in
`plugin-healthcare/tara` issue #28, opened 2026-09-27, still "Proposed",
removed from its milestone on 2026-10-13, no linked pull request
([issue](https://github.com/plugin-healthcare/tara/issues/28)). That thread
records that the pinned exporter "doesn't fully support the minimal `@vocab`
context in Appendix B" and "isn't a packaged API". A web search finds
nothing else using the name. **Inference:** a published draft with one
organization behind it and no second implementation; "standard" overstates
it.

**Syntax (observed, SPEC §1, §4, §5).**

- Each Markdown file is one RDF resource. Its YAML frontmatter is read as
  YAML-LD through one shared `context.jsonld`. Keys are bare short aliases;
  prefixes appear only in values. Aliases `type` for `@type` and `id` for
  `@id` MUST be honored.
- A note MUST NOT carry its own `@context` (§4.2). The context lives outside
  the notes and may be composed from an array applied left to right. §6's
  checklist mentions "an optional per-file `@context` layered over" the
  shared one, which contradicts §4.2.
- Identity (§4.5): the IRI is the governing `@base` plus the file name
  without `.md`, percent-encoded; folders never enter it. An explicit `id`
  takes precedence and MUST be a full absolute IRI; a relative value or
  CURIE is non-conforming.
- Links (§4.4): a `[[Target]]` in a frontmatter value becomes an object IRI,
  resolved against the vault's file names. A `#fragment` is discarded. A
  note without `type` does not participate. A link to no note is dangling:
  tools MUST flag it and MAY mint an IRI.
- Literals: plain YAML scalars; datatypes come from the context, never from
  inline annotation. Multi-valued properties are YAML arrays.
- Body (§5.3): "documentation", not converted to RDF. Only frontmatter
  becomes triples. Generators MUST preserve an existing body.
- Sections: no section-level addressing; `[[name#Heading]]` resolves to the
  whole note.
- Round trip (§5): export to Turtle (`schema.ttl`, `data.ttl`), ingest
  inverts it, placement round-trips through `vld:path`. Named graphs, blank
  nodes, RDF lists and reification are not addressed anywhere.

**Processing (observed).** The reference exporter `scripts/vault_to_rdf.py`
is about 600 lines of Python on `rdflib` and `PyYAML`: parse the first
frontmatter block, compose contexts, rewrite wiki-links to IRIs, build
triples from the context's coercions. No branch handles nested mappings.
Hardened against untrusted vaults since 0.3.0. No implementation in any
other language is listed. **Inference:** a consumer needs three steps: a
YAML parse, a wiki-link rewrite, and a JSON-LD 1.1 expansion. In Go the last
would come from `piprate/json-gold` (JSON-LD 1.1, `toRDF` to N-Quads,
Apache-2.0, 317 stars, no visible releases, conformance last stated in 2020,
[repo](https://github.com/piprate/json-gold)); whether it is maintained was
not established.

**LLM and agent statements (observed).** The body is for human and LLM
readers and "holds narrative the triple store cannot" (§5.3); §7 places the
format beside agent-maintained Markdown wikis; the README credits Karpathy's
"LLM wiki" idea. Nothing is said about streaming, response formats, or an
agent consuming one note over a wire.

**Alternatives (observed).** No agreed format for RDF in Markdown exists;
every candidate is a community project.

- **YAML-LD**: the W3C JSON-LD Community Group's YAML serialization of the
  JSON-LD data model, which Vault-LD builds on. Its spec site did not
  resolve from this session.
- **MD-LD (Markdown-Linked Data)**, designed by davay42, JavaScript
  reference `davay42/mdld-parse`, Python port `alan8373/mdld-py` (MIT):
  inline `{...}` annotations on links, emphasis, headings and list items,
  for example `[Alice] {?ex:author =ex:alice .prov:Person}`; "strip the
  annotations and you are left with normal Markdown"
  ([mdld-py](https://github.com/alan8373/mdld-py)). This is the format that
  puts edges inline with the prose, not Vault-LD.
- **`ozekik/markdown-ld`** (JavaScript, last pushed April 2024 per the
  search result), **enola.dev's YAML-LD frontmatter and code-block specs**
  (site unreachable) and **`linked-markdown`** (TypeScript): the same
  frontmatter idea, none read at source. RDFa and JSON-LD `<script>` blocks
  are HTML, not Markdown.

How Vault-LD differs: frontmatter only, no inline annotation, wiki-link
values as object IRIs, one external context, identity from file names. It
is a format for a folder, not for a document.

## 2. What Worklode already has

**The show family.** `lode show <id>` classifies its argument by grammar
alone (`internal/cmd/show.go:66-94`). `runRuleShow` (`show.go:385-418`)
fetches `model.RuleDetail`, prints the raw server bytes on `--json`
(`show.go:402-405`), otherwise calls `cli.RuleDetailRender` and, with
`--inline`, folds in-force amendments through `designdoc.NewInliner`.
`runRuleClosure` (`show.go:421-436`) fetches `GET /api/v1/rules/{id}/closure`
(`internal/api/router.go:357`) and prints a table. Flags: `--closure`,
`--inline`, `--section`, `--version`, `--usage`, the kind flags, and the
global `--json` (`internal/cmd/root.go:75`). No command has a `--format`
flag, and nothing under `internal/api`, `internal/cli` or `internal/cmd`
names `text/markdown`, `text/turtle` or `application/ld+json`. The CLI's only
RDF output is `lode graph triples`, sorted N-Triples through
`graphproj.Document` (`internal/cmd/graph.go:78-134`,
`internal/graphproj/triple.go:111-127`).

**The renderers.** `RuleRender` prints the header fields, arrangements,
covering plans, governed tasks and every edge with direction and source,
then the body (`internal/cli/rules.go:67-114`); `RuleDetailRender`,
`RuleAmendmentsRender` and `RuleClosureTable` follow (`rules.go:115-131`,
`133-145`, `194-211`). `DocDetailRender` prints header, sections, body and
edges both ways (`internal/cli/docs.go:615-690`); `DocShowText` is the
Markdown both `lode show` and the MCP tool print for a document
(`docs.go:847-873`). `TaskDetailRender` prints header, hierarchy,
governance (`GovernanceRender`, `internal/cli/tasks.go:589-608`), body,
edges and more (`tasks.go:452-547`). So the edges already reach the agent,
as labelled lines beside the prose.

**The model shapes.** `model.Rule` carries `Ref`, `Kind`, `Status`,
`Version`, `ConceptIRI`, `ArrangedIn`, `CoveredBy`, `GovernedTasks`, `Edges`
(`internal/model/rule.go:8-47`); `RuleEdge.Type` is refines, needs,
constrains, conflictsWith, references, amends, supersedes or
wasDerivedFrom, with `Source` manual, derived or refactor
(`internal/model/ruleedge.go:9-18`). `RuleClosure` is `Members[0]` plus every
rule reached over refines and needs, each with the edge and the rule it came
from (`rule.go:172-190`). `DocDetail` adds `Sections` (anchor, number,
heading, the arranged rule's ref and kind), `Edges`, `EdgesIn`, `Notes`,
`Amendments` (`internal/model/doc.go:93-121`, `263-277`, `451-470`).
`TaskDetail` carries `Edges.Out`/`In`, `GovernedBy` (`model.TaskGovernance`:
rule, pinned version, current version, source), `Generated`, `Hierarchy`
(`internal/model/taskdetail.go:57-89`, `internal/model/governedby.go:1-21`).
No wire type has a `produces` or `mirrors` field: the ontology says
`wl:produces`'s "backbone projection is deferred to v2 until a
task→deliverable edge exists in the backbone" (`ns/ontology.ttl:462-473`),
and `wl:mirrors` (`ontology.ttl:855-865`) has no model field. A task
response cannot carry those two edges today, whatever the format.

**The RDF layer.** `ns/ontology.ttl`: prefixes `wl:`, `wlc:`, `dct:`,
`prov:`, `skos:`, `dcat:` (24-35); `wl:DesignDoc` (59), `wl:Task` (128),
`wl:Rule` as a subclass of `wl:Section` (491); `wl:governedBy` (347,
`wl:storedAs "task_governed_by.governedBy"`), `wl:covers` (476, plan to
rule), `wl:Arrangement`, `wl:arranges`, `wl:position` (509-526),
`wl:refines` (528), `wl:needs` (549), `wl:references` (591). The inverses
`wl:refinedBy` and `wl:neededBy` are "inferred … never stored" (539-547,
558-566). `wl:storedAs` maps each declared edge to its table and type
(1023-1026). IRIs are minted only through `internal/kg/iri`: base
`https://worklode.io/ns/`, `wlid:` instances, `wl:` terms (`iri.go:17-22`);
a rule is `id/rule/<project-id>/<number>` and "a section has no IRI … what
it holds is the rule" (`iri.go:84-89`); a document is
`id/doc/<kind>-<project>-<nnn>` (`iri.go:64-71`); `CURIE` abbreviates to
`wlid:` (`iri.go:80-82`); RDF 1.2 reifiers are IRIs keyed on the edge they
annotate (`iri.go:98-111`). `graphproj.TaskTriples` emits the task's
fields, `dct:isPartOf`, `wl:dependsOn`, `wl:followUpTo`, `wl:duplicateOf`
and the materialized inverse `wl:blocks`
(`internal/graphproj/task.go:46-93`), and no `wl:governedBy` (no match
anywhere in `internal/graphproj`). `RuleTriples` emits type, identifier,
title, kind, status, `dct:isPartOf`, `wl:lastRevisedIn` and the successor's
`dct:replaces` (`internal/graphproj/doc.go:146-174`), and no refines, needs
or references. `DocEdgeTriples` emits covers and defers (`doc.go:176-190`).
Triples live in Oxigraph named graphs and are read back by SPARQL
(`internal/overview/queries.go:14-21`). Event payloads are already JSON-LD
with `wlid:event/<id>` as `@id` (`internal/store/events.go:190`).

**Markdown handling.** Documents are stored as full Markdown with a YAML
frontmatter whose keys are "ontology property local names, not a second
vocabulary" (`status`, `issued`, `covers`, `requires`, `wasDerivedFrom`;
`internal/designdoc/frontmatter.go:11-50`), called transitional: "once
documents live in the backbone the header goes away and these become
columns" (`frontmatter.go:17-19`). `designdoc.Parse` splits the frontmatter
and cuts sections on `{#sec-N}` anchors (`designdoc.go:99-111`);
`mdblocks` reads `{#id}` heading attributes for doc and rule bodies only
(`internal/mdblocks/parse.go:52-56`); `corpusindex.ChunkDoc` chunks a spec
on its anchors (`internal/corpusindex/chunk.go:92-135`). So Worklode already
parses a YAML header with ontology-named keys over a Markdown body: the
Vault-LD shape, minus the `@context`.

**The MCP tool.** `show` returns one sentence, then the same text the CLI
prints (`cli.RuleRender` plus amendments, `TaskDetailRender`, or
`DocShowText`), with `inline` on by default (`internal/mcp/find.go:44-46`,
`96-160`, `result.go:24-41`).

**Agent surfaces.** `docs/agent-surfaces.md:11-25` registers every file that
hardcodes `lode` invocations. About thirty-four lines across the shipped
plugin skills and repo skills invoke a `show` verb, mostly `lode show <ref>
--inline` and `lode show <rule-ref> --json`. None consumes RDF from `show`.

## 3. What a Vault-LD-shaped response would look like

Real `wl:` terms and `wlid:` IRIs; not conforming Vault-LD, for the reasons
§4 lists; the flag name is a placeholder.

**A rule.** `lode show WL-REQ-1367 --ld`:

```markdown
---
id: https://worklode.io/ns/id/rule/worklode/1367
type: "[[wl:Rule]]"
identifier: WL-REQ-1367
ruleKind: "[[wlc:requirement]]"
status: "[[wlc:accepted]]"
version: 3
isPartOf: "[[wlid:doc/spec-worklode-077]]"
refines: ["[[wlid:rule/worklode/1300]]"]
needs: ["[[wlid:rule/worklode/1368]]", "[[wlid:rule/worklode/1288]]"]
references: ["[[wlid:rule/worklode/165]]"]
coveredBy: ["[[wlid:doc/plan-worklode-041]]"]
modified: 2026-10-30T14:02:11Z
---

# WL-REQ-1367 A rule is sized to its context closure

A rule is applied with its context closure alone ...

> **[amending WL-REQ-1367 (WL-RULE-1402)]:** ... (folded by --inline)
```

`refines`, `needs` and `references` are the `rule_edges` rows `wl:storedAs`
names. `coveredBy` is the inverse of a plan's `wl:covers`, never stored, so
it is presentation derived from `model.Rule.CoveredBy`. The amendment block
is prose only: Vault-LD gives it no triple.

**A closure.** `--closure` has no Vault-LD form: one resource per file, no
container for several. One note per member separated by a horizontal rule
is not something a Vault-LD processor reads as more than one note, and a
`closure:` list on the head rule loses the reached-by edge and word counts
the table shows today (`rules.go:194-211`).

**A task.** `lode show WL-1412 --ld`:

```markdown
---
id: https://worklode.io/ns/id/task/WL-1412
type: "[[wl:Task]]"
title: Fold amendments into the rule page
taskState: in_progress
taskKind: "[[wlc:feature]]"
priority: p2
inProject: "[[wlid:project/worklode]]"
governedBy: ["[[wlid:rule/worklode/1367]]", "[[wlid:rule/worklode/165]]"]
dependsOn: ["[[wlid:task/WL-1398]]"]
isPartOf: "[[wlid:task/WL-1390]]"
plannedIn: "[[wlid:doc/plan-worklode-041]]"
---

# WL-1412 Fold amendments into the rule page

governed by: WL-REQ-1367 (v3, pinned v2) ...
```

The pinned version on a `governedBy` link (`TaskGovernance.Pinned`,
`governedby.go:14`) has no place in a flat value: it is a fact about the
edge, an RDF 1.2 reifier in the graph (`iri.go:98-111`), which Vault-LD
does not address. `produces` and `mirrors` cannot appear (§2).

**A document.** A spec is an arrangement (`wl:Arrangement`,
`ontology.ttl:509-526`), so its frontmatter would carry `arranges` as an
ordered list and each rule its `position`. The body would be the rendered
rules under `{#sec-N}` headings, which Vault-LD reads as prose; every
rule's own triples would need a second note. A plan's `covers` is already a
frontmatter key in the stored body (`frontmatter.go:23`), so a plan is the
one kind that is nearly Vault-LD today, missing `id`, `type` and absolute
IRIs.

## 4. Evaluation against the goal

**Single response, all edges, prose plus RDF.** The prose half is met by the
current render, which prints every edge as a labelled line beside the body.
The RDF half is where Vault-LD does not fit a wire format:

1. *No self-description.* A note MUST NOT carry `@context`, so a response
   that stands alone names its context out of band or breaks the rule.
   Worklode publishes no `context.jsonld`; one would be derived from
   `ns/ontology.ttl`.
2. *Identity.* `id` must be an absolute IRI, so no `wlid:` CURIE there, and
   a rule's local id contains slashes a file name cannot.
3. *Links resolve against a vault.* In a one-note response every `[[...]]`
   is dangling by definition (§4.4.1); a conforming processor warns on each.
4. *No section addressing.* Worklode sidesteps this because the rule is the
   subject and a section has no IRI (`iri.go:84-89`).
5. *No multi-note container*, so no `--closure`.
6. *Body carries no triples.* "Edges inline with the prose that explains
   them" describes MD-LD. Vault-LD gives edges in a header and prose below,
   which the current render already does in another notation.
7. *Edge annotations.* Pinned versions, edge sources and amendment
   attribution are facts about edges, reifiers in the graph; Vault-LD has
   none.
8. *Inverses.* `refinedBy`, `neededBy` and `coveredBy` are inferred, never
   stored; a frontmatter cannot say which direction is the stored one.

**Token cost (measured on the §3 rule example).** The frontmatter is 435
bytes of a 622-byte note; the same facts as Turtle with prefixes are 872
bytes. So a YAML-LD header is cheaper than Turtle and about as long as the
header lines `RuleRender` prints today, both dominated by refs. `--json`
(`model.RuleDetail`) is larger: both headings of every edge, timestamps,
inline references with line numbers, field access. JSON-LD with an inline
`@context` would be the most expensive. **Inference:** the format moves
token cost by tens of percent on the header, not on the body, which is the
bulk of any rule or spec response.

**Does an agent need the triples?** Every surface that invokes `show`
reads prose and follows refs by name, or takes `--json` for a field. The
one RDF consumer is Oxigraph, fed by `graphproj`, read by
`internal/overview`; an agent wanting triples would run SPARQL or `lode
graph triples`. **Inference:** the machine half of the goal has no consumer
yet; the prose half is already served.

**Frontmatter versus inline; `--inline` and `--closure`.** A frontmatter
block is easy to generate from a `model` value and easy to strip. Inline
MD-LD annotations would be written into rule text, changing the Markdown
that `mdblocks`, `corpusindex` and the body editor's round trip read, and
new syntax for every agent. **Recommendation:** if anything, frontmatter;
never inline. `--inline` folds other rules' text into the body with an
attribution line (`internal/designdoc/inline.go:1-20`); under Vault-LD that
is plain body and the `amends` edge sits in the header as a derived
inverse. `--closure` has no conforming form.

**Where it would live.** Per the `internal/cmd` decides / `internal/cli`
renders seam: a `cli.RuleLDRender(w, model.RuleDetail)` and siblings in the
feature files, the flag picked in `show.go` beside `--json`, nothing new
across the API, so WL-RULE-1349 is untouched. IRIs from `internal/kg/iri`;
the term mapping from `graphproj`'s triple functions rendered through a
context, so the CLI never holds a second model-to-term mapping. That is the
blocker: `graphproj` emits no refines, needs, references or governedBy
today (§2), so a `show` format carrying them would be the second owner of
that mapping, against "no fact has two owners" (`CLAUDE.md`). Extending
`graphproj` first is the right order whatever the format.

**API or CLI.** An `Accept: text/markdown` profile on `GET
/api/v1/rules/{id}` is a second body format on one route, with its own
`routeDoc` and client encoding. Client-side rendering from the JSON already
fetched costs the server nothing. **Recommendation:** CLI only, if at all.

**MCP.** The tool already returns the CLI's text (`find.go:96-160`), so a
frontmatter form is a one-line switch once a renderer exists. It would also
put a YAML header in front of every document an agent reads, a cost on
every call for a consumer that does not parse it.

**Flag name.** `internal/cmd/CLAUDE.md`'s nine rules govern commands, not
flags. Output switches in the tree are booleans (`--json`, `--inline`,
`--editable`, `--anchors`); no `--format` exists. A boolean beside `--json`
(`--ld`) fits, and should not say "vault-ld" unless the output conforms.

## 5. Contrary evidence and risks

- *Spec immaturity.* Four months old, 0.5.0, no release artifacts, an
  inconsistency on per-note `@context`, no multi-note container,
  reification and named graphs unaddressed. One known adopter, still a
  draft ADR, which found the exporter incomplete for Appendix B.
- *No Go implementation.* A Go consumer would be written from the spec on
  `json-gold`, whose maintenance status was not established.
- *A second owner of triples.* A `show` renderer mapping model fields to
  `wl:` terms on its own is a second projection beside `graphproj`, which
  lacks exactly the edges the brief cares about.
- *Surface maintenance.* A new output form is one more thing for the plugin
  skills, the command catalog and the MCP tool to describe
  (`agent-surfaces.md`), with no surface asking for it.
- *Vault-LD's direction.* Built for vaults on disk. Worklode's documents
  left the file corpus for Postgres (WL-SPEC-77) and the transitional
  frontmatter is scheduled to become columns (`frontmatter.go:17-19`). An
  identity rule of "base plus file name" points the other way.
- *In favour.* The unit of meaning matches: one subject, one prose body,
  ontology-named keys, which stored documents already have. A header is
  cheap once `graphproj` carries the edges, and a plan is nearly there.

## 6. Recommendation and open questions

**Decline, with a trigger.** Do not add a Vault-LD output to `lode show`
now, and do not name Vault-LD in any surface. If a machine-readable header
is wanted later, build it as a Worklode-defined YAML-LD frontmatter
generated from `graphproj` triples through a published `context.jsonld`
derived from `ns/ontology.ttl`, behind a boolean flag, CLI-side, after
`graphproj` projects refines, needs, references and governedBy. Call it
YAML-LD in its help; call it Vault-LD only if a conformance check against a
released spec passes.

Triggers that would change this:

1. A consumer that needs triples from a single `show` response: an agent
   harness keeping a local graph of what it has read, or a client that
   cannot reach Oxigraph. None exists today.
2. Vault-LD reaching a 1.0 with a stated status, a multi-note or stream
   form, and an implementation outside Python.
3. `graphproj` growing the rule and governance edges, which removes the
   two-owners risk and makes the renderer a formatting job.

Open questions the evidence cannot settle: whether `vault-ld.org` publishes
a different, versioned text from the repository's `SPEC.md` (site
unreachable); whether YAML-LD's status (a W3C Community Group draft, per
secondary sources) is firm enough to build on (site unreachable); whether
`json-gold` is maintained; the real token delta on live responses; and
whether agents would use a header at all. The cheapest test of the last is
not a renderer but a week of MCP `show` logs showing whether any client
re-reads the edge lines it already gets.
