
The row is the link: a link exists exactly when a row exists, and unlinking deletes it.

| Column | Notes |
|---|---|
| `actor_id` | PK, references `actors` |
| `github_user_id` | Unique. The durable external identity. |
| `github_login` | Display only, refreshed on re-link |
| `token_ciphertext` | AES-GCM under `LODE_TOKEN_ENC_KEY`, sealing `{access_token, refresh_token, access_expires_at}`. Sealing lives in `internal/tokencrypt`. |
| `status` | `active` or `broken` |
| `created_at`, `updated_at` | |

