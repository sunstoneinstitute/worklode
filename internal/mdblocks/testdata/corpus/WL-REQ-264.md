
Three rules hold throughout.

| Rule | Meaning |
|---|---|
| CLI over MCP | Agents drive `lode --json`. No MCP server, no per-tool schema tokens in context. |
| Hooks over prompts | Lease renewal, resume, and release are compiled hooks firing on editor and git events. The model is never asked to remember a heartbeat. |
| Server-side selection over agent reasoning | `lode task claim --next` ranks and leases in one transaction (WL-SPEC-75 §7). The agent never lists, picks, then claims. `lode task claim <id>` claims a specific task. |

Machinery does acquire, bind, renew, resume, release, and context assembly. The model does done/block/release judgment and design authoring. Product is Worklode, CLI is `lode`, slash commands are `/lode:<name>`.

