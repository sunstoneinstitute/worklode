
A search runs two retrievers over `index_chunks` and fuses their rankings. Neither arm is primary and neither is a fallback.

The dense arm embeds the query once with `Role = Query` and scores it by cosine similarity, max-pooled per subject (docs by `(doc_id, anchor)`, so one spec may return two sections). Max pooling lets one strong section surface its document. Pooling happens before ranking so a long document cannot accumulate RRF mass from many mediocre chunks. The similarity floor (default 0.35) is a candidate filter on this arm only; the candidate limit is 50 by default, independent of the caller's limit.

