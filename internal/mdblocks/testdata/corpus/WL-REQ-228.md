
| Surface | Purpose |
|---|---|
| `POST /api/v1/blobs` | streamed raw upload; returns `{hash, media_type, size, url}` plus `poster_url` for a video. Idempotent: identical bytes return `200` and the existing row. |
| `GET /blob/{hash}` | authenticate, then redirect to a presigned URL |
| `GET /api/v1/tasks/{id}/blobs` | list a task's blobs: hash, filename, media type, size, embedded, attached |
| `POST /api/v1/tasks/{id}/blobs` | attach an uploaded hash (`attached = true`) |
| `DELETE /api/v1/tasks/{id}/blobs/{hash}` | clear `attached`; the row goes if `embedded` is also false |
| `lode task attach <id> <file>…` | upload, then attach. Images also get `![<basename>](/blob/…)` appended to the body; videos get a `<video src poster controls preload="metadata">` element. `--no-embed` suppresses the append. |
| `lode task attach <id> -` | one blob from stdin |
| `lode task detach <id> <hash>` | the inverse; warns if the body still embeds it |
| `lode task add --body-file`, `lode task edit --body-file` | reference rewriting (§8.5) |
| `POST /api/v1/blobs/gc` | both GC sweeps (admin); `dry_run` reports, otherwise deletes |
| `lode blob gc [--apply] [--grace-hours N]` | calls the GC endpoint; reports unless `--apply` |

