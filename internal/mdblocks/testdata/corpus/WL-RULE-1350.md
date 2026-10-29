
| Capability | Section | Builds it |
|---|---|---|
| Prod in the admin cluster: `deploy/overlays/admin/`, promotion to it, CI checks of the overlay invariants, retiring `hzprod` | 4.1, 4.3 | WL-PLAN-4 |
| `scripts/bootstrap.sh` as the one environment description, with CI calling it | 5.1 | WL-PLAN-1 |
| Worker image family `docker/base` and `docker/generic` | 5.2 | WL-PLAN-1 |
| Content-addressed worker-image tags | 5.3 | WL-PLAN-1 |
| Sandbox e2e: a session with only `LODE_SERVER` and `LODE_TOKEN` claims and closes a task | 9 | WL-PLAN-1 |
| `.worklode/egress.txt` and the computed baseline host list (`lode egress`) | 5.4 | WL-PLAN-124 |
