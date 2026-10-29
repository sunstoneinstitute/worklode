
`config.toml` (`~/.config/worklode/`) holds only `server`. The token never goes there.

Resolution order in `LoadConfig`: `LODE_TOKEN` env, then keychain keyed by server URL, then the token file keyed by server URL.

| Store | Details |
|---|---|
| OS keychain | `github.com/zalando/go-keyring`: macOS Keychain, Linux Secret Service, Windows Credential Manager. Service `worklode`, account = server URL, so one machine holds tokens for several servers and `LODE_SERVER` selects one. |
| Token file | `~/.config/worklode/token`, mode 0600, one `<server> <token>` line per server. Written temp-file-plus-rename, which also resets a looser mode. Deleting the last token removes the file. Used only when no keychain exists; login prints the path when it takes this route. |

The `tokenStore` abstraction (`Get/Set/Delete(server)`) has keychain, file and mock (`keyring.MockInit()`) implementations. Absence earns the fallback, failure does not: `keychainAvailable` probes for an account no server URL could equal. `ErrUnsupportedPlatform`, a D-Bus `ServiceUnknown`/`NameHasNoOwner` reply, or no reachable bus count as absence. A locked collection or dismissed prompt is an error carrying `export LODE_TOKEN=…` guidance; that failing path still records `server` in `config.toml`.

`lode logout` deletes the current server's token from both stores. In-cluster automation uses `LODE_TOKEN`.

