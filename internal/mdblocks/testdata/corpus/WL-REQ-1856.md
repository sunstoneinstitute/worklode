
A classifier picks the predicate an inline reference's edge carries. There are
two, both defined in WL-REQ-1857: `cue` matches fixed phrases taken from the
ontology's edge labels on the reference's line, and `jev` asks the TypeSafe
Jev model. Every predicate a classifier picks is stored as a row in
`ref_classifications`, including `references`, so misses can be counted
later. A row holds:

- the inline reference: node kind, id, field and version, line, target, and
  the line's text;
- the classifier: `cue` or `jev`, its `classifier_version`, and the model
  name. `classifier_version` is one integer per classifier, bumped whenever
  its code, prompt, criteria, cue labels or model changes. A golden test
  hashes the prompt text, criteria, cue labels and model name and fails when
  the hash changes without a bump; a code-only change is bumped by hand;
- the answer: `predicate`, `confidence`, and the full `probabilities` as jsonb
  (`cue` rows store confidence 1 and no probabilities);
- the outcome: `kept`, `adjusted` (with the predicate a person changed it
  to), `rejected` or `missed`, plus who caused it and when.

Writing a row records a `wl:ReferenceClassified` event, and changing its
outcome records a `wl:ClassificationRepaired` event, both through
`RecordEvent` with the row's fields as the payload, so `lode event tail` and
the event bus see every classification and repair.

A row is never deleted when its reference leaves the field, so past outcomes
stay countable. Re-running a newer `classifier_version` writes new rows and
keeps the old ones, so both versions can be compared on the same references.
