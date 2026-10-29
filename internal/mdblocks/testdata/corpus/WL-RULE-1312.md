
- **Server-side rendering in Go.** Go cannot render React components. It
  would mean writing each page twice (templ and React) or embedding a
  JavaScript runtime. An app used behind a login gains little from it.
- **A backend-for-frontend.** It adds a Node.js service, a second deploy and
  proxied auth, and moves view shaping away from `routeGuards` and task
  scope checks. Worklode has one backend. Composite endpoints the [SPA](/projects/worklode/term/spa) needs
  go in `internal/api`, next to `cockpit.go`, `overview.go` and
  `morningbrief.go`.
- **GraphQL.** It solves over- and under-fetching across many different
  clients. Worklode has one web client and a CLI that uses the REST API. It
  would add a second API surface, per-field authorization beside
  `routeGuards`, and N+1 query handling.
