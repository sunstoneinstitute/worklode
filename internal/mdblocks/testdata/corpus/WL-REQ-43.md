
| Area | Check |
|---|---|
| Executables | Each entry point's argument contract; dependency test over `lode-hook` and `lode-statusline` transitive imports; managed-binding upgrade and uninstall; Windows archive and Scoop manifest expose three user executables; Homebrew tests the same three; container checks for server entry point, migrate init command and watcher image |
| Sandbox | A session holding only `LODE_SERVER` and `LODE_TOKEN`, no config file, no keychain, claims and closes a task through `e2e/`; CI builds `docker/base` and `docker/generic` and runs `bootstrap.sh` inside each; changing a tagged input changes the tag and changing nothing keeps it |
| Metrics | Per-package tests with a fresh registry and `testutil`; `TestMetricsEndpointDomainFamilies` wires one registry through store and server and asserts the families on admin `/metrics`; `TestHealthzAndMetricsNotOnPublicHandler` |
| Usage accounting | Repeated OTLP delivery yields the same total; a Codex completed-response log yields usage without a rollout file; a bound task session is charged to its task and an unbound parent session once to overhead; Edge Agent restart preserves unreported usage; `lode install` converges on a second run; no installer path enables content telemetry |
| Overhead | A repeat report replaces; a shrinking report drops a day's rollup row; a session's tokens count once whichever bucket they land in, in both reclassification directions; `ProjectCost` combines and `TaskCost` is unaffected; route-guard boot check covers the route |

