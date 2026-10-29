
Three regression tests hold hybrid search (section 15) to its purpose:

1. An identifier query (`child_of`, `LODE_BRANCH_TEMPLATE`) ranks the subject that literally contains the identifier first, and the test that proves it fails when the lexical arm is disabled.
2. A long document with many weak chunks does not outrank a short exact match. A test constructs that case. It checks that fusion runs over max-pooled subject rankings.
3. The lexical arm uses `simple`: a test asserts that the query `child_of` does not match prose reading "the child task of a parent". This test stops a later change of the configuration to `english`.

