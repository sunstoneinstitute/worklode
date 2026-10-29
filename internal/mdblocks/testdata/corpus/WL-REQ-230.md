
Upload streams and never buffers: `http.MaxBytesReader` at `maxBlobBytes = 100 MiB`, spool to a temp file in `LODE_BLOB_SPOOL_DIR` through a `TeeReader` into sha256, capture 512 bytes for `http.DetectContentType`, then look up the hash. Present: discard the temp file and return the existing row. Absent: `PutObject` from the rewound file, then insert the row. Write order is object-then-row, so failure leaves an orphan object (swept by WL-REQ-232), never a dangling row. The client `Content-Type` is never trusted or stored. Nothing is rejected on type.

| Class | Types | Treatment |
|---|---|---|
| embeddable image | `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `image/svg+xml` | rendered inline; `inline` |
| embeddable video | `video/mp4`, `video/webm` | rendered via `<video>` with a poster frame; `inline` |
| attachment | anything else | never rendered; `attachment` |

A video upload also runs `ffmpeg` over the spooled file before the dedup check and stores the first frame as an ordinary JPEG blob, returned as `poster_url`. Without `ffmpeg` the video is stored and `poster_url` is omitted; `worklode_video_poster_extractions_total` records which happened. SVG is embeddable deliberately; a script inside it runs in the object store's origin and an SVG loaded through `<img>` runs no script.

