
A firing records a **`wl:AutomationFired`** event: `wl:subject`, `wl:automationName`, `wl:fromState`/`wl:toState`, `prov:wasInformedBy` the triggering event, with the dedup identity of WL-SPEC-76#sec-10.3a. The `state_log` row is attributed to that event. The chain webhook, transition, `TaskTransitioned`, automation fired, transition is walkable in one log.

