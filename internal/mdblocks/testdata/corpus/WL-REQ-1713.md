
The lexical arm ranks by `ts_rank_cd(tsv, websearch_to_tsquery('simple', $q))`, max-pooled per subject as the dense arm is (WL-REQ-259). `websearch_to_tsquery` lets a user quote a phrase or negate a term. The configuration is `simple`, never `english`: `english` stems and drops stopwords, so `child_of` becomes `'child'` and matches the prose "the child task of a parent". Under `simple` it tokenises to `'child'`, `'of'` and matches only the identifier. The lexical arm exists to be exact; recall from stemming is the dense arm's job. A header match (`A`) outranks a body match (`B`).

