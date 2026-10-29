
All seven come from one Go module and one release version, and expose the same stamped version from one shared package.

| Executable | Contract | Installed for |
|---|---|---|
| `lode` | Human-facing CLI and HTTP client | end users |
| `lode-hook` | Short-lived lifecycle and Git hook runner | end users |
| `lode-statusline` | Short-lived status-line renderer | end users |
| `lode-server` | HTTP servers and server-owned background loops | server image |
| `lode-watch` | Kubernetes pod watcher | watcher image |
| `lode-migrate` | Postgres schema migration runner | server image, init container |
| `lode-gate-check` | Offline spec-gate check of a pull request | CI only, built from source in the job |

`lode-hook` takes an event and its hook arguments, or `--harness`, `--next`, `--list`. `lode-statusline` takes the harness stdin payload and emits the status line. The server, watcher and migrator take their flags directly with no subcommand word. `lode-gate-check` takes the flags of `lode gate check` and makes no Worklode API call.
