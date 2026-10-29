
Every write is a `POST` under `/projects/{id}/progress/`. One gate
(`beginJSONPost`, `internal/api/webform.go`) enforces rules 1 to 4 and 6 for
all of them, and for the document and task pages' Accept and Publish
buttons. Each route obeys all of:

1. No write on GET; any other method is a 405.
2. Same-origin, strictly: when `Sec-Fetch-Site` is present it must be
   `same-origin`, so `none` is refused, since a script-issued fetch is never a
   user-typed navigation (form routes keep accepting `none` for bookmarks);
   without the header the form routes' origin check applies. A refusal is a
   403.
3. The page script sends `X-Requested-With: lode-cockpit` and the route
   requires it, answering 403 when it is missing; a form cannot set it and a
   cross-origin script triggers a preflight the server never answers.
4. JSON body of at most 64 KiB, JSON reply: 200 with the new fact's id, 403
   when the actor may not act, 404 for a document or task outside the
   project, 409 with a reason when the backbone refuses, 415 for any body
   that is not `application/json`, 422 for a malformed body or an unknown
   field. Never HTML.
5. Guarded in `routeGuards`; the owner gate for acceptance stays in the store,
   so the page cannot grant what `lode doc accept` would refuse.
6. The actor is the session subject; a body naming an actor is a 422.
7. Idempotent: accepting an accepted document is 409, re-adding to a rally is
   a no-op, minting a planning task when one is open returns it, confirming an
   active rally is 409.
8. No `next` or return-URL parameter.
9. The merge route records the session's actor before the outward call and
   never uses a page-supplied token.

Rules 1 to 4 are what stand between a hostile page and a write; a change that
relaxes one must say why the rest suffice. `frame-ancestors 'none'` stays on
every response including JSON replies and fragments.

