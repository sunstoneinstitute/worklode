
`ID()` names the embedding space and has the shape `openai:<model>@<width>@<host><path>[#<prefix digest>]`, where the digest is the first 8 hex characters of SHA-256 over query prefix, NUL, document prefix, omitted when both are empty.

