
Every committed transition, whatever caused it, emits a **`wl:TaskTransitioned`** domain event in the same transaction as its `state_log` row: `wl:subject` the task, `wl:fromState`, `wl:toState`, `prov:wasAssociatedWith` the actor. This is the whole trigger surface of the automation engine. Keying off transitions lets automations compose with every existing mover without duplicating webhook correlation.

