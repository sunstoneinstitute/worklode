-- Spec 029 §8.3: new ingest sources follow the 004 pattern — one
-- events.source value per system. cms/ci/pipeline are signed webhook
-- emitters; prober is the poll prober reporting over the bearer API
-- (029 §3.2). Down restores the 0040 list.
ALTER TABLE events DROP CONSTRAINT events_source_check;
ALTER TABLE events ADD CONSTRAINT events_source_check
    CHECK (source IN ('github','flux','watcher','cli','system','web',
                      'catalog','cms','ci','pipeline','prober'));
