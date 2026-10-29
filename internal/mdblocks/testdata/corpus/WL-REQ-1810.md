
A form in the SPA is a React component that holds its values and field
errors in plain React state, with no form library. It submits to an existing `/api/v1`
write route, and its body has the generated request type from
`web/app/src/api/schema.d.ts`, so a field the Go type lacks fails `pnpm
typecheck`. A form that has no route to submit to gets one in `internal/api`
in the same change that ports its page (WL-REQ-1305).

The route's validation decides. The form may check required fields before it
submits, to save a round trip, but a rule the server enforces is never
enforced only in the browser. Values the person does not choose, such as the
author, the creation date and a new record's id, are set by the server.

A form moves to TanStack Form only when it needs repeating field groups or
per-field validation against the server while the person types.

An edit form opens with the record from that record's own `GET` route, read
fresh each time it opens, so it never shows a cached copy.

The design repo's prototype builds every form at run time from a schema the
server sends, so that a separate backend developer can add a field without a
frontend change. The SPA is embedded in `lode-server` (WL-REQ-1310), so the
two always ship together and that gains nothing here. Worklode has a handful
of forms, written by the people who write the routes.
