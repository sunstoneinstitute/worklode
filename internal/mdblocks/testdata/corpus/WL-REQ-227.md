
Bodies store a root-relative permanent URL: `![alt](/blob/9f2a…c1)`. `GET /blob/{hash}` authenticates, then 302-redirects to a presigned object-storage URL with a 5-minute lifetime; the redirect carries `Cache-Control: private, max-age=60` and `Referrer-Policy: no-referrer`. Bytes never pass through the application. The route sits outside `/api/v1`.

| Header on the presigned response | Source |
|---|---|
| `Content-Type` | set as object metadata at upload and overridden per request via `response-content-type` from `blobs.media_type` |
| `Content-Length` | the object store's own |
| `Content-Disposition` | `response-content-disposition`: `inline` for embeddable types, `attachment` otherwise, plus `filename="…"` when the reference names one |
| `Cache-Control` | `response-cache-control: private, max-age=31536000, immutable` |

The filename travels with the reference. Every surface that lists a reference appends `?filename=crash.log`, and the route echoes it into the disposition, RFC 6266-encoded and cleaned (control characters stripped, last path segment only, invalid UTF-8 or over 200 bytes refused). Body text is never rewritten this way, so an embedded `](/blob/<hash>)` keeps matching the anchored grammar that pins it against GC. The parameter is unsigned on our side; it changes neither the bytes, the media type, nor the inline/attachment token.

