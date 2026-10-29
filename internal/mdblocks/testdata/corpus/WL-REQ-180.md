
`lode doc import` brings a markdown corpus into the backbone. It is a client-side two-pass walker over `internal/designdoc` writing through the public API only (pass 2 re-resolves header references once the whole corpus exists and writes each document's edges through `PUT /api/v1/docs/{id}/edges`). It is idempotent by slug, walks the top level only, states status rather than deriving it (a plan with no status imports `accepted`, needs the admin-only `doc.import` permission, and writes no task rows), and lands every document at version 1 with `last_revised_in` 1. Revision history is not reconstructed.

The importer knows one identity convention, the slug. Duplicate slugs are reconciled by hand before import. Import writes documents only, so a mis-import is reversed by deleting the document.

Numbering is the server's at write time, rule identity is enforced in the store, consolidation is `lode show --inline`, the header is validated on `lode doc add` and `lode doc import`.

A document created in the cockpit is reachable by exactly the same commands as an imported one.

