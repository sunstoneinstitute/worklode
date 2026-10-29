
Every design document has a version-free canonical IRI that denotes the current version and one immutable IRI per version:

```
/docs/WL-SPEC-77        canonical
/docs/WL-SPEC-77/3      snapshot
```

The grammar and the `wlid:` base are in WL-SPEC-79#sec-10.

The canonical IRI is the only IRI anything links to by default. Versioned IRIs appear only in pinned claims (WL-REQ-177). This is a named exception to rdf-registry ADR-0006's version-free rule.

