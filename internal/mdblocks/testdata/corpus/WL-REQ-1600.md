
`LODE_INSTANCE_ENV` (WL-REQ-22) decides whether a delete needs a justification: on `prod` a non-blank justification is required and its absence is a 422 naming the environment; on `dev` it is optional. The rule is enforced server-side. Undelete needs no justification anywhere.

