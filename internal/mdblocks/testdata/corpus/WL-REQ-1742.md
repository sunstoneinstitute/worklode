
| Table | Columns |
|---|---|
| `skills` | `id`, `qualifier`, `name`, `description`, `source_repo`, `source_path`, `latest_version_id`, `deleted_at`; unique `(qualifier, name)` (WL-REQ-321) |
| `skill_versions` | `id`, `skill_id`, `git_commit`, `content_hash` (sha256 over the canonicalized skill dir), `frontmatter` jsonb, `skill_md` (body inlined for briefs and indexing), `archive` bytea (tar of the skill dir), `created_at`; unique `(skill_id, content_hash)` |

A skill's chunks and embeddings live in the corpus index (`index_chunks`, subject kind `skill`), described in WL-SPEC-79#sec-14.

