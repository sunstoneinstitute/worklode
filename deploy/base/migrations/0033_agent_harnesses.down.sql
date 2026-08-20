-- Restore 0004's original agent_sessions_agent_known list verbatim.
ALTER TABLE agent_sessions DROP CONSTRAINT agent_sessions_agent_known;
ALTER TABLE agent_sessions ADD CONSTRAINT agent_sessions_agent_known CHECK (agent IN
    ('claude-code','codex','cursor','aider',
     'opencode','pi','amp','other'));
