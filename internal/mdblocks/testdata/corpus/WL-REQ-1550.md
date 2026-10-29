
`lode doctor` reports whether the `[gate]` table (WL-REQ-6) parses. A
repository with a `[gate]` table must also require pull requests on its default
branch, since the gate runs on pull requests, and `lode doctor` warns when the
repository's branch rules do not require them. It reads the rules the server
last observed on the default branch (`GET /api/v1/repos/branch-rules`,
WL-REQ-1340). The warning does not fail the run. When the server has no
observation, or is unreachable, `lode doctor` reports the requirement as
unknown. Only rulesets count: a pull request requirement set by classic branch
protection reads as not required.

