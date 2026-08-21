-- Record which plan declaration a minted task came from (025 §9.2).
--
-- Re-accepting an accepted plan mints the declarations that have no row yet
-- and leaves every existing row alone, so the accept transaction needs a key
-- saying which declaration a task already covers. That key is the
-- declaration's title as written at mint: stable under renumbering, and
-- recorded here rather than read back off tasks.title because a minted task's
-- title is execution fact and may be edited afterwards.
--
-- A soft-deleted task keeps its key: withdrawn work stays withdrawn, and a
-- re-accept must not resurrect it by minting the declaration a second time.

ALTER TABLE tasks ADD COLUMN plan_task_key text;

UPDATE tasks SET plan_task_key = title WHERE plan_doc IS NOT NULL;

-- Nothing before this migration stopped two declarations in one plan sharing
-- a title, so the backfill can collide. Keep the earliest row on the title
-- itself — that is the row a re-accept then matches the declaration to — and
-- disambiguate the rest, which leaves them minted and untouched, which is
-- what "leaves every existing row alone" asks for.
WITH dup AS (
    SELECT id, row_number() OVER (PARTITION BY plan_doc, title ORDER BY created_at, id) AS n
      FROM tasks
     WHERE plan_doc IS NOT NULL
)
UPDATE tasks t SET plan_task_key = t.title || ' #' || t.id
  FROM dup WHERE dup.id = t.id AND dup.n > 1;

ALTER TABLE tasks ADD CONSTRAINT tasks_plan_task_key_with_plan_doc
    CHECK ((plan_doc IS NULL) = (plan_task_key IS NULL));

CREATE UNIQUE INDEX tasks_plan_task_key ON tasks (plan_doc, plan_task_key)
    WHERE plan_doc IS NOT NULL;
