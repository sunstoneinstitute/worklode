
An observed-but-unasserted edge that the architecture tolerates is a declared-layer fact, crit-reviewed, provenanced and expirable. The tolerated edge must stay unasserted, otherwise it would become intent and report as stale-intent drift the moment code dropped it. A triple-term annotation asserts its inner triple, so the deviation names its edge with RDF reification (`rdf:subject`, `rdf:predicate`, `rdf:object`) in plain RDF 1.1.

- Home graph: the declared named graph of the sanctioning ADR, under the same crit gate. Superseding or removing the ADR removes the suppression.
- Expiry: optional `dct:valid` (`xsd:date`). Past it the violation re-surfaces. Absent means indefinite, and every deviation stays listable through `lode graph drift --acknowledged`.
- Scope: any s/p/o; only the `dct:requires` violation query consumes it (`internal/overview`).

