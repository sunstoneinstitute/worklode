
Accepting a rule version, through `lode rule accept` or through the accept of a spec that arranges it, bumps the version of every accepted spec arranging the rule (WL-SPEC-77#sec-3a). Version N of a spec therefore still reads the rule versions it showed at N.

The in-place amendment gates (WL-SPEC-77#sec-10a, WL-SPEC-77#sec-10b) apply to the rule version. A version that trips a mechanically substantive check, or that its author or a fixer judges substantive, mints a review task for the rule's owner and the reviewers of every spec arranging it, and marks every plan covering the rule `stale`. The referrer set of a rule is everything that refers to it directly or through any spec arranging it.

