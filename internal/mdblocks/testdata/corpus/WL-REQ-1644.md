
**Amendment and supersession are relations between rules.** Documents do not amend or replace each other. A document arranges rules, and the rules carry these edges. An inverse (`amendedBy`, `supersededBy`) is read from the far end of the stored edge and is never stored.

`amends` runs from the amending rule A to the amended rule B: A changes how B is read without replacing it. An architect writes it by hand with `lode rule link WL-RULE-A --amends WL-RULE-B` and removes it with `lode rule unlink`. `lode show --inline` folds a rule's in-force amendments into it.

