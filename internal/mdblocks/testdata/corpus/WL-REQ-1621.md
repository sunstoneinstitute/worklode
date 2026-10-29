
The automation subscriber is at-least-once. Redelivery of an event already evaluated by the pass of WL-REQ-156 is a no-op through two layers: the fired event's dedup identity (`source = watcher`, `external_id = automations:<triggering event id>:<automation name>`) and the guard's from-state check. The offset commits after the pass regardless of outcome. A poison event must not wedge the log.

