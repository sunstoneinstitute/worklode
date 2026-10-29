
`LODE_INSTANCE_ENV` says which kind of instance the server is. It is unrelated to `LODE_CLUSTER_ENV_MAP` (WL-REQ-22).

| | |
|---|---|
| Env var | `LODE_INSTANCE_ENV` |
| Values | `dev`, `prod`. Any other value fails startup. |
| Default | `prod` |
| Set in | `dev` in the hzdev overlay and `docker-compose.yml`, `prod` in the admin overlay |

The default is `prod` because a prod instance that believes it is dev drops real decision records, and the reverse only asks for an unneeded justification. This rule defines the field only. Behaviour keyed on it, such as deletion requiring a justification on prod, belongs to the rule introducing that behaviour.

