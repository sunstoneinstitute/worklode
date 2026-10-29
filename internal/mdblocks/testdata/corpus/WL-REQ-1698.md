
Task bodies are untrusted (imported issue text lands in `tasks.body`). The web UI renders with goldmark (GFM, `html.WithUnsafe()` so raw HTML reaches the sanitiser) then bluemonday `UGCPolicy()` with tightenings: `img[src]`, `video[src]`, `source[src]` must match `^/blob/[0-9a-f]{64}$`; `a[href]` limited to `http`, `https`, `mailto` with `rel="nofollow"`; `video` allowed with `controls`, `preload`, `poster`. Board and project pages show titles only.

