-- Spec 024: copilot is an integratable harness; 0004's CHECK predates it.
-- Never edit a shipped migration — replace the constraint.
ALTER TABLE agent_sessions DROP CONSTRAINT agent_sessions_agent_known;
ALTER TABLE agent_sessions ADD CONSTRAINT agent_sessions_agent_known CHECK (agent IN
    ('claude-code','codex','copilot','cursor','aider',
     'opencode','pi','amp','other'));
