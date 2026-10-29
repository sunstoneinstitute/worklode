
`GET /projects/{id}/progress/events` is a Server-Sent Events stream shaped like
`GET /api/v1/events/stream` (a poll over the events log with `Last-Event-ID`
resume and a 30-second heartbeat), web-session guarded and filtered to one
project. Without `Last-Event-ID` it starts at the head of the log. The server
resolves each event's task or document id to the project, plan and specs in
one batched read per poll, and emits one frame per event touching the project;
an event touching no row of the project advances the cursor and sends nothing.

| Frame field | Content |
|---|---|
| `event` | the backbone event type |
| `task`, `state` | the task touched and its state after the event |
| `plan`, `specs` | the plan the task or document belongs to and the specs it covers |
| `rally` | the active rally's band when a rally or member was touched |
| `at` | the event timestamp |

