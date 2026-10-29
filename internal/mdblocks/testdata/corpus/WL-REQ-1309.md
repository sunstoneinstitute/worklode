
The [SPA](/projects/worklode/term/spa)'s API client is generated from the
OpenAPI 3.1 document at `GET /api/v1/openapi.json`, with `openapi-typescript`
for the types and `openapi-fetch` for the calls. That document is reflected
from the Go types `internal/api` serializes and the typed params structs its
handlers decode, so the client matches the wire shapes. The types are not
generated from `ns/*.ttl`, which describes the domain and can differ from the
wire shape in field names, optionality and nesting. Enum values the SPA needs
for pickers and validation may be generated from `ns/concept.ttl`.

The document declares a cookie security scheme next to the bearer scheme,
and a session-only route (WL-REQ-1771) lists only the cookie scheme.

Generated files are committed. CI regenerates them and fails on drift, like
`*_templ.go`.

The SPA's mock API ([MSW](/projects/worklode/term/msw)) stays for development
and tests and uses the generated types, so a mock that drifts from the Go
shapes fails the type check.
