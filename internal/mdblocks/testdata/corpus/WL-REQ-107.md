
The model is a Kafka consumer group in one row. **Read** takes the batch after `last_read_offset` below the commit horizon (WL-REQ-106) and advances `last_read_offset`. **Ack** advances `last_acked_offset` forward only to the newest completely processed offset. **Restart** resumes at `last_acked_offset`; everything read but unacked is redelivered. Delivery is in order and at least once, so handlers must be idempotent.

A subscriber row is created by its owner at startup with `ON CONFLICT DO NOTHING`, so a new subscriber replays the whole log; `lode event seek` chooses otherwise. `GET /api/v1/events/stream` and `lode event tail --follow` serve the log live.

