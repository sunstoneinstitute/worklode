-- WL-REQ-109: a minted task carries prov:wasInformedBy to the event that
-- caused it as a relation on the task, not as a line in its body.
ALTER TABLE tasks ADD COLUMN informed_by bigint REFERENCES events(id) ON DELETE RESTRICT;

-- Backfill from the task.created event the mint attributed to the task.
UPDATE tasks t
   SET informed_by = src.id
  FROM (SELECT e.payload->>'task' AS task,
               substring(e.payload->>'prov:wasInformedBy' FROM '^wlid:event/(\d+)$')::bigint AS id
          FROM events e
         WHERE e.type = 'task.created'
           AND e.payload->>'prov:wasInformedBy' ~ '^wlid:event/\d+$') src
 WHERE t.id = src.task
   AND EXISTS (SELECT 1 FROM events WHERE id = src.id);

-- The Progress page's mints recorded no prov:wasInformedBy in the event
-- payload; their generated body named the event, so read it from there.
UPDATE tasks t
   SET informed_by = src.id
  FROM (SELECT id AS task,
               substring(body FROM '(?:^|\n)prov:wasInformedBy wlid:event/(\d+)(?:\n|$)')::bigint AS id
          FROM tasks
         WHERE informed_by IS NULL
           AND body ~ '(^|\n)prov:wasInformedBy wlid:event/\d+(\n|$)') src
 WHERE t.id = src.task
   AND EXISTS (SELECT 1 FROM events WHERE id = src.id);
