
Agent actors are provisioned per authority. The one test for whether a split deserves its own actor row: would we ever revoke this one's credential without revoking the other's, or grant it a permission the other lacks? If no, it is one actor. Wanting to see two things apart in a report is answered by a query over data already recorded, never by a new identity.

| Rejected dimension | Where that fact already lives |
|---|---|
| Specialty (`security-agent`, `reviewer`) | The task's `kind`, the skills the task declares, the project's role-labelled participant rows. Agents are never Crew members (WL-SPEC-75). |
| Harness (`claude`, `codex`) | `agent_sessions.agent` and `agent_version` on every session (WL-SPEC-80). No permission differs between harnesses. |
| Model and effort (`sonnet-worker`) | Per-usage-bucket rows priced from `model_prices`. Model is chosen during execution, after the credential exists. |
| Launch profile (prompt, model, skill set) | Configuration owned by the plugin and repo that dispatch the agent. A task that needs a specific setup says so in its body or its declared skills. |

An unattended automation gets its own actor only when it needs a `grants` row `sandbox` does not have (WL-REQ-49). It lands together with that permission difference and a written reason. An actor with the same grants as `sandbox` is `sandbox` under another name.

