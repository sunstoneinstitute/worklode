
A review thread anchors to content because positions do not survive the
author changing the artifact.

Document and rule threads anchor to the subject, one version of it, and a
UTF-8 byte range in that version's stored Markdown, plus the quoted text of
that range. A thread starts from a text selection in the body editor or the
read-only view, and the selection's byte range is the anchor. A spec is read rendered, so a
selection on a spec is a byte range of that document version's rendered body: `lode-server`
maps it onto the arranged rule's text or the spec's template text it came from, and refuses
a range that crosses from one into another. Bytes inside an
arranged rule's text belong to the rule, so a comment on a rendered spec
section is a thread on the rule. Bytes of a spec's own template text belong to
the document. An accepted version is locked, so its ranges never move. A draft
version is rewritten in place, so every rewrite re-finds each open thread's
quoted text through the recovery ladder: when the quote is found the range
moves to it, and when it is not found the thread becomes `obsolete`. The body
editor highlights each thread's range over the text it maps to and stores no
comment marks in the Markdown. Task bodies get no threads anchored this way.
Threads written before this version keep their section anchor and quote.

Change threads anchor to `(repo, path, side, line_number)` plus `anchor_text`
(the exact anchored line at creation) and the new-side blob sha; after a
force-push the text re-anchors through the recovery ladder.

A thread whose anchor no longer resolves becomes `obsolete`; it is never
deleted or re-pointed.
