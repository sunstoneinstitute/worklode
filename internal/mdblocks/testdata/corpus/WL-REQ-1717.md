
| Metric | Type | Labels |
|---|---|---|
| `worklode_index_chunks` | gauge | `subject_kind` |
| `worklode_index_chunks_without_vector` | gauge | |
| `worklode_index_subjects_stale` | gauge | `subject_kind` |
| `worklode_index_reembed_total` | counter | `subject_kind`, `outcome` (`ok`, `error`) |
| `worklode_index_kind_abandoned_total` | counter | `subject_kind` |
| `worklode_index_convergence_duration_seconds` | histogram | |
| `worklode_search_requests_total` | counter | `mode`, `outcome` |
| `worklode_search_duration_seconds` | histogram | `mode` |
| `worklode_search_arm_empty_total` | counter | `arm` |

Search `outcome` is `ok|error|empty`. Duration is labelled by `mode` because both arms run in one statement. Three signals matter: `worklode_index_subjects_stale` must return to zero every pass, `worklode_index_kind_abandoned_total` above zero means the provider is failing, and `worklode_search_arm_empty_total{arm="lexical"}` rising while the dense arm is busy signals a broken `tsv`.

