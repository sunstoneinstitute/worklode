
The crit bridge is client-side only, in `internal/cmd`: resolve the review,
fetch the guide, materialize it to a temp file outside the working tree; launch
crit against the subject with `on_finish_*` hooks pointing at `lode review
import`; on finish crit pipes its JSON to stdin, and ingest POSTs a round with
`client=crit` and opens a thread per open comment, with no intent, which `lode-server`
classifies (WL-REQ-1885); `lode review
wait` long-polls `/events` on the author side. crit anchors documents by line,
so `lode doc show <ref> --anchors <map>` writes the document plus a sidecar mapping
line ranges to the subject, version and byte offsets WL-REQ-1317 anchors to, and ingestion
maps a comment spanning lines 412 to 414 to a thread on those bytes with crit's quote. The export is read-only and regenerable; nothing
is read back from it. A review driven over the API and the same review driven
from a recorded crit round produce equivalent rows.
