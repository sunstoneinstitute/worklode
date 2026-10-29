
| Key | Example |
|---|---|
| `LODE_BLOB_ENDPOINT` | `https://hel1.your-objectstorage.com` |
| `LODE_BLOB_BUCKET` | `sunstone-worklode-blobs` |
| `LODE_BLOB_REGION` | `hel1` |
| `LODE_BLOB_ACCESS_KEY` / `LODE_BLOB_SECRET_KEY` | 1Password via ESO (WL-SPEC-74) |
| `LODE_BLOB_SPOOL_DIR` | temp-file directory; defaults to `os.TempDir()` |

`aws-sdk-go-v2` with `UsePathStyle: true`. With no endpoint set, uploads and `GET /blob/{hash}` return `501` and everything else is unaffected. Once endpoint and bucket are set, both keys are required and a missing one fails startup.


