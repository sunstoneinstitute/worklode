-- Crew roles become a fixed vocabulary (WL-297): free-form labels made a
-- dropdown's option set, constrained here so the two cannot drift. The list
-- is drawn from the corpus (spec 029 §6.1, the cockpit design brief):
-- member is the generic default, the rest are the named story-project roles.
-- Widening means a new migration plus the UI option list and the store's
-- validParticipantRoles, which a test holds together.
--
-- Existing rows may carry labels outside the vocabulary. Each actor's
-- non-conforming rows are folded into one 'member' row — preferring the row
-- carrying is_lead so the lead flag survives — and the remainder deleted,
-- because the (project, actor, role) primary key leaves no second row to
-- keep. Role labels lost this way are recoverable from the crew events.

UPDATE project_participants p SET role = 'member'
 WHERE p.role NOT IN ('member','editor','science-lead','reporter','domain-expert','data-scientist','engineer')
   AND NOT EXISTS (SELECT 1 FROM project_participants m
                    WHERE m.project_id = p.project_id AND m.actor_id = p.actor_id
                      AND m.role = 'member')
   AND p.ctid = (SELECT q.ctid FROM project_participants q
                  WHERE q.project_id = p.project_id AND q.actor_id = p.actor_id
                    AND q.role NOT IN ('member','editor','science-lead','reporter','domain-expert','data-scientist','engineer')
                  ORDER BY q.is_lead DESC, q.ctid LIMIT 1);

DELETE FROM project_participants
 WHERE role NOT IN ('member','editor','science-lead','reporter','domain-expert','data-scientist','engineer');

ALTER TABLE project_participants
  ADD CONSTRAINT project_participants_role_check
  CHECK (role IN ('member','editor','science-lead','reporter','domain-expert','data-scientist','engineer'));
