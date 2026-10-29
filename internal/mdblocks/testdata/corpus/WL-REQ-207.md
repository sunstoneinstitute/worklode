
The document write path refuses, and never rewrites:

- a `covers` entry that is not a plain reference, including one carrying `coverage:` or `fullCoverageWith:`;
- a plan carrying both `covers` and `implements`;
- a `defers` entry missing `spec` or `to`; a `spec` with no fragment; a `to` carrying a fragment; `defers` on a document that is not a plan; a plan deferring a section to itself; one section deferred to two owners.

