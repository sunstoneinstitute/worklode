
A declared edge is stored subject → object and owned by its subject. The declared direction of a property is the one whose subject is written knowing the object. Inverse properties are inferred through `owl:inverseOf`, never stored and never accepted on write. A symmetric property is stored once per pair.

The subject is the dependent or later node, so creating a relation never forces a revision of an accepted object. Only the subject's writes create or remove the edge, and the edge appears only in the subject's version history (WL-SPEC-77#sec-3a). A write that names an inverse property is refused, naming the declared property to use instead.

