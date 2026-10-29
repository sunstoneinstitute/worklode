
A bare note is a thread with `review_id IS NULL`: `lode doc note`
(WL-SPEC-77) writes into `review_threads` with the same anchor, author and
resolution verb and nothing obliged to clear it. A note never blocks execution.
"What is outstanding on §4.3, from any source" is one query that returns notes
and review threads together.

