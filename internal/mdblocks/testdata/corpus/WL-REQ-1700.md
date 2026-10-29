
The CLI rewrites `/blob/…` to `<server>/blob/…` so URLs are clickable, and `lode task show` lists attachments under the body (filename, size, media type, URL). Glamour renders an image as alt text plus link. Inline terminal images are out of scope.

`lode task brief --json` carries a `blobs` array, each with absolute `url`, `filename`, `media_type`, `size`, and `embedded`, fetchable with the agent's bearer token (WL-SPEC-80). Alt text stays in the body.

