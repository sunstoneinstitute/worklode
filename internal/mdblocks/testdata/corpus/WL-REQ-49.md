
Every request resolves to a `Subject` (actor, admin flag, bound task if any). `internal/api/authz.go` holds a `grants` table mapping permission to the roles that hold it and a default-deny `Decide`. The only roles are `user` and `admin`, the two Keycloak client roles. Adding a role means editing that table, never adding a check inside a handler. A narrower per-actor RBAC model is introduced only when an unattended agent actor needs a grant `sandbox` lacks; that narrowing is the change that introduces it.

