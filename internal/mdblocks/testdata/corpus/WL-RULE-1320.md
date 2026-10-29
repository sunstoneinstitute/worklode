
A guide is the mirror image of `lode task brief`: an overview, per-file
orientation, an explicit flag on what deserves scrutiny, and skim spans on what
does not, shaping a reviewer's attention. `GET /api/v1/reviews/{id}/guide`
serves it as JSON; `lode review show --json` embeds it. crit has no guide
concept, so the guide is materialized as a file crit is pointed at. The guide
is optional everywhere: with no guide row `GET .../guide` returns 404.

The author proposes a guide with `PUT /api/v1/reviews/{id}/guide`, which only
the review's author may call. The proposal carries the unified diff at the
review's `base_ref` (empty for a document), so the server can check every
proposed skim against the files it touches. There is one guide per round; a
second proposal at the same round replaces the first. Files the server
classifies as mechanical get a whole-file skim decided by `policy` even when the
author proposed none. A document guide carries an overview and per-section
orientation and no skims.

Skims apply **only** on a review marked `focused`, and an unfocused guide
skims nothing. `focused` is the reviewer's declaration on its round: an
unfocused read returns the stored guide with its skims emptied, so one stored
guide serves both reads.

