
The single piece of server logic that turns recorded delivery facts into forward-only task state transitions along a repo's `done_state` path. Webhook handlers record facts and call it, so the order in which events arrive does not matter.

