
Implementation is one statement, answered by a query and never a node: "Component A implemented Section B by deploying Deliverable C to Environment D."

```sparql
?component   wl:implements  ?section .
?deliverable wl:deliveredBy ?component ; dct:relation ?artifact , ?env .
?deployment  prov:used ?artifact ; wl:toEnvironment ?env .
```

This answers "is this spec implemented?" per environment. For an Effect the last two clauses bind a Commit instead of an Artifact.

