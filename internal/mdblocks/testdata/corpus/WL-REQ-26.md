
`scripts/bootstrap.sh` takes a bare checkout to a working environment and is the only description of that environment. CI, the worker image build, a laptop and a sandbox all call it. It reads each tool's version from its one source instead of restating it.

| Tool | Single source | Consumed via |
|---|---|---|
| Go | `go.mod` | `go-version-file: go.mod` in every workflow |
| templ | `tool github.com/a-h/templ/cmd/templ` in `go.mod` | `go tool templ` |
| Tailwind | `scripts/tailwind.sha256` (version plus per-platform SHA256) | `scripts/fetch-tailwind.sh` |

No tool manager (`mise`, `.tool-versions`) is used. The trigger to adopt one is a second runtime needing a pin no existing source carries. System packages live in the Dockerfile, which is Linux-only. `bootstrap.sh` fails loudly naming what a laptop lacks. No cross-platform package-name schema exists.

