
An **automation** is a project's standing instruction for when to take one of its workflow's legal transitions. Automations are an ordered list per project, evaluated first-match-wins against the events the backbone already records, firing at most one transition per triggering event. **Automations choose which legal edge to take and when, never which edges exist.**

Automations cover what the hardwired movers (claiming, the PR-opened hook, `ResolveDelivery`, manual transitions) do not: policy at joints with more than one legal continuation, or where no observed fact ever arrives, such as `merged -> released` for a CLI project with no Flux frontier. Automations are conveniences. Every core edge stays manually takeable, so a broken automation or an unavailable LLM degrades to "nobody moved the task automatically".

