
| Instance | Cluster | Overlay | Host |
|---|---|---|---|
| prod | admin | `deploy/overlays/admin/` | `worklode.sunstoneinstitute.ai` |
| dev | hzdev | `deploy/overlays/hzdev/` | `worklode.dev.sunstoneinstitute.ai`, and `worklode.dev.sunstone.internal` on the internal ingress |

Only dev runs today. `deploy/overlays/hzprod/` is a placeholder overlay with an internal-only ingress that no cluster consumes. The admin overlay replaces it.

Prod runs in the admin cluster because reaching it is worth the org: every task, brief and event across projects, token minting through the bootstrap token in the pod environment, encrypted GitHub user tokens whose key sits in the same pod, approvals, and the Postgres behind all of it. The admin cluster already holds Keycloak and has the narrowest set of people and pipelines that can change it. `hzprod` runs production workloads with a one-application blast radius, and the observer should not share the fate of what it observes.

