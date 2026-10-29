
| Kind | Source | Unit of retrieval |
|---|---|---|
| `doc` | `docs.body`, split on `doc_sections` | a section |
| `task` | `tasks.title` plus `tasks.body` | the task |
| `skill` | `skill_versions.skill_md` of the latest version, description prepended | the skill |

Events, transcripts, inbox items, blobs, task comments and rules are not indexed. A rule's text is found through the section that arranges it. Chunks of a deleted subject go by FK cascade, so the index carries no tombstones.

