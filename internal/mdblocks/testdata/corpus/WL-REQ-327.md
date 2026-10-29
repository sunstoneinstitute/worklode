
`lode doc fetch <ref>...` fetches named documents; `lode doc fetch --all` fetches every document the backbone holds for the project. It is a client-side cache fill and needs only `docs:read`. `fetch` is an L3 allowlist verb: `show` renders a document to stdout, `fetch` stores its current version on disk, and neither of the seven canonical verbs expresses the second. Documents land verbatim, frontmatter included, at `.worklode/cache/docs/<KEY>-<TYPE>-<n>.md` with a sibling `.meta.json` recording version and fetch time. `.worklode/cache/` is gitignored in its entirety.

