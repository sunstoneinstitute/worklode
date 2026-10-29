
Pages are HTML assembled on the server from the same `assemble*` functions the
JSON API uses, so presentation logic has one source. `internal/store` readers
feed `internal/api`'s assembly into the shared `internal/model` projection
shapes; `internal/api/render.go` maps them onto `internal/ui` view types;
`internal/ui` renders typed, compiled templ components that take those views as
arguments and escape interpolated values by default. `internal/ui` depends on
stdlib, `internal/model` and the templ runtime only. `internal/api` imports
`internal/ui`; the reverse import is forbidden.

