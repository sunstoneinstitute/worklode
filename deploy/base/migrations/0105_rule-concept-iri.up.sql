-- A definition rule may name the ns/concept.ttl concept it defines
-- (WL-SPEC-77 §4d). The store validates the IRI against internal/ns.
ALTER TABLE rules ADD COLUMN concept_iri text;
