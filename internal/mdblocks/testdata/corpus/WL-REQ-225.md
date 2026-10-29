
Blobs serve two jobs. An embedded blob is an image or video the body cites inline and is rendered in place. An attached blob is a file (log, HAR, dataset, core dump) that is downloadable and never rendered. Every attachment is a blob; only image and video blobs are embeddable. Blobs attach to tasks only; `blobs` is unscoped so a `section_blobs` table could reference the same bytes.

