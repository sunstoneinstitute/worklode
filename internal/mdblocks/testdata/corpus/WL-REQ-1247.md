
The backbone is the only place a document's `status` lives. Read it with
`lode doc show`, `lode show --inline`, `lode doc list` or the `/api/v1/docs`
routes. Nothing else is authoritative.

Every rule that reads a status runs inside the server, in the transaction that
changes it: the arrangement constraints on accept and on `lode doc revise --accept`,
header validation on `lode doc add` and `lode doc import`, amendment folding in
`lode show --inline`. No hook, CI job or script reads a status from a file, and
adding one is a regression.

Worklode never reads a document field back out of a git tree and never treats a
file copy as authoritative. There is no git-write side effect on
`lode doc accept` and no sync or export command that reconciles a file with the
backbone. A one-directional render into a working copy is allowed: it is derived output refreshed from the backbone, and
nothing gates on it.

