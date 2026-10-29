
`catalog.toml` is a hand-rolled TOML subset (`internal/secrets.ParseCatalog`). An entry has one of two shapes.

```toml
[GITHUB_TOKEN]                      # plain
ref = "op://Employee/GitHub agent token/credential"
description = "GitHub credential the agent operates as"
baseline = true

[KUBECONFIG_HZDEV]                  # templated
description = "Kubernetes access to the hzdev cluster"
template = "kubeconfig-hzdev.yaml"   # sibling key in the same Secret
env = "KUBECONFIG"
cred.CLIENT_CERT = "op://Infrastructure/hzdev kubeconfig/client-cert"
cred.CLIENT_KEY = "op://Infrastructure/hzdev kubeconfig/client-key"
```

| Key | Rule |
|---|---|
| `ref` | One `op://` reference. Mutually exclusive with `template`; every entry has exactly one of the two. |
| `template` | Names a sibling key in the projected Secret holding the template text. Requires at least one `cred.` key. |
| `cred.<PLACEHOLDER>` | Placeholder to `op://` reference. Invalid without `template`. |
| `env` | Exported environment-variable name at exec. Defaults to the entry name. Any shape. Two entries for one task resolving to the same `env` is an exec-time error naming both. |
| `description` | Shown at the consent prompt. |
| `baseline` | `true` marks a secret every task needs (commit signing key, GitHub credential). Packed for every claim, exempt from consent. Default `false`. |

A templated entry exists because an OS keystore item is size-capped (about 2.9 KB raw on macOS through go-keyring, 2560 bytes on Windows) and a kubeconfig is 3 to 6 KB. A value over the cap is a modelling error: only the client credentials are secret, and the server URL, CA certificate and context names are configuration. Chunking across items is rejected. Every credential of a templated entry must itself fit the cap.

