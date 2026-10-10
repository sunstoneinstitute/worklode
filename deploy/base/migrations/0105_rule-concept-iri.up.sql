-- A definition rule may name the ns/concept.ttl concept it defines
-- (WL-REQ-1368). The store validates the IRI against internal/ns.
ALTER TABLE rules ADD COLUMN concept_iri text;
