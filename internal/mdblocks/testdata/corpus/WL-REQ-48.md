
The only API credential is an opaque token, `wl_` plus 40 hex characters. The server stores its hash. Anything that is not that shape is treated as a hash, never as a plaintext. Single sign-on is one way to mint an actor-scoped token.

| Token shape | `tokens.task_id` | Acts as | Minted by | Expiry |
|---|---|---|---|---|
| Actor-scoped | NULL | The actor it was minted for | `lode login`, `POST /auth/oidc/token`, an admin, or `LODE_BOOTSTRAP_TOKEN` at boot | 30 days for login tokens |
| Task-scoped | The bound task | An agent actor, `sandbox` by default | `POST /api/v1/tasks/{id}/tokens`, called by an operator or dispatcher | The task lease's TTL by default. Renewing the lease extends it. |

A task-scoped token's description names the actor that minted it (`task-scoped token minted by <actor>`). Authorization never reads it.

