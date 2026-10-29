
On a frame naming a spec the page fetches the fragment `GET
/projects/{id}/progress/spec/{doc}` and swaps the row in place; on any frame it
refreshes the counts, section bar and rally band from `GET
/projects/{id}/progress/summary`. Both fragments read only the specs they
render and the plans covering them. Expansion state, a pinned tooltip and an
in-flight confirmation survive the swap. After a drop the page reconnects with
`Last-Event-ID`, refreshes every row once, and shows "reconnecting" inside the
rally band.

