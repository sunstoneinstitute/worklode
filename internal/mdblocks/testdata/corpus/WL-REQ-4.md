
These never need a spec change, whatever files they touch:

| Kind | Test |
|---|---|
| A fix | the cited sentence exists and the old code violated it |
| A refactor | no visible noun, rule, promise or output meaning changes; tests unchanged or moved |
| Performance | same observable results, faster or cheaper |
| Rendering and copy | the same information, laid out or worded differently; `www/` copy |
| Tests, build, dependencies, CI plumbing | `_test.go`, `Makefile`, `go.mod`, workflow files, Dockerfiles |
| Operator configuration values | a value changes, the key and its meaning do not |

