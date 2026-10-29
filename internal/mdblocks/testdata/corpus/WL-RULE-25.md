
A coding agent in a provider-managed cloud sandbox has a fresh checkout, an ephemeral filesystem, no keychain and no operator. It is an ordinary CLI client: the backbone is reachable over the public API ingress, so it claims, heartbeats and closes tasks as a laptop does. Leases and worktrees, commits as heartbeat, the `LODE_SERVER`/`LODE_TOKEN` environment override and lazy per-task skill fetch all apply. What the sandbox needs is provisioning, plus one ordering rule (5.5). The agent image in use today is `.worklode/Dockerfile`, a standalone image.

