
`events.id` is assigned at insert and visible at commit, so a reader tracking `last_seen_id` can skip a slow transaction's lower id forever. Subscribers therefore read only below the commit horizon:

```sql
SELECT id, source, type, payload, received_at FROM events
 WHERE id > $1 AND txid < pg_snapshot_xmin(pg_current_snapshot())
 ORDER BY id LIMIT $2;
```

A visible row below the horizon is committed for good, so the visible log grows only at its tail. Consequences: aborted transactions leave holes (offsets are positions in the sequence); writers are not serialized; any long transaction anywhere holds the horizon back for every subscriber, which the lag metric makes visible. `graph_projection.last_txid` uses the same horizon for the graph projector's watermark.

