-- Keycloak identity claims recorded in full at login (spec 029 §6.2).
-- groups is the raw groups claim as a JSON array; email is the email claim.
-- NULL means the actor has not logged in since these columns shipped.
ALTER TABLE actors ADD COLUMN groups jsonb;
ALTER TABLE actors ADD COLUMN email text;
