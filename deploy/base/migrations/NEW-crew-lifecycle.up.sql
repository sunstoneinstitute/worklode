-- An invited participant before identity linkage (WL-REQ-1605). Not a
-- project_participants row: that table FKs actors, and an invitee has none
-- yet. One role label per invitation; no lead flag at all, so an invitation
-- cannot act as project lead, structurally. linked_actor set means the
-- invitation is history for that actor; linking never deletes the row.
CREATE TABLE crew_invitations (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id   text NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    display_name text NOT NULL,
    email        text,
    role         text NOT NULL CHECK (role IN ('member','editor','science-lead',
        'reporter','domain-expert','data-scientist','engineer')),
    invited_at   timestamptz NOT NULL,
    invited_by   text REFERENCES actors (id) ON DELETE RESTRICT,
    linked_actor text REFERENCES actors (id) ON DELETE RESTRICT,
    linked_at    timestamptz
);

CREATE UNIQUE INDEX crew_invitations_open_email
    ON crew_invitations (project_id, lower(email))
    WHERE linked_actor IS NULL AND email IS NOT NULL;

-- One lead-handoff ceremony (WL-REQ-1605). state 'pending' is the open
-- ceremony; completion requires incoming_accepted_at plus either
-- outgoing_accepted_at or both authorization slots. The partial unique
-- index allows one open ceremony per project.
CREATE TABLE crew_lead_handoffs (
    id                         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id                 text NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    from_actor                 text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    to_actor                   text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    state                      text NOT NULL CHECK (state IN ('pending','completed','cancelled')),
    outgoing_accepted_at       timestamptz,
    incoming_accepted_at       timestamptz,
    editor_authorized_by       text REFERENCES actors (id) ON DELETE RESTRICT,
    editor_authorized_at       timestamptz,
    science_lead_authorized_by text REFERENCES actors (id) ON DELETE RESTRICT,
    science_lead_authorized_at timestamptz,
    requested_by               text REFERENCES actors (id) ON DELETE RESTRICT,
    requested_at               timestamptz NOT NULL,
    resolved_at                timestamptz
);

CREATE UNIQUE INDEX crew_lead_handoffs_one_pending
    ON crew_lead_handoffs (project_id) WHERE state = 'pending';
