
The SPA body editor edits a document, rule or task body under a lock that one
person holds at a time, and has no live co-editing. Opening a subject in the
editor takes its lock when the lock is free. Anyone else who opens the subject
sees who holds the lock and a read-only view, and cannot change the body
until the lock is free.

The holder's page checks in regularly, saying whether it has unsaved changes.
`lode-server` releases a lock when the holder saves and leaves, closes the
editor or navigates away, with or without unsaved changes; after an hour in
which the page reported no unsaved changes and no activity, even while it
keeps checking in; and 24 hours after the last check-in in any case. The subject's
owner or an admin can release a held lock at any time, and the lock status a
caller reads says whether that caller may (`canUnlock`). A holder whose lock
was released keeps their unsaved changes as a browser draft, shown read-only
until they take the lock again.

An editor save carries its lock session, and `lode-server` refuses it with
`409 Conflict` when that session is not a live session of the lock's holder.
Writes through the CLI, the API or MCP carry no session, take no lock and are
never refused because someone holds one.

Every editor save is also a compare-and-swap. It carries the `body_sha256`
the editor loaded (WL-REQ-1867), and `lode-server` answers `409 Conflict`
when the SHA-256 of the stored body differs, for example because an agent
wrote through the CLI or API while the lock was held. The check is on the
body text, not on a version number, because a save to a draft rule rewrites
that draft in place and leaves its version number unchanged (WL-REQ-1298). A
document or plan body edit is the document's next version (WL-REQ-164), and
the same check covers it. On a conflict the
editor keeps the unsaved blocks and shows the newer stored text beside them;
it never overwrites silently.

Review threads are not edits to the body and need no lock. Minutes and notes
keep their own drafting contract.
