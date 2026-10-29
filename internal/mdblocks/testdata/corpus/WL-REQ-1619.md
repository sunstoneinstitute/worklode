
For each `wl:TaskTransitioned` event (WL-REQ-154), the engine evaluates the task's project automations in array order and fires the action of the first automation whose trigger and condition both hold. No match is a no-op. The subscriber consumes events in log order, so evaluation is totally ordered and nondeterminism enters only through an automation's own prompt.

Automation-fired transitions are attributed to the engine's own service actor, and **the engine never evaluates an event whose actor is itself.** One event, at most one automation-fired transition. A project wanting `merged -> deployed_prod -> released` automated writes one automation per hop and lets a non-automation mover supply the intermediate trigger.

