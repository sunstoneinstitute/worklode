
Worklode's own `[gate]` table guards these paths:

| Path | Why |
|---|---|
| `internal/api/router.go`, `internal/api/authz.go` | routes, guards, permissions |
| `internal/cmd/**` (excluding `_test.go`) | the command surface |
| `internal/model/**` | every wire shape |
| `deploy/base/**` (migrations) | stored nouns |
| `internal/store/*.go` where a state machine or ranking lives | rules |
| `internal/hooks/**`, `internal/watcher/**`, `internal/eventbus/**` | automatic behaviour |
| `ns/*.ttl` | the ontology |
| `plugins/**/skills/**`, `plugins/**/commands/**` | what agents are told to do |

