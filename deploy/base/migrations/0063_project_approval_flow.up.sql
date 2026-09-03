-- Spec 029 §7.2: the project stores the *effective snapshot* of its
-- approval flow, so a later instance-configuration edit cannot silently
-- change an open review. approval_flow is the full snapshot (flow plus the
-- project reviewer template); name and rev are denormalized for listing
-- without unmarshalling. All NULL until a flow is applied.
ALTER TABLE projects ADD COLUMN approval_flow      jsonb;
ALTER TABLE projects ADD COLUMN approval_flow_name text;
ALTER TABLE projects ADD COLUMN approval_flow_rev  text;
