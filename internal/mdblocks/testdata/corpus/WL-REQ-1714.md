
The two rankings are fused by reciprocal rank fusion:

```
score(s) = sum over arms of w_arm / (k + rank_arm(s))      k = 60, w = 1.0 both arms
```

Implemented as a `FULL OUTER JOIN` on the subject key with missing ranks coalesced to zero, ordered by summed score and cut to the caller's `LIMIT`. Fusion uses ranks because cosine similarity and `ts_rank_cd` live on incomparable scales and per-query min-max normalisation is unstable. On the reference fixture, query `child_of` ranks the defining section third on the dense arm and first after fusion, with no query classifier or routing heuristic.

Kind and project filters apply inside both arms; `project IS NULL` keeps org-wide skills visible inside a project-scoped search. pgvector applies `WHERE` after walking the HNSW index, so a selective filter can return fewer than 50 dense candidates; the fix if it appears is `hnsw.iterative_scan`. Soft-deleted docs, tasks and skills keep their chunks until convergence removes them, so the query filters on the subject's own `deleted_at`.

