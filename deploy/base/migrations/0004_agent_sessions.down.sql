-- Drops the table only; agent_session.started / agent_session.ended rows in
-- events are not removed and become orphaned (no session for them to point at).
DROP TABLE agent_sessions;
