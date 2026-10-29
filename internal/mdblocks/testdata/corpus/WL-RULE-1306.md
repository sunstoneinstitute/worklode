
The [SPA](/projects/worklode/term/spa) uses the cockpit's canonical URL scheme unchanged:
`/projects/<proj>/<kind>/<n>` for tasks and documents, `/<n>/<ver>` for a
document version, and `/projects/<proj>/rule/<n>` for a rule.

These paths redirect to a canonical URL:

- `/projects/<proj>/task/<n>` to the task's own kind.
- `/projects/<proj>/<kind>/<n>` naming a task under another task kind to the task's own kind.
- `/tasks/{id}` to the task.
- `/docs/{ref}` to the document.
- `/rules/<ref>` to the rule.
- `/<ref>` to the task, document or rule the reference names.

Links already in GitHub comments, CLI output, the Morning Brief and documents
point at the public host with these paths, and must keep resolving.

Links the server generates keep using `LODE_PUBLIC_URL`. They reach
whichever surface serves the path (WL-REQ-1303).
