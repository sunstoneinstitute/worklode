
`<ref>` is a corpus number with kind (`--spec 77`, `--adr 7`, `--plan 7`, or `--kind` with a positional), a slug, or the shorthand `WL-SPEC-77` (with `WL-SPEC-77#sec-9` as a shorter `--section`). A ref must match exactly one document; an ambiguous ref is an error listing the candidates. The candidates are the documents bearing the name asked for and nothing else: a number-led slug names the document whose slug it is, and where none carries it the ref names none.

A bare number is the one ref form that is not universal. At `lode show` it is a task id; at `lode doc <verb>` it is a document id. In the ref grammar it is a per-kind corpus number, so `26` naming a spec and a plan is a real ambiguity. Every other form resolves identically on `lode show`, `lode doc todo`, `lode doc show`, and `lode doc versions`. A shorthand naming another project resolves against that project's backbone docs (§3.2 tier 2). A ref with no key resolves against the current project first, then the org's documents.

A kind flag always means the current project's documents and resolves by bare number even without a `project_key`. A positional shorthand whose key cannot be established is `unresolved`.

